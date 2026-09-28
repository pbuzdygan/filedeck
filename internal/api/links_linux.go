package api

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/pbuzdygan/filedeck/internal/core"
	"github.com/pbuzdygan/filedeck/internal/identity"
	"github.com/pbuzdygan/filedeck/internal/storage"
	"github.com/pbuzdygan/filedeck/internal/web"
)

// unlockLifetime bounds how long a browser stays unlocked for a password link.
const unlockLifetime = 12 * time.Hour

// maxPublicDownloads bounds concurrent downloads through public links, so
// visitors cannot take every request slot from signed-in users.
const maxPublicDownloads = 8

func (a *API) linkRoutes() {
	a.mux.HandleFunc("POST /api/links", a.auth(func(w http.ResponseWriter, r *http.Request, l identity.Login) {
		var in struct {
			Space    string `json:"space"`
			Path     string `json:"path"`
			Hours    int    `json:"expires_in_hours"`
			Password string `json:"password"`
		}
		if !decode(w, r, &in) {
			return
		}
		if in.Hours < 1 || in.Hours > int(core.MaxLinkLifetime/time.Hour) {
			fail(w, 400, "invalid_input")
			return
		}
		token, link, err := a.files.CreateLink(r.Context(), l.User.ID, in.Space, in.Path, time.Duration(in.Hours)*time.Hour, in.Password)
		if err != nil {
			problem(w, err)
			return
		}
		// The token is shown only now; the server keeps just its hash.
		reply(w, 201, map[string]any{"link": a.linkView(link, map[string]string{l.User.ID: l.User.Username}), "url": a.config.Origin + "/s/" + token})
	}, false, false))
	a.mux.HandleFunc("GET /api/links", a.auth(func(w http.ResponseWriter, r *http.Request, l identity.Login) {
		q, err := url.ParseQuery(r.URL.RawQuery)
		all := q.Get("all") == "1"
		if err != nil || len(q) > 1 || (len(q) == 1 && !all) || len(q["all"]) > 1 {
			fail(w, 400, "invalid_query")
			return
		}
		if all && !l.User.Admin {
			fail(w, 403, "admin_required")
			return
		}
		links, err := a.files.Links(l.User.ID, all)
		if err != nil {
			problem(w, err)
			return
		}
		names := map[string]string{l.User.ID: l.User.Username}
		if all {
			users, err := a.store.Users()
			if err != nil {
				problem(w, err)
				return
			}
			for _, u := range users {
				names[u.ID] = u.Username
			}
		}
		out := make([]linkView, 0, len(links))
		for _, link := range links {
			out = append(out, a.linkView(link, names))
		}
		reply(w, 200, out)
	}, false, false))
	a.mux.HandleFunc("DELETE /api/links/{id}", a.auth(func(w http.ResponseWriter, r *http.Request, l identity.Login) {
		if err := a.files.RevokeLink(l.User.ID, r.PathValue("id"), l.User.Admin); err != nil {
			problem(w, err)
			return
		}
		w.WriteHeader(204)
	}, false, false))

	a.mux.HandleFunc("GET /s/{token}", web.Share)
	a.mux.HandleFunc("GET /favicon.ico", web.Favicon)
	a.mux.HandleFunc("GET /api/public/{token}", a.public(func(w http.ResponseWriter, r *http.Request, s *core.Shared, unlocked bool) {
		if !unlocked {
			reply(w, 200, map[string]bool{"needs_password": true})
			return
		}
		reply(w, 200, map[string]any{"needs_password": false, "name": s.Entry.Name, "directory": s.Directory, "size": s.Entry.Size, "modified": s.Entry.Modified, "expires": s.Expires})
	}))
	a.mux.HandleFunc("POST /api/public/{token}/unlock", a.public(func(w http.ResponseWriter, r *http.Request, s *core.Shared, _ bool) {
		ip, _, _ := net.SplitHostPort(r.RemoteAddr)
		if !a.unlocks.allow(ip) {
			w.Header().Set("Retry-After", "60")
			fail(w, 429, "login_rate_limit")
			return
		}
		var in struct {
			Password string `json:"password"`
		}
		if !decode(w, r, &in) {
			return
		}
		if err := s.CheckPassword(r.Context(), in.Password); err != nil {
			problem(w, err)
			return
		}
		expires := time.Now().Add(unlockLifetime)
		if s.Expires.Before(expires) {
			expires = s.Expires
		}
		http.SetCookie(w, &http.Cookie{Name: a.unlockCookie(s.ID), Value: a.unlockValue(s.ID, expires), Path: "/", Secure: !a.config.InsecureLocal, HttpOnly: true, SameSite: http.SameSiteStrictMode, Expires: expires, MaxAge: int(time.Until(expires).Seconds())})
		w.WriteHeader(204)
	}))
	a.mux.HandleFunc("GET /api/public/{token}/files", a.public(func(w http.ResponseWriter, r *http.Request, s *core.Shared, unlocked bool) {
		sub, ok := publicPath(w, r, unlocked)
		if !ok {
			return
		}
		entries, err := s.List(r.Context(), sub)
		if err != nil {
			problem(w, err)
			return
		}
		reply(w, 200, entries)
	}))
	a.mux.HandleFunc("GET /api/public/{token}/content", a.public(func(w http.ResponseWriter, r *http.Request, s *core.Shared, unlocked bool) {
		sub, ok := publicPath(w, r, unlocked)
		if !ok {
			return
		}
		select {
		case a.downloads <- struct{}{}:
			defer func() { <-a.downloads }()
		default:
			w.Header().Set("Retry-After", "5")
			fail(w, 429, "capacity_exceeded")
			return
		}
		f, err := s.Read(sub)
		if err != nil {
			problem(w, err)
			return
		}
		defer f.Close()
		name := s.Entry.Name
		if sub != "." {
			name = path.Base(sub)
		}
		a.download(w, r, name, f)
	}))
}

type linkView struct {
	core.Link
	OwnerName string `json:"owner_name"`
}

func (a *API) linkView(l core.Link, names map[string]string) linkView {
	return linkView{Link: l, OwnerName: names[l.Owner]}
}

type publicHandler func(http.ResponseWriter, *http.Request, *core.Shared, bool)

// public validates the link on every request and reports whether this browser
// has unlocked it (always true for links without a password).
func (a *API) public(fn publicHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Public pages must not be indexed or leak the link through Referer.
		w.Header().Set("X-Robots-Tag", "noindex, nofollow")
		if err := r.Context().Err(); err != nil {
			problem(w, err)
			return
		}
		s, err := a.files.OpenLink(r.PathValue("token"))
		if err != nil {
			problem(w, err)
			return
		}
		defer s.Close()
		fn(w, r, s, !s.HasPassword || a.unlocked(r, s))
	}
}

// publicPath accepts at most one "path" (default "."), only once unlocked.
func publicPath(w http.ResponseWriter, r *http.Request, unlocked bool) (string, bool) {
	if !unlocked {
		fail(w, 403, "link_locked")
		return "", false
	}
	q, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil || len(q) > 1 || len(q["path"]) > 1 || (len(q) == 1 && len(q["path"]) != 1) {
		fail(w, 400, "invalid_query")
		return "", false
	}
	sub := "."
	if p := q["path"]; len(p) == 1 {
		sub = p[0]
	}
	if !storage.ValidPath(sub, true) {
		fail(w, 400, "invalid_input")
		return "", false
	}
	return sub, true
}

func (a *API) unlockCookie(id string) string {
	if a.config.InsecureLocal {
		return "filedeck-link-" + id
	}
	return "__Host-filedeck-link-" + id
}

// unlockValue is "<expiry>.<HMAC>" with a key that exists only in memory: a
// restart asks for the password again; nothing needs storing or revoking.
func (a *API) unlockValue(id string, expires time.Time) string {
	exp := strconv.FormatInt(expires.Unix(), 10)
	m := hmac.New(sha256.New, a.unlockKey)
	m.Write([]byte("filedeck-link-v1|" + id + "|" + exp))
	return exp + "." + base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}

func (a *API) unlocked(r *http.Request, s *core.Shared) bool {
	cookies := r.CookiesNamed(a.unlockCookie(s.ID))
	if len(cookies) != 1 {
		return false
	}
	exp, _, ok := strings.Cut(cookies[0].Value, ".")
	unix, err := strconv.ParseInt(exp, 10, 64)
	if !ok || err != nil || !time.Now().Before(time.Unix(unix, 0)) {
		return false
	}
	return hmac.Equal([]byte(cookies[0].Value), []byte(a.unlockValue(s.ID, time.Unix(unix, 0))))
}

// download streams a file as an attachment. The write deadline moves forward
// with every write, so a large file on a slow connection is not cut off by the
// request timeout, while a stalled client is still dropped.
func (a *API) download(w http.ResponseWriter, r *http.Request, name string, f *os.File) {
	if strings.Contains(r.Header.Get("Range"), ",") {
		fail(w, 416, "single_range_only")
		return
	}
	info, err := f.Stat()
	if err != nil {
		problem(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": name}))
	http.ServeContent(&streaming{ResponseWriter: w, rc: http.NewResponseController(w), idle: a.timeout}, r, name, info.ModTime(), f)
}

type streaming struct {
	http.ResponseWriter
	rc   *http.ResponseController
	idle time.Duration
}

func (s *streaming) Write(p []byte) (int, error) {
	if err := s.rc.SetWriteDeadline(time.Now().Add(s.idle)); err != nil && !errors.Is(err, http.ErrNotSupported) {
		return 0, err
	}
	return s.ResponseWriter.Write(p)
}
func (s *streaming) Unwrap() http.ResponseWriter { return s.ResponseWriter }
