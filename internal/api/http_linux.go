// Package api exposes the core only through verified, revocable sessions.
package api

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/pbuzdygan/filedeck/internal/core"
	"github.com/pbuzdygan/filedeck/internal/identity"
	"github.com/pbuzdygan/filedeck/internal/storage"
	"github.com/pbuzdygan/filedeck/internal/web"
	"golang.org/x/sys/unix"
)

const RequestTimeout = 60 * time.Second
const maxJSON = 16 << 10

type Config struct {
	Origin        string
	InsecureLocal bool
	ProxyCIDR     string
	Limits        core.Limits
	// AllowSetup lets a store without an active administrator start in setup
	// mode: the first administrator is created with a one-time code (SetupCode).
	AllowSetup bool
}
type API struct {
	store  *identity.Store
	files  *core.Service
	config Config
	origin *url.URL
	proxy  netip.Prefix
	cookie string
	// Revocation is serialized with publication, not downloads or network body
	// reads. In-flight reads may finish; revoked sessions cannot publish later.
	security sync.RWMutex
	capacity chan struct{}
	logins   *limiter
	mux      *http.ServeMux
	setupMu  sync.Mutex
	setup    string
	// texts bounds concurrent text saves, each buffering up to maxTextJSON.
	texts chan struct{}
	// Public links: password attempts, unlock cookie key, download slots.
	unlocks   *limiter
	unlockKey []byte
	downloads chan struct{}
	// timeout bounds each request (RequestTimeout; shorter in tests).
	timeout time.Duration
}

// newSetupCode returns 80 random bits as four groups of Crockford-like base32.
func newSetupCode() string {
	const alphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	b := make([]byte, 16)
	rand.Read(b) // crypto/rand.Read never fails on Linux (panics otherwise)
	out := make([]byte, 0, 19)
	for i, c := range b {
		if i > 0 && i%4 == 0 {
			out = append(out, '-')
		}
		out = append(out, alphabet[int(c)%len(alphabet)])
	}
	return string(out)
}
func normalizeCode(c string) string {
	return strings.ToUpper(strings.NewReplacer("-", "", " ", "").Replace(c))
}

// SetupCode returns the pending one-time setup code, or "" once configured.
func (a *API) SetupCode() string {
	a.setupMu.Lock()
	defer a.setupMu.Unlock()
	return a.setup
}

func New(store *identity.Store, files *core.Service, c Config) (*API, error) {
	u, e := url.Parse(c.Origin)
	if e != nil || u.Host == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || (u.Scheme != "https" && u.Scheme != "http") {
		return nil, errors.New("origin must be a canonical http(s) origin without path")
	}
	if c.Limits.MaxChunkBytes < 1 {
		return nil, errors.New("upload limits required")
	}
	if c.InsecureLocal {
		ip, _ := netip.ParseAddr(u.Hostname())
		if u.Scheme != "http" || (u.Hostname() != "localhost" && !ip.IsLoopback()) || c.ProxyCIDR != "" {
			return nil, errors.New("insecure mode requires an HTTP loopback origin without proxy")
		}
	} else if u.Scheme != "https" {
		return nil, errors.New("HTTPS origin required")
	}
	a := &API{store: store, files: files, config: c, origin: u, cookie: "__Host-filedeck", capacity: make(chan struct{}, 64), texts: make(chan struct{}, 4), logins: newLimiter(), mux: http.NewServeMux(),
		unlocks: newLimiter(), unlockKey: make([]byte, 32), downloads: make(chan struct{}, maxPublicDownloads), timeout: RequestTimeout}
	rand.Read(a.unlockKey)
	if c.InsecureLocal {
		a.cookie = "filedeck-local"
	}
	if c.ProxyCIDR != "" {
		a.proxy, e = netip.ParsePrefix(c.ProxyCIDR)
		if e != nil {
			return nil, errors.New("invalid proxy CIDR")
		}
	}
	if e = store.RequireInitialized(); e != nil {
		if !c.AllowSetup {
			return nil, e
		}
		a.setup = newSetupCode()
	}
	users, e := store.Users()
	if e != nil {
		return nil, e
	}
	for _, user := range users {
		if e = a.syncUser(user); e != nil {
			return nil, e
		}
	}
	a.routes()
	return a, nil
}
func (a *API) syncUser(u identity.User) error {
	if u.Disabled {
		return a.files.SetPermissions(u.ID, nil)
	}
	return a.files.SetPermissions(u.ID, u.Spaces)
}
func (a *API) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'; sandbox")
	w.Header().Set("Referrer-Policy", "no-referrer")
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		fail(w, 400, "invalid_peer")
		return
	}
	peer, err := netip.ParseAddr(host)
	if err != nil {
		fail(w, 400, "invalid_peer")
		return
	}
	peer = peer.Unmap()
	// Liveness probe for container health checks: loopback only, no data.
	if r.URL.Path == "/healthz" && peer.IsLoopback() {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = io.WriteString(w, "ok\n")
		return
	}
	if !strings.EqualFold(r.Host, a.origin.Host) {
		fail(w, 421, "unexpected_host")
		return
	}
	if a.config.InsecureLocal {
		if !peer.IsLoopback() {
			fail(w, 403, "loopback_required")
			return
		}
	} else if a.proxy.IsValid() {
		if !a.proxy.Contains(peer) {
			fail(w, 403, "untrusted_proxy")
			return
		}
	} else if r.TLS == nil {
		fail(w, 400, "tls_required")
		return
	}
	// Cross-site navigation may open the interface; it never reaches the API.
	if r.Header.Get("Sec-Fetch-Site") == "cross-site" && strings.HasPrefix(r.URL.Path, "/api/") {
		fail(w, 403, "cross_site_request")
		return
	}
	if r.Method != "GET" && r.Method != "HEAD" && r.Header.Get("Origin") != a.config.Origin {
		fail(w, 403, "origin_required")
		return
	}
	select {
	case a.capacity <- struct{}{}:
		defer func() { <-a.capacity }()
	default:
		fail(w, 503, "request_capacity")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), a.timeout)
	defer cancel()
	r = r.WithContext(ctx)
	deadline := time.Now().Add(a.timeout)
	rc := http.NewResponseController(w)
	_ = rc.SetReadDeadline(deadline)
	_ = rc.SetWriteDeadline(deadline)
	a.mux.ServeHTTP(w, r)
}
func reply(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func fail(w http.ResponseWriter, status int, code string) {
	reply(w, status, map[string]string{"error": code})
}

// classify maps an error to an HTTP status and a stable code without details.
func classify(err error) (int, string) {
	var tooLarge *http.MaxBytesError
	switch {
	case errors.As(err, &tooLarge):
		return 413, "body_too_large"
	case errors.Is(err, identity.ErrAuth):
		return 401, "unauthorized"
	case errors.Is(err, identity.ErrBusy), errors.Is(err, core.ErrQuota), errors.Is(err, core.ErrJobLimit):
		return 429, "capacity_exceeded"
	case errors.Is(err, identity.ErrInvalid), errors.Is(err, storage.ErrPath), errors.Is(err, storage.ErrType):
		return 400, "invalid_input"
	case errors.Is(err, core.ErrSize):
		return 413, "upload_size"
	case errors.Is(err, storage.ErrCopyLimit):
		return 413, "copy_limit"
	case errors.Is(err, core.ErrLinkNotFound), errors.Is(err, storage.ErrGone):
		return 404, "link_unavailable"
	case errors.Is(err, core.ErrLinkPassword):
		return 403, "link_wrong_password"
	case errors.Is(err, core.ErrLinkInput):
		return 400, "invalid_input"
	case errors.Is(err, core.ErrDenied), errors.Is(err, os.ErrPermission), errors.Is(err, unix.ELOOP), errors.Is(err, unix.EXDEV):
		return 403, "forbidden"
	case errors.Is(err, storage.ErrReadOnly):
		return 403, "read_only"
	case errors.Is(err, storage.ErrChanged):
		return 409, "changed"
	case errors.Is(err, core.ErrNotText):
		return 415, "not_text"
	case errors.Is(err, core.ErrTooLarge):
		return 413, "too_large"
	case errors.Is(err, core.ErrNotFound), errors.Is(err, identity.ErrNotFound), errors.Is(err, os.ErrNotExist), errors.Is(err, core.ErrNoSpace), errors.Is(err, core.ErrTrashNotFound), errors.Is(err, core.ErrJobNotFound):
		return 404, "not_found"
	case errors.Is(err, identity.ErrLastAdmin):
		return 409, "last_admin"
	case errors.Is(err, storage.ErrConflict), errors.Is(err, core.ErrOffset), errors.Is(err, core.ErrIncomplete), errors.Is(err, identity.ErrExists):
		return 409, "conflict"
	case errors.Is(err, storage.ErrLimit):
		return 422, "listing_limit"
	case errors.Is(err, unix.ENOSPC), errors.Is(err, unix.EDQUOT):
		return 507, "no_space"
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return 408, "request_timeout"
	}
	return 500, "internal_error"
}
func problem(w http.ResponseWriter, err error) {
	status, code := classify(err)
	if status == 429 {
		w.Header().Set("Retry-After", "5")
	}
	fail(w, status, code)
}

// jobView adds the error code of a failed transfer.
type jobView struct {
	core.Job
	Error string `json:"error,omitempty"`
}

func viewJob(j core.Job) jobView {
	v := jobView{Job: j}
	if j.Error != nil {
		_, v.Error = classify(j.Error)
	}
	return v
}
func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	return decodeLimit(w, r, v, maxJSON)
}
func decodeLimit(w http.ResponseWriter, r *http.Request, v any, limit int64) bool {
	typ, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || typ != "application/json" {
		fail(w, 415, "json_required")
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, limit)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if err = d.Decode(v); err == nil {
		var extra any
		err = d.Decode(&extra)
		if errors.Is(err, io.EOF) {
			return true
		}
		if err == nil {
			err = identity.ErrInvalid
		}
	}
	var large *http.MaxBytesError
	if errors.As(err, &large) {
		problem(w, err)
	} else {
		fail(w, 400, "invalid_json")
	}
	return false
}
func (a *API) setCookie(w http.ResponseWriter, token string, expires time.Time) {
	maxAge := int(identity.SessionLifetime.Seconds())
	if token == "" {
		maxAge = -1
		expires = time.Unix(1, 0)
	}
	http.SetCookie(w, &http.Cookie{Name: a.cookie, Value: token, Path: "/", Secure: !a.config.InsecureLocal, HttpOnly: true, SameSite: http.SameSiteStrictMode, MaxAge: maxAge, Expires: expires})
}
func (a *API) rate(w http.ResponseWriter, r *http.Request) bool {
	ip, _, _ := net.SplitHostPort(r.RemoteAddr)
	if !a.logins.allow(ip) {
		w.Header().Set("Retry-After", "60")
		fail(w, 429, "login_rate_limit")
		return false
	}
	return true
}

type authenticated func(http.ResponseWriter, *http.Request, identity.Login)

func (a *API) auth(fn authenticated, exclusive, admin bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if exclusive {
			// Never let a slow client hold the account/publication lock.
			if r.URL.Path != "/api/auth/logout" {
				typ, _, e := mime.ParseMediaType(r.Header.Get("Content-Type"))
				if e != nil || typ != "application/json" {
					fail(w, 415, "json_required")
					return
				}
				data, e := io.ReadAll(http.MaxBytesReader(w, r.Body, maxJSON))
				if e != nil {
					problem(w, e)
					return
				}
				_ = r.Body.Close()
				r.Body = io.NopCloser(bytes.NewReader(data))
			}
			a.security.Lock()
			defer a.security.Unlock()
		}
		if err := r.Context().Err(); err != nil {
			problem(w, err)
			return
		}
		cookies := r.CookiesNamed(a.cookie)
		if len(cookies) != 1 {
			fail(w, 401, "unauthorized")
			return
		}
		login, err := a.store.Authenticate(cookies[0].Value)
		if err != nil {
			problem(w, err)
			return
		}
		if r.Method != "GET" && r.Method != "HEAD" && !identity.ValidCSRF(login.Token, r.Header.Get("X-CSRF-Token")) {
			fail(w, 403, "csrf_required")
			return
		}
		if admin && !login.User.Admin {
			fail(w, 403, "admin_required")
			return
		}
		fn(w, r, login)
	}
}
func (a *API) routes() {
	a.mux.HandleFunc("POST /api/auth/login", func(w http.ResponseWriter, r *http.Request) {
		if !a.rate(w, r) {
			return
		}
		var in struct {
			Username string `json:"username"`
			Password string `json:"password"`
		}
		if !decode(w, r, &in) {
			return
		}
		// Login can evict or replace sessions; serialize this with publication too.
		a.security.Lock()
		defer a.security.Unlock()
		login, err := a.store.Login(r.Context(), in.Username, in.Password)
		if err != nil {
			problem(w, err)
			return
		}
		// Successful login replaces a presented session rather than accumulating it.
		if old, err := r.Cookie(a.cookie); err == nil {
			if err = a.store.Logout(old.Value); err != nil && !errors.Is(err, identity.ErrAuth) {
				_ = a.store.Logout(login.Token)
				problem(w, err)
				return
			}
		}
		a.setCookie(w, login.Token, login.Expires)
		reply(w, 200, login)
	})
	a.mux.HandleFunc("GET /api/setup", func(w http.ResponseWriter, r *http.Request) {
		reply(w, 200, map[string]bool{"required": a.SetupCode() != ""})
	})
	a.mux.HandleFunc("POST /api/setup", a.completeSetup)
	a.mux.HandleFunc("GET /{$}", web.Index)
	a.mux.HandleFunc("GET /assets/{name}", web.Asset)
	a.mux.HandleFunc("GET /api/auth/me", a.auth(func(w http.ResponseWriter, r *http.Request, l identity.Login) {
		type limits struct {
			MaxFileBytes  int64 `json:"max_file_bytes"`
			MaxChunkBytes int64 `json:"max_chunk_bytes"`
		}
		reply(w, 200, struct {
			identity.Login
			Limits limits `json:"limits"`
		}{l, limits{a.config.Limits.MaxFileBytes, a.config.Limits.MaxChunkBytes}})
	}, false, false))
	a.mux.HandleFunc("POST /api/auth/logout", a.auth(func(w http.ResponseWriter, r *http.Request, l identity.Login) {
		if err := a.store.Logout(l.Token); err != nil {
			problem(w, err)
			return
		}
		a.setCookie(w, "", time.Time{})
		w.WriteHeader(204)
	}, true, false))
	a.mux.HandleFunc("POST /api/auth/password", a.auth(func(w http.ResponseWriter, r *http.Request, l identity.Login) {
		if !a.rate(w, r) {
			return
		}
		var in struct {
			Current string `json:"current_password"`
			Next    string `json:"new_password"`
		}
		if !decode(w, r, &in) {
			return
		}
		if err := a.store.ChangePassword(r.Context(), l.User.ID, in.Current, in.Next); err != nil {
			passwordProblem(w, err)
			return
		}
		a.setCookie(w, "", time.Time{})
		w.WriteHeader(204)
	}, true, false))
	a.mux.HandleFunc("GET /api/files", a.auth(a.list, false, false))
	a.mux.HandleFunc("POST /api/folders", a.publication(func(w http.ResponseWriter, r *http.Request, l identity.Login) {
		var in struct {
			Space string `json:"space"`
			Path  string `json:"path"`
		}
		if !decode(w, r, &in) {
			return
		}
		if err := a.files.Mkdir(r.Context(), l.User.ID, in.Space, in.Path); err != nil {
			problem(w, err)
			return
		}
		w.WriteHeader(201)
	}))
	a.mux.HandleFunc("GET /api/content", a.auth(a.content, false, false))
	a.mux.HandleFunc("GET /api/spaces", a.auth(a.spaces, false, false))
	a.mux.HandleFunc("GET /api/preview", a.auth(a.preview, false, false))
	a.mux.HandleFunc("GET /api/search", a.auth(func(w http.ResponseWriter, r *http.Request, l identity.Login) {
		q, err := url.ParseQuery(r.URL.RawQuery)
		if err != nil || len(q["space"]) != 1 || len(q["q"]) != 1 || len(q["path"]) > 1 || len(q) != 2+len(q["path"]) {
			fail(w, 400, "invalid_query")
			return
		}
		dir := "."
		if p := q["path"]; len(p) == 1 {
			dir = p[0]
		}
		hits, truncated, err := a.files.Search(r.Context(), l.User.ID, q["space"][0], dir, q["q"][0])
		if err != nil {
			problem(w, err)
			return
		}
		reply(w, 200, map[string]any{"results": hits, "truncated": truncated})
	}, false, false))
	a.mux.HandleFunc("POST /api/transfers", a.publication(func(w http.ResponseWriter, r *http.Request, l identity.Login) {
		// Either a single from/to or a list of items (selection).
		var in struct {
			Kind  string         `json:"kind"`
			From  *core.Endpoint `json:"from"`
			To    *core.Endpoint `json:"to"`
			Items []core.Pair    `json:"items"`
		}
		if !decodeLimit(w, r, &in, 1<<20) {
			return
		}
		pairs := in.Items
		if in.From != nil || in.To != nil {
			if in.From == nil || in.To == nil || len(pairs) != 0 {
				fail(w, 400, "invalid_input")
				return
			}
			pairs = []core.Pair{{From: *in.From, To: *in.To}}
		}
		j, err := a.files.StartTransfers(l.User.ID, in.Kind, pairs)
		if err != nil {
			problem(w, err)
			return
		}
		reply(w, 202, viewJob(j))
	}))
	a.mux.HandleFunc("GET /api/transfers", a.auth(func(w http.ResponseWriter, r *http.Request, l identity.Login) {
		list := a.files.Transfers(l.User.ID)
		out := make([]jobView, 0, len(list))
		for _, j := range list {
			out = append(out, viewJob(j))
		}
		reply(w, 200, out)
	}, false, false))
	a.mux.HandleFunc("GET /api/transfers/{id}", a.auth(func(w http.ResponseWriter, r *http.Request, l identity.Login) {
		j, err := a.files.Transfer(l.User.ID, r.PathValue("id"))
		if err != nil {
			problem(w, err)
			return
		}
		reply(w, 200, viewJob(j))
	}, false, false))
	a.mux.HandleFunc("DELETE /api/transfers/{id}", a.auth(func(w http.ResponseWriter, r *http.Request, l identity.Login) {
		if err := a.files.CancelTransfer(l.User.ID, r.PathValue("id")); err != nil {
			problem(w, err)
			return
		}
		w.WriteHeader(204)
	}, false, false))
	a.mux.HandleFunc("GET /api/text", a.auth(func(w http.ResponseWriter, r *http.Request, l identity.Login) {
		space, name, ok := queryPath(w, r, "")
		if !ok {
			return
		}
		txt, err := a.files.ReadText(r.Context(), l.User.ID, space, name)
		if err != nil {
			problem(w, err)
			return
		}
		reply(w, 200, txt)
	}, false, false))
	// The body (up to a few MiB) is read before taking the publication lock.
	a.mux.HandleFunc("PUT /api/text", a.limitTexts(prebuffer(maxTextJSON, a.publication(func(w http.ResponseWriter, r *http.Request, l identity.Login) {
		var in struct {
			Space   string `json:"space"`
			Path    string `json:"path"`
			Content string `json:"content"`
			Version string `json:"version"`
		}
		if !decodeLimit(w, r, &in, maxTextJSON) {
			return
		}
		version, err := a.files.WriteText(r.Context(), l.User.ID, in.Space, in.Path, in.Content, in.Version)
		if err != nil {
			problem(w, err)
			return
		}
		reply(w, 200, map[string]string{"version": version})
	}))))
	a.mux.HandleFunc("POST /api/rename", a.publication(func(w http.ResponseWriter, r *http.Request, l identity.Login) {
		var in struct {
			Space string `json:"space"`
			From  string `json:"from"`
			To    string `json:"to"`
		}
		if !decode(w, r, &in) {
			return
		}
		if err := a.files.Rename(r.Context(), l.User.ID, in.Space, in.From, in.To); err != nil {
			problem(w, err)
			return
		}
		w.WriteHeader(204)
	}))
	a.mux.HandleFunc("POST /api/trash", a.publication(func(w http.ResponseWriter, r *http.Request, l identity.Login) {
		var in struct {
			Space string `json:"space"`
			Path  string `json:"path"`
		}
		if !decode(w, r, &in) {
			return
		}
		item, err := a.files.Trash(r.Context(), l.User.ID, in.Space, in.Path)
		if err != nil {
			problem(w, err)
			return
		}
		reply(w, 201, item)
	}))
	a.mux.HandleFunc("GET /api/trash", a.auth(func(w http.ResponseWriter, r *http.Request, l identity.Login) {
		q, err := url.ParseQuery(r.URL.RawQuery)
		if err != nil || len(q) != 1 || len(q["space"]) != 1 {
			fail(w, 400, "invalid_query")
			return
		}
		items, err := a.files.TrashList(l.User.ID, q["space"][0])
		if err != nil {
			problem(w, err)
			return
		}
		reply(w, 200, items)
	}, false, false))
	a.mux.HandleFunc("POST /api/trash/{id}/restore", a.publication(func(w http.ResponseWriter, r *http.Request, l identity.Login) {
		var in struct {
			Path string `json:"path"`
		}
		if !decode(w, r, &in) {
			return
		}
		if err := a.files.Restore(r.Context(), l.User.ID, r.PathValue("id"), in.Path); err != nil {
			problem(w, err)
			return
		}
		w.WriteHeader(204)
	}))
	a.mux.HandleFunc("DELETE /api/trash/{id}", a.publication(func(w http.ResponseWriter, r *http.Request, l identity.Login) {
		// Permanent deletion is reserved to administrators; trash protects
		// against mistakes and compromised ordinary accounts.
		if !l.User.Admin {
			fail(w, 403, "admin_required")
			return
		}
		if err := a.files.Purge(r.Context(), l.User.ID, r.PathValue("id")); err != nil {
			problem(w, err)
			return
		}
		w.WriteHeader(204)
	}))
	a.mux.HandleFunc("POST /api/uploads", a.auth(func(w http.ResponseWriter, r *http.Request, l identity.Login) {
		var in struct {
			Space string `json:"space"`
			Path  string `json:"path"`
			Size  *int64 `json:"size"`
		}
		if !decode(w, r, &in) {
			return
		}
		if in.Size == nil {
			fail(w, 400, "size_required")
			return
		}
		u, err := a.files.Begin(r.Context(), l.User.ID, in.Space, in.Path, *in.Size)
		if err != nil {
			problem(w, err)
			return
		}
		reply(w, 201, u)
	}, false, false))
	a.mux.HandleFunc("GET /api/uploads/{id}", a.auth(func(w http.ResponseWriter, r *http.Request, l identity.Login) {
		u, err := a.files.Status(l.User.ID, r.PathValue("id"))
		if err != nil {
			problem(w, err)
			return
		}
		reply(w, 200, u)
	}, false, false))
	a.mux.HandleFunc("PATCH /api/uploads/{id}", a.auth(a.patch, false, false))
	a.mux.HandleFunc("POST /api/uploads/{id}/commit", a.publication(func(w http.ResponseWriter, r *http.Request, l identity.Login) {
		published, err := a.files.Commit(r.Context(), l.User.ID, r.PathValue("id"))
		if published {
			reply(w, 200, map[string]any{"published": true, "durability_confirmed": err == nil})
			return
		}
		if err != nil {
			problem(w, err)
			return
		}
		fail(w, 500, "publication_failed")
	}))
	a.mux.HandleFunc("DELETE /api/uploads/{id}", a.auth(func(w http.ResponseWriter, r *http.Request, l identity.Login) {
		if err := a.files.Abort(l.User.ID, r.PathValue("id")); err != nil {
			problem(w, err)
			return
		}
		w.WriteHeader(204)
	}, false, false))
	a.adminRoutes()
	a.linkRoutes()
}

// queryPath accepts exactly "space" and an optional "path", each once.
func queryPath(w http.ResponseWriter, r *http.Request, defaultPath string) (string, string, bool) {
	q, err := url.ParseQuery(r.URL.RawQuery)
	valid := err == nil && len(q["space"]) == 1 && len(q["path"]) <= 1 && len(q) == 1+len(q["path"])
	if !valid {
		fail(w, 400, "invalid_query")
		return "", "", false
	}
	name := defaultPath
	if v, ok := q["path"]; ok {
		name = v[0]
	}
	return q["space"][0], name, true
}
func (a *API) spaces(w http.ResponseWriter, r *http.Request, l identity.Login) {
	type view struct {
		core.SpaceInfo
		Permissions core.Permission `json:"permissions"`
	}
	granted := a.files.Effective(l.User.ID)
	out := make([]view, 0)
	for _, sp := range a.files.Spaces() {
		// Administrators see every space (to grant access); others only theirs.
		if p, ok := granted[sp.Name]; ok || l.User.Admin {
			out = append(out, view{sp, p})
		}
	}
	reply(w, 200, out)
}
func (a *API) list(w http.ResponseWriter, r *http.Request, l identity.Login) {
	space, name, ok := queryPath(w, r, ".")
	if !ok {
		return
	}
	entries, err := a.files.List(r.Context(), l.User.ID, space, name, 10000)
	if err != nil {
		problem(w, err)
		return
	}
	reply(w, 200, entries)
}
func (a *API) content(w http.ResponseWriter, r *http.Request, l identity.Login) {
	space, name, ok := queryPath(w, r, "")
	if !ok {
		return
	}
	f, err := a.files.Read(r.Context(), l.User.ID, space, name)
	if err != nil {
		problem(w, err)
		return
	}
	defer f.Close()
	a.download(w, r, path.Base(name), f)
}

// maxTextJSON bounds a text save request: JSON escaping can expand content.
const maxTextJSON = 6*core.MaxTextBytes + maxJSON

// prebuffer reads a bounded body into memory before calling next, so a slow
// client cannot hold locks taken by next while trickling its request.
func prebuffer(limit int64, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, limit))
		if err != nil {
			problem(w, err)
			return
		}
		_ = r.Body.Close()
		r.Body = io.NopCloser(bytes.NewReader(data))
		next(w, r)
	}
}

// limitTexts caps memory used by buffered text saves (4 × maxTextJSON).
func (a *API) limitTexts(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		select {
		case a.texts <- struct{}{}:
			defer func() { <-a.texts }()
			next(w, r)
		default:
			w.Header().Set("Retry-After", "2")
			fail(w, 429, "capacity_exceeded")
		}
	}
}

// previewTypes lists what the browser may render inline; everything else is
// only downloadable. Types are fixed by extension, never sniffed.
var previewTypes = map[string]string{
	".png": "image/png", ".jpg": "image/jpeg", ".jpeg": "image/jpeg", ".gif": "image/gif", ".webp": "image/webp",
	".avif": "image/avif", ".bmp": "image/bmp", ".ico": "image/x-icon", ".svg": "image/svg+xml",
	".mp4": "video/mp4", ".m4v": "video/mp4", ".webm": "video/webm", ".mov": "video/quicktime",
	".mp3": "audio/mpeg", ".m4a": "audio/mp4", ".aac": "audio/aac", ".ogg": "audio/ogg", ".oga": "audio/ogg",
	".opus": "audio/ogg", ".wav": "audio/wav", ".flac": "audio/flac",
	".pdf": "application/pdf",
}

// preview serves a file inline for <img>, <video>, <audio> or a PDF frame.
// Responses keep nosniff and a sandbox CSP, so even an SVG or a mislabelled
// file opened directly in a tab cannot run script in the application origin.
func (a *API) preview(w http.ResponseWriter, r *http.Request, l identity.Login) {
	space, name, ok := queryPath(w, r, "")
	if !ok {
		return
	}
	typ, known := previewTypes[strings.ToLower(path.Ext(name))]
	if !known {
		fail(w, 415, "preview_unsupported")
		return
	}
	f, err := a.files.Read(r.Context(), l.User.ID, space, name)
	if err != nil {
		problem(w, err)
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		problem(w, err)
		return
	}
	h := w.Header()
	h.Set("Content-Type", typ)
	h.Set("Content-Disposition", mime.FormatMediaType("inline", map[string]string{"filename": path.Base(name)}))
	h.Set("Cross-Origin-Resource-Policy", "same-origin")
	if typ == "application/pdf" {
		// Browsers refuse to render PDFs in sandboxed documents; a PDF is not
		// HTML and nosniff keeps it that way. Only our own page may frame it.
		h.Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'self'")
	}
	http.ServeContent(&streaming{ResponseWriter: w, rc: http.NewResponseController(w), idle: a.timeout}, r, path.Base(name), info.ModTime(), f)
}

func (a *API) patch(w http.ResponseWriter, r *http.Request, l identity.Login) {
	typ, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || typ != "application/octet-stream" {
		fail(w, 415, "octet_stream_required")
		return
	}
	offset, err := strconv.ParseInt(r.Header.Get("Upload-Offset"), 10, 64)
	if err != nil || offset < 0 {
		fail(w, 400, "invalid_offset")
		return
	}
	if r.ContentLength > a.config.Limits.MaxChunkBytes {
		fail(w, 413, "chunk_too_large")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, a.config.Limits.MaxChunkBytes)
	u, err := a.files.Patch(r.Context(), l.User.ID, r.PathValue("id"), offset, r.Body)
	if err != nil {
		problem(w, err)
		return
	}
	reply(w, 200, u)
}
func (a *API) reauth(w http.ResponseWriter, r *http.Request, l identity.Login, secret string) bool {
	if !a.rate(w, r) {
		return false
	}
	if err := a.store.VerifyPassword(r.Context(), l.User.ID, secret); err != nil {
		passwordProblem(w, err)
		return false
	}
	return true
}

// passwordProblem reports a wrong re-entered password as 403: the session is
// still valid, and 401 would make the client treat it as a logout.
func passwordProblem(w http.ResponseWriter, err error) {
	if errors.Is(err, identity.ErrAuth) {
		fail(w, 403, "wrong_password")
		return
	}
	problem(w, err)
}
func (a *API) adminRoutes() {
	a.mux.HandleFunc("GET /api/users", a.auth(func(w http.ResponseWriter, r *http.Request, l identity.Login) {
		users, err := a.store.Users()
		if err != nil {
			problem(w, err)
			return
		}
		reply(w, 200, users)
	}, false, true))
	a.mux.HandleFunc("POST /api/users", a.auth(func(w http.ResponseWriter, r *http.Request, l identity.Login) {
		var in struct {
			Username string                     `json:"username"`
			Password string                     `json:"password"`
			Admin    bool                       `json:"admin"`
			Spaces   map[string]core.Permission `json:"spaces"`
			Reauth   string                     `json:"reauth_password"`
		}
		if !decode(w, r, &in) || !a.reauth(w, r, l, in.Reauth) {
			return
		}
		u, err := a.store.Create(r.Context(), in.Username, in.Password, in.Admin, in.Spaces)
		if err != nil {
			problem(w, err)
			return
		}
		if err = a.syncUser(u); err != nil {
			problem(w, err)
			return
		}
		reply(w, 201, u)
	}, true, true))
	a.mux.HandleFunc("PUT /api/users/{id}", a.auth(func(w http.ResponseWriter, r *http.Request, l identity.Login) {
		var in struct {
			Admin    *bool                      `json:"admin"`
			Disabled *bool                      `json:"disabled"`
			Spaces   map[string]core.Permission `json:"spaces"`
			Reauth   string                     `json:"reauth_password"`
		}
		if !decode(w, r, &in) {
			return
		}
		if in.Admin == nil || in.Disabled == nil || in.Spaces == nil {
			fail(w, 400, "all_fields_required")
			return
		}
		if !a.reauth(w, r, l, in.Reauth) {
			return
		}
		u, err := a.store.Update(r.PathValue("id"), *in.Admin, *in.Disabled, in.Spaces)
		if err != nil {
			problem(w, err)
			return
		}
		if err = a.syncUser(u); err != nil {
			problem(w, err)
			return
		}
		reply(w, 200, u)
	}, true, true))
	// Deleting an account ends its sessions, clears its file permissions and so
	// removes its public links. Administrators cannot delete their own account
	// (that would end the session doing it); the last administrator stays.
	a.mux.HandleFunc("DELETE /api/users/{id}", a.auth(func(w http.ResponseWriter, r *http.Request, l identity.Login) {
		var in struct {
			Reauth string `json:"reauth_password"`
		}
		if !decode(w, r, &in) || !a.reauth(w, r, l, in.Reauth) {
			return
		}
		if r.PathValue("id") == l.User.ID {
			fail(w, 409, "delete_self")
			return
		}
		u, err := a.store.Delete(r.PathValue("id"))
		if err != nil {
			problem(w, err)
			return
		}
		if err = a.files.SetPermissions(u.ID, nil); err != nil {
			problem(w, err)
			return
		}
		w.WriteHeader(204)
	}, true, true))
	a.mux.HandleFunc("POST /api/users/{id}/password", a.auth(func(w http.ResponseWriter, r *http.Request, l identity.Login) {
		var in struct {
			Password string `json:"new_password"`
			Reauth   string `json:"reauth_password"`
		}
		if !decode(w, r, &in) || !a.reauth(w, r, l, in.Reauth) {
			return
		}
		if err := a.store.ResetPassword(r.Context(), r.PathValue("id"), in.Password); err != nil {
			problem(w, err)
			return
		}
		w.WriteHeader(204)
	}, true, true))
}

// completeSetup creates the first administrator. The code is compared in
// constant time, attempts share the login rate limit, and Bootstrap itself
// refuses once any account exists, so setup can succeed at most once.
func (a *API) completeSetup(w http.ResponseWriter, r *http.Request) {
	if !a.rate(w, r) {
		return
	}
	var in struct {
		Code     string `json:"setup_code"`
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if !decode(w, r, &in) {
		return
	}
	a.security.Lock()
	defer a.security.Unlock()
	code := a.SetupCode()
	if code == "" {
		fail(w, 409, "setup_complete")
		return
	}
	if subtle.ConstantTimeCompare([]byte(normalizeCode(in.Code)), []byte(normalizeCode(code))) != 1 {
		fail(w, 403, "invalid_setup_code")
		return
	}
	u, err := a.store.Bootstrap(r.Context(), in.Username, in.Password)
	if err != nil {
		problem(w, err)
		return
	}
	if err = a.syncUser(u); err != nil {
		problem(w, err)
		return
	}
	a.setupMu.Lock()
	a.setup = ""
	a.setupMu.Unlock()
	reply(w, 201, u)
}

func (a *API) publication(fn authenticated) http.HandlerFunc {
	handler := a.auth(fn, false, false)
	return func(w http.ResponseWriter, r *http.Request) {
		a.security.RLock()
		defer a.security.RUnlock()
		handler(w, r)
	}
}
