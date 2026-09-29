package api

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pbuzdygan/filedeck/internal/core"
	"github.com/pbuzdygan/filedeck/internal/identity"
	"github.com/pbuzdygan/filedeck/internal/storage"
)

const password = "test-password-long-enough"
const publicOrigin = "https://files.example.test"

type fixture struct {
	app           *API
	store         *identity.Store
	files         *core.Service
	root, state   string
	admin, reader identity.User
}

func setup(t *testing.T) *fixture {
	t.Helper()
	base := t.TempDir()
	root := filepath.Join(base, "files")
	state := filepath.Join(base, "state")
	for _, p := range []string{root, state} {
		if e := os.Mkdir(p, 0700); e != nil {
			t.Fatal(e)
		}
	}
	limits := core.Limits{MaxFileBytes: 64, MaxStagingBytes: 128, MaxChunkBytes: 8, MaxUploads: 8, MaxUploadsPerUser: 4, TTL: time.Hour, TrashRetention: time.Hour}
	files, e := core.Open(state, map[string]string{"files": root}, limits, storage.DefaultModes())
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { files.Close() })
	store, e := identity.Open(state)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { store.Close() })
	admin, e := store.Bootstrap(context.Background(), "admin", password)
	if e != nil {
		t.Fatal(e)
	}
	reader, e := store.Create(context.Background(), "reader", password, false, map[string]core.Permission{"files": core.List | core.Read})
	if e != nil {
		t.Fatal(e)
	}
	app, e := New(store, files, Config{Origin: publicOrigin, Limits: limits})
	if e != nil {
		t.Fatal(e)
	}
	return &fixture{app, store, files, root, state, admin, reader}
}

type client struct {
	cookie *http.Cookie
	csrf   string
}

func (f *fixture) request(method, target string, body any, c client, headers map[string]string) *httptest.ResponseRecorder {
	var data []byte
	typ := "application/json"
	switch v := body.(type) {
	case string:
		data = []byte(v)
		typ = "application/octet-stream"
	case nil:
	default:
		data, _ = json.Marshal(v)
	}
	r := httptest.NewRequest(method, publicOrigin+target, bytes.NewReader(data))
	r.RemoteAddr = "127.0.0.1:1234"
	r.TLS = &tls.ConnectionState{}
	r.Header.Set("Content-Type", typ)
	r.Header.Set("Origin", publicOrigin)
	if c.cookie != nil {
		r.AddCookie(c.cookie)
		r.Header.Set("X-CSRF-Token", c.csrf)
	}
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	f.app.ServeHTTP(w, r)
	return w
}
func status(t *testing.T, w *httptest.ResponseRecorder, want int) {
	t.Helper()
	if w.Code != want {
		t.Fatalf("status %d, want %d: %s", w.Code, want, w.Body.String())
	}
}
func (f *fixture) login(t *testing.T, name string) client {
	t.Helper()
	w := f.request("POST", "/api/auth/login", map[string]any{"username": name, "password": password}, client{}, nil)
	status(t, w, 200)
	var l identity.Login
	if e := json.Unmarshal(w.Body.Bytes(), &l); e != nil {
		t.Fatal(e)
	}
	cookies := w.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatal(cookies)
	}
	return client{cookies[0], l.CSRF}
}
func (f *fixture) begin(t *testing.T, c client, path string, size int64) core.Upload {
	t.Helper()
	w := f.request("POST", "/api/uploads", map[string]any{"space": "files", "path": path, "size": size}, c, nil)
	status(t, w, 201)
	var u core.Upload
	json.Unmarshal(w.Body.Bytes(), &u)
	return u
}

func TestLoginCookieLogoutAndReplayedSession(t *testing.T) {
	f := setup(t)
	c := f.login(t, "admin")
	if !c.cookie.Secure || !c.cookie.HttpOnly || c.cookie.SameSite != http.SameSiteStrictMode || c.cookie.Path != "/" || c.cookie.Domain != "" || c.cookie.Name != "__Host-filedeck" {
		t.Fatal(c.cookie)
	}
	status(t, f.request("GET", "/api/auth/me", nil, c, nil), 200)
	w := f.request("POST", "/api/auth/logout", nil, c, nil)
	status(t, w, 204)
	if w.Result().Cookies()[0].MaxAge != -1 {
		t.Fatal("cookie not cleared")
	}
	status(t, f.request("GET", "/api/auth/me", nil, c, nil), 401)
	status(t, f.request("GET", "/api/files?space=files", nil, client{}, map[string]string{"X-Auth": c.cookie.Value, "X-Remote-User": "admin"}), 401)
}
func TestCSRFOriginAndHostBoundaries(t *testing.T) {
	f := setup(t)
	c := f.login(t, "admin")
	for _, headers := range []map[string]string{{"Origin": "https://evil.example"}, {"Origin": ""}, {"X-CSRF-Token": ""}, {"X-CSRF-Token": "wrong"}, {"Sec-Fetch-Site": "cross-site"}} {
		status(t, f.request("POST", "/api/uploads", map[string]any{"space": "files", "path": "nope", "size": 0}, c, headers), 403)
	}
	entries, _ := os.ReadDir(f.root)
	entries = slices.DeleteFunc(entries, func(e os.DirEntry) bool { return e.Name() == storage.MetaDir })
	if len(entries) != 0 {
		t.Fatal(entries)
	}
	r := httptest.NewRequest("GET", "https://evil.example/api/files?space=files", nil)
	r.RemoteAddr = "127.0.0.1:42"
	r.TLS = &tls.ConnectionState{}
	r.AddCookie(c.cookie)
	w := httptest.NewRecorder()
	f.app.ServeHTTP(w, r)
	status(t, w, 421)
	if !strings.Contains(w.Body.String(), `"expected_origin":"`+publicOrigin+`"`) {
		t.Fatal(w.Body.String())
	}
	r = httptest.NewRequest("GET", publicOrigin+"/api/files?space=files", nil)
	r.RemoteAddr = "127.0.0.1:42"
	r.TLS = nil
	r.AddCookie(c.cookie)
	w = httptest.NewRecorder()
	f.app.ServeHTTP(w, r)
	status(t, w, 400)
}
func TestJSONLimitsAndUnknownFields(t *testing.T) {
	f := setup(t)
	c := f.login(t, "admin")
	status(t, f.request("POST", "/api/uploads", map[string]any{"space": "files", "path": "file", "size": 0, "subject": f.admin.ID}, c, nil), 400)
	status(t, f.request("POST", "/api/uploads", map[string]any{"space": "files", "path": "file"}, c, nil), 400)
	status(t, f.request("POST", "/api/uploads", strings.Repeat("x", maxJSON+1), c, map[string]string{"Content-Type": "application/json"}), 400)
	// Valid JSON until the limit is reached must yield 413, without creating data.
	status(t, f.request("POST", "/api/uploads", `{"path":"`+strings.Repeat("x", maxJSON)+`","size":0}`, c, map[string]string{"Content-Type": "application/json"}), 413)
	status(t, f.request("POST", "/api/uploads", `{"path":"file","size":0} {}`, c, map[string]string{"Content-Type": "application/json"}), 400)
}
func TestUploadDownloadRangeAndConflict(t *testing.T) {
	f := setup(t)
	admin := f.login(t, "admin")
	reader := f.login(t, "reader")
	u := f.begin(t, admin, "hello.txt", 5)
	status(t, f.request("GET", "/api/uploads/"+u.ID, nil, reader, nil), 404)
	status(t, f.request("POST", "/api/uploads", map[string]any{"space": "files", "path": "evil", "size": 0}, reader, nil), 403)
	status(t, f.request("PATCH", "/api/uploads/"+u.ID, "hello!", admin, map[string]string{"Upload-Offset": "0"}), 413)
	w := f.request("GET", "/api/uploads/"+u.ID, nil, admin, nil)
	status(t, w, 200)
	var partial core.Upload
	json.Unmarshal(w.Body.Bytes(), &partial)
	if partial.Offset != 0 {
		t.Fatal(partial)
	}
	status(t, f.request("PATCH", "/api/uploads/"+u.ID, "hello", admin, map[string]string{"Upload-Offset": "0"}), 200)
	status(t, f.request("POST", "/api/uploads/"+u.ID+"/commit", nil, admin, nil), 200)
	w = f.request("GET", "/api/content?space=files&path=hello.txt", nil, reader, map[string]string{"Range": "bytes=1-3"})
	status(t, w, 206)
	if w.Body.String() != "ell" || w.Header().Get("Content-Type") != "application/octet-stream" || !strings.HasPrefix(w.Header().Get("Content-Disposition"), "attachment") {
		t.Fatal(w.Body.String(), w.Header())
	}
	status(t, f.request("GET", "/api/files?space=files", nil, reader, nil), 200)
	u = f.begin(t, admin, "hello.txt", 0)
	status(t, f.request("POST", "/api/uploads/"+u.ID+"/commit", nil, admin, nil), 409)
	status(t, f.request("DELETE", "/api/uploads/"+u.ID, nil, admin, nil), 204)
	b, _ := os.ReadFile(filepath.Join(f.root, "hello.txt"))
	if string(b) != "hello" {
		t.Fatal(string(b))
	}
}
func TestPathDecodingAndCookieAmbiguity(t *testing.T) {
	f := setup(t)
	c := f.login(t, "admin")
	for _, q := range []string{"path=..%2Foutside", "path=%2Fetc%2Fpasswd", "path=a%5Cb", "path=a&path=b", "path=%zz", "other=x", "space=files&space=x"} {
		status(t, f.request("GET", "/api/content?space=files&"+q, nil, c, nil), 400)
	}
	os.WriteFile(filepath.Join(f.root, "%2e%2e"), []byte("literal"), 0600)
	w := f.request("GET", "/api/content?space=files&path=%252e%252e", nil, c, nil)
	status(t, w, 200)
	if w.Body.String() != "literal" {
		t.Fatal(w.Body.String())
	}
	r := httptest.NewRequest("GET", publicOrigin+"/api/auth/me", nil)
	r.RemoteAddr = "127.0.0.1:1"
	r.TLS = &tls.ConnectionState{}
	r.AddCookie(c.cookie)
	r.AddCookie(c.cookie)
	w = httptest.NewRecorder()
	f.app.ServeHTTP(w, r)
	status(t, w, 401)
}
func TestPasswordChangeAndAdminDisableRevokeSessions(t *testing.T) {
	f := setup(t)
	admin := f.login(t, "admin")
	reader := f.login(t, "reader")
	status(t, f.request("POST", "/api/auth/password", map[string]any{"current_password": "wrong", "new_password": password + "new"}, reader, nil), 403)
	status(t, f.request("GET", "/api/auth/me", nil, reader, nil), 200)
	status(t, f.request("POST", "/api/auth/password", map[string]any{"current_password": password, "new_password": password + "new"}, reader, nil), 204)
	status(t, f.request("GET", "/api/auth/me", nil, reader, nil), 401)
	status(t, f.request("PUT", "/api/users/"+f.reader.ID, map[string]any{"admin": false, "disabled": true, "spaces": map[string]int{}, "reauth_password": password}, admin, nil), 200)
	status(t, f.request("POST", "/api/auth/login", map[string]any{"username": "reader", "password": password + "new"}, client{}, nil), 401)
	status(t, f.request("PUT", "/api/users/"+f.admin.ID, map[string]any{"admin": false, "disabled": false, "spaces": map[string]int{}, "reauth_password": password}, admin, nil), 409)
}
func TestAdminRequiresRoleAndReauthentication(t *testing.T) {
	f := setup(t)
	admin := f.login(t, "admin")
	reader := f.login(t, "reader")
	in := map[string]any{"username": "newuser", "password": password, "spaces": map[string]int{}, "reauth_password": password}
	status(t, f.request("POST", "/api/users", in, reader, nil), 403)
	in["reauth_password"] = "wrong"
	status(t, f.request("POST", "/api/users", in, admin, nil), 403)
	in["reauth_password"] = password
	w := f.request("POST", "/api/users", in, admin, nil)
	status(t, w, 201)
	if strings.Contains(w.Body.String(), password) || strings.Contains(w.Body.String(), "digest") {
		t.Fatal("secret response")
	}
	c := f.login(t, "newuser")
	status(t, f.request("GET", "/api/files?space=files", nil, c, nil), 403)
	status(t, f.request("GET", "/api/users", nil, c, nil), 403)
}
func TestProxyBoundaryAndForwardedHeadersIgnored(t *testing.T) {
	f := setup(t)
	app, e := New(f.store, f.files, Config{Origin: publicOrigin, ProxyCIDR: "10.0.0.2/32", Limits: f.app.config.Limits})
	if e != nil {
		t.Fatal(e)
	}
	r := httptest.NewRequest("GET", publicOrigin+"/api/files?space=files", nil)
	r.RemoteAddr = "10.0.0.3:42"
	r.TLS = nil
	r.Header.Set("X-Forwarded-For", "10.0.0.2")
	r.Header.Set("X-Remote-User", "admin")
	w := httptest.NewRecorder()
	app.ServeHTTP(w, r)
	status(t, w, 403)
	r.RemoteAddr = "10.0.0.2:42"
	w = httptest.NewRecorder()
	app.ServeHTTP(w, r)
	status(t, w, 401)
}
func TestRateLimitAndBoundedBookkeeping(t *testing.T) {
	l := newLimiter()
	for i := 0; i < 10; i++ {
		if !l.allow("one") {
			t.Fatal(i)
		}
	}
	if l.allow("one") {
		t.Fatal("per-peer unlimited")
	}
	for i := 0; i < 10; i++ {
		if !l.allow("two") {
			t.Fatal(i)
		}
	}
	if l.allow("three") {
		t.Fatal("global unlimited")
	}
	for i := 0; i < 5000; i++ {
		l.allow(string(rune(i)) + "ip")
	}
	if len(l.peers) > 4096 {
		t.Fatal(len(l.peers))
	}
}

// An authenticated slow upload must not hold the revocation/publication lock.
type gatedBody struct {
	entered, release chan struct{}
	once             sync.Once
	r                io.Reader
}

func (b *gatedBody) Read(p []byte) (int, error) {
	b.once.Do(func() { close(b.entered); <-b.release })
	return b.r.Read(p)
}
func (b *gatedBody) Close() error { return nil }
func TestLogoutDuringUploadPreventsPublication(t *testing.T) {
	f := setup(t)
	c := f.login(t, "admin")
	u := f.begin(t, c, "new", 4)
	b := &gatedBody{entered: make(chan struct{}), release: make(chan struct{}), r: strings.NewReader("data")}
	r := httptest.NewRequest("PATCH", publicOrigin+"/api/uploads/"+u.ID, nil)
	r.RemoteAddr = "127.0.0.1:1"
	r.TLS = &tls.ConnectionState{}
	r.Body = b
	r.ContentLength = -1
	r.AddCookie(c.cookie)
	r.Header.Set("Origin", publicOrigin)
	r.Header.Set("X-CSRF-Token", c.csrf)
	r.Header.Set("Upload-Offset", "0")
	r.Header.Set("Content-Type", "application/octet-stream")
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { w := httptest.NewRecorder(); f.app.ServeHTTP(w, r); done <- w }()
	<-b.entered
	logout := make(chan *httptest.ResponseRecorder, 1)
	go func() { logout <- f.request("POST", "/api/auth/logout", nil, c, nil) }()
	select {
	case w := <-logout:
		status(t, w, 204)
	case <-time.After(2 * time.Second):
		close(b.release)
		<-done
		t.Fatal("logout blocked by network reader")
	}
	close(b.release)
	status(t, <-done, 200)
	status(t, f.request("POST", "/api/uploads/"+u.ID+"/commit", nil, c, nil), 401)
	if _, e := os.Stat(filepath.Join(f.root, "new")); !errors.Is(e, os.ErrNotExist) {
		t.Fatal(e)
	}
}

func TestUploadIDsRemainPrivateBetweenWriters(t *testing.T) {
	f := setup(t)
	u, e := f.store.Create(context.Background(), "writer", password, false, map[string]core.Permission{"files": core.Create})
	if e != nil {
		t.Fatal(e)
	}
	if e = f.app.syncUser(u); e != nil {
		t.Fatal(e)
	}
	a := f.login(t, "admin")
	b := f.login(t, "writer")
	upload := f.begin(t, a, "new", 0)
	for _, request := range []struct{ method, path string }{{"GET", "/api/uploads/" + upload.ID}, {"DELETE", "/api/uploads/" + upload.ID}, {"POST", "/api/uploads/" + upload.ID + "/commit"}} {
		status(t, f.request(request.method, request.path, nil, b, nil), 404)
	}
	status(t, f.request("POST", "/api/uploads/"+upload.ID+"/commit", nil, a, nil), 200)
}

func TestAdministrativeResetAndPermissionsInvalidateOldCookies(t *testing.T) {
	f := setup(t)
	a := f.login(t, "admin")
	b := f.login(t, "reader")
	status(t, f.request("POST", "/api/users/"+f.reader.ID+"/password", map[string]any{"new_password": password, "reauth_password": password}, a, nil), 204)
	status(t, f.request("GET", "/api/auth/me", nil, b, nil), 401)
	b = f.login(t, "reader")
	status(t, f.request("PUT", "/api/users/"+f.reader.ID, map[string]any{"admin": false, "disabled": false, "spaces": map[string]int{}, "reauth_password": password}, a, nil), 200)
	status(t, f.request("GET", "/api/files?space=files", nil, b, nil), 401)
	b = f.login(t, "reader")
	status(t, f.request("GET", "/api/files?space=files", nil, b, nil), 403)
}

func TestLoginRateCannotUseForwardedIPToBypassLimit(t *testing.T) {
	f := setup(t)
	for i := 0; i < 10; i++ {
		if !f.app.logins.allow("127.0.0.1") {
			t.Fatal(i)
		}
	}
	status(t, f.request("POST", "/api/auth/login", map[string]any{"username": "admin", "password": password}, client{}, map[string]string{"X-Forwarded-For": "203.0.113.99"}), 429)
}

func TestHTTPSTransportAndRestartedIdentityStore(t *testing.T) {
	f := setup(t)
	// Bind first so the exact origin is known before constructing the handler.
	ts := httptest.NewUnstartedServer(nil)
	origin := "https://" + ts.Listener.Addr().String()
	app, e := New(f.store, f.files, Config{Origin: origin, Limits: f.app.config.Limits})
	if e != nil {
		t.Fatal(e)
	}
	ts.Config.Handler = app
	ts.StartTLS()
	defer ts.Close()
	call := func(method, target, body string, cookie *http.Cookie, csrf string) *http.Response {
		t.Helper()
		r, e := http.NewRequest(method, origin+target, strings.NewReader(body))
		if e != nil {
			t.Fatal(e)
		}
		r.Header.Set("Origin", origin)
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("X-CSRF-Token", csrf)
		if cookie != nil {
			r.AddCookie(cookie)
		}
		res, e := ts.Client().Do(r)
		if e != nil {
			t.Fatal(e)
		}
		return res
	}
	res := call("POST", "/api/auth/login", `{"username":"admin","password":"`+password+`"}`, nil, "")
	if res.StatusCode != 200 {
		t.Fatal(res.StatusCode)
	}
	cookie := res.Cookies()[0]
	var login identity.Login
	json.NewDecoder(res.Body).Decode(&login)
	res.Body.Close()
	res = call("GET", "/api/auth/me", "", cookie, "")
	if res.StatusCode != 200 {
		t.Fatal(res.StatusCode)
	}
	res.Body.Close()
	res = call("POST", "/api/auth/logout", "", cookie, login.CSRF)
	if res.StatusCode != 204 {
		t.Fatal(res.StatusCode)
	}
	res.Body.Close()
	res = call("GET", "/api/auth/me", "", cookie, "")
	if res.StatusCode != 401 {
		t.Fatal(res.StatusCode)
	}
	res.Body.Close()
	ts.Close()
	f.store.Close()
	reopened, e := identity.Open(f.state)
	if e != nil {
		t.Fatal(e)
	}
	defer reopened.Close()
	if _, e = reopened.Authenticate(cookie.Value); !errors.Is(e, identity.ErrAuth) {
		t.Fatal("logout not durable", e)
	}
}

func TestUploadResumesAcrossRestartAndCommitIsIdempotent(t *testing.T) {
	f := setup(t)
	admin := f.login(t, "admin")
	u := f.begin(t, admin, "resume.txt", 10)
	status(t, f.request("PATCH", "/api/uploads/"+u.ID, "hello", admin, map[string]string{"Upload-Offset": "0"}), 200)
	// Restart the whole process state: files, identity store and handler.
	if e := errors.Join(f.files.Close(), f.store.Close()); e != nil {
		t.Fatal(e)
	}
	var e error
	if f.files, e = core.Open(f.state, map[string]string{"files": f.root}, f.app.config.Limits, storage.DefaultModes()); e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { f.files.Close() })
	if f.store, e = identity.Open(f.state); e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { f.store.Close() })
	if f.app, e = New(f.store, f.files, f.app.config); e != nil {
		t.Fatal(e)
	}
	reader := f.login(t, "reader")
	status(t, f.request("GET", "/api/uploads/"+u.ID, nil, reader, nil), 404)
	w := f.request("GET", "/api/uploads/"+u.ID, nil, admin, nil)
	status(t, w, 200)
	var got core.Upload
	json.Unmarshal(w.Body.Bytes(), &got)
	if got.Offset != 5 || got.State != core.StateUploading {
		t.Fatal(got)
	}
	status(t, f.request("PATCH", "/api/uploads/"+u.ID, "world", admin, map[string]string{"Upload-Offset": "5"}), 200)
	for i := 0; i < 2; i++ {
		w = f.request("POST", "/api/uploads/"+u.ID+"/commit", nil, admin, nil)
		status(t, w, 200)
		if !strings.Contains(w.Body.String(), `"published":true`) || !strings.Contains(w.Body.String(), `"durability_confirmed":true`) {
			t.Fatal(w.Body.String())
		}
	}
	w = f.request("GET", "/api/uploads/"+u.ID, nil, admin, nil)
	status(t, w, 200)
	json.Unmarshal(w.Body.Bytes(), &got)
	if got.State != core.StatePublished || !got.Durable {
		t.Fatal(got)
	}
	status(t, f.request("DELETE", "/api/uploads/"+u.ID, nil, admin, nil), 404)
	b, _ := os.ReadFile(filepath.Join(f.root, "resume.txt"))
	if string(b) != "helloworld" {
		t.Fatal(string(b))
	}
}

func TestInterfaceHeadersAndFolders(t *testing.T) {
	f := setup(t)
	w := f.request("GET", "/", nil, client{}, map[string]string{"Sec-Fetch-Site": "cross-site", "Origin": ""})
	status(t, w, 200)
	csp := w.Header().Get("Content-Security-Policy")
	if !strings.Contains(w.Header().Get("Content-Type"), "text/html") || !strings.Contains(csp, "script-src 'self'") || strings.Contains(csp, "unsafe") || !strings.Contains(csp, "require-trusted-types-for") {
		t.Fatal(w.Header())
	}
	status(t, f.request("GET", "/assets/app.js", nil, client{}, nil), 200)
	for _, bad := range []string{"/assets/index.html", "/assets/..%2fweb.go", "/assets/missing.js", "/index.html", "/static/app.js"} {
		status(t, f.request("GET", bad, nil, client{}, nil), 404)
	}
	admin := f.login(t, "admin")
	reader := f.login(t, "reader")
	w = f.request("GET", "/api/auth/me", nil, admin, nil)
	status(t, w, 200)
	if !strings.Contains(w.Body.String(), `"max_chunk_bytes":8`) || !strings.Contains(w.Body.String(), `"csrf"`) {
		t.Fatal(w.Body.String())
	}
	status(t, f.request("GET", "/api/files?space=files", nil, admin, map[string]string{"Sec-Fetch-Site": "cross-site"}), 403)
	status(t, f.request("POST", "/api/folders", map[string]any{"space": "files", "path": "docs"}, admin, nil), 201)
	status(t, f.request("POST", "/api/folders", map[string]any{"space": "files", "path": "docs"}, admin, nil), 409)
	status(t, f.request("POST", "/api/folders", map[string]any{"space": "files", "path": "../escape"}, admin, nil), 400)
	status(t, f.request("POST", "/api/folders", map[string]any{"space": "files", "path": "nope"}, reader, nil), 403)
	status(t, f.request("POST", "/api/folders", map[string]any{"space": "files", "path": "x"}, admin, map[string]string{"X-CSRF-Token": ""}), 403)
	u := f.begin(t, admin, "docs/a.txt", 1)
	status(t, f.request("PATCH", "/api/uploads/"+u.ID, "a", admin, map[string]string{"Upload-Offset": "0"}), 200)
	status(t, f.request("POST", "/api/uploads/"+u.ID+"/commit", nil, admin, nil), 200)
	if st, e := os.Stat(filepath.Join(f.root, "docs", "a.txt")); e != nil || st.Size() != 1 {
		t.Fatal(st, e)
	}
}

func TestFirstRunSetupCodeCreatesAdministratorOnce(t *testing.T) {
	base := t.TempDir()
	root, state := filepath.Join(base, "files"), filepath.Join(base, "state")
	for _, p := range []string{root, state} {
		if e := os.Mkdir(p, 0700); e != nil {
			t.Fatal(e)
		}
	}
	limits := core.Limits{MaxFileBytes: 64, MaxStagingBytes: 128, MaxChunkBytes: 8, MaxUploads: 8, MaxUploadsPerUser: 4, TTL: time.Hour, TrashRetention: time.Hour}
	files, e := core.Open(state, map[string]string{"files": root}, limits, storage.DefaultModes())
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { files.Close() })
	store, e := identity.Open(state)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { store.Close() })
	if _, e = New(store, files, Config{Origin: publicOrigin, Limits: limits}); e == nil {
		t.Fatal("uninitialized store accepted without setup mode")
	}
	app, e := New(store, files, Config{Origin: publicOrigin, Limits: limits, AllowSetup: true})
	if e != nil {
		t.Fatal(e)
	}
	f := &fixture{app: app, store: store, files: files, root: root, state: state}
	code := app.SetupCode()
	if len(code) != 19 {
		t.Fatal(code)
	}
	w := f.request("GET", "/api/setup", nil, client{}, nil)
	if !strings.Contains(w.Body.String(), `"required":true`) {
		t.Fatal(w.Body.String())
	}
	in := map[string]any{"setup_code": "AAAA-BBBB-CCCC-DDDD", "username": "admin", "password": password}
	status(t, f.request("POST", "/api/setup", in, client{}, nil), 403)
	in["setup_code"] = code
	status(t, f.request("POST", "/api/setup", in, client{}, map[string]string{"Origin": "https://evil.example"}), 403)
	in["password"] = "short"
	status(t, f.request("POST", "/api/setup", in, client{}, nil), 400)
	in["password"] = password
	in["setup_code"] = strings.ToLower(strings.ReplaceAll(code, "-", ""))
	status(t, f.request("POST", "/api/setup", in, client{}, nil), 201)
	if app.SetupCode() != "" {
		t.Fatal("setup code still valid")
	}
	in["username"] = "second"
	status(t, f.request("POST", "/api/setup", in, client{}, nil), 409)
	w = f.request("GET", "/api/setup", nil, client{}, nil)
	if !strings.Contains(w.Body.String(), `"required":false`) {
		t.Fatal(w.Body.String())
	}
	admin := f.login(t, "admin")
	status(t, f.request("POST", "/api/folders", map[string]any{"space": "files", "path": "ok"}, admin, nil), 201)
	users, _ := store.Users()
	if len(users) != 1 || !users[0].Admin {
		t.Fatal(users)
	}
}

func TestSpacesRenameAndTrashOverHTTP(t *testing.T) {
	f := setup(t)
	admin := f.login(t, "admin")
	reader := f.login(t, "reader")
	w := f.request("GET", "/api/spaces", nil, reader, nil)
	status(t, w, 200)
	if w.Body.String() != `[{"name":"files","read_only":false,"permissions":3}]`+"\n" {
		t.Fatal(w.Body.String())
	}
	status(t, f.request("GET", "/api/files?space=nas", nil, admin, nil), 403)
	status(t, f.request("GET", "/api/files", nil, admin, nil), 400)
	u := f.begin(t, admin, "a.txt", 1)
	status(t, f.request("PATCH", "/api/uploads/"+u.ID, "a", admin, map[string]string{"Upload-Offset": "0"}), 200)
	status(t, f.request("POST", "/api/uploads/"+u.ID+"/commit", nil, admin, nil), 200)

	rename := func(c client, from, to string) *httptest.ResponseRecorder {
		return f.request("POST", "/api/rename", map[string]any{"space": "files", "from": from, "to": to}, c, nil)
	}
	status(t, rename(reader, "a.txt", "b.txt"), 403)
	status(t, rename(admin, "a.txt", "../b.txt"), 400)
	status(t, rename(admin, "a.txt", ".filedeck/b"), 400)
	status(t, rename(admin, "a.txt", "b.txt"), 204)
	status(t, rename(admin, "a.txt", "c.txt"), 404)

	status(t, f.request("POST", "/api/trash", map[string]any{"space": "files", "path": "b.txt"}, reader, nil), 403)
	w = f.request("POST", "/api/trash", map[string]any{"space": "files", "path": "b.txt"}, admin, nil)
	status(t, w, 201)
	var item core.TrashItem
	json.Unmarshal(w.Body.Bytes(), &item)
	if item.Path != "b.txt" || item.ID == "" {
		t.Fatal(w.Body.String())
	}
	status(t, f.request("GET", "/api/trash?space=files", nil, reader, nil), 403)
	w = f.request("GET", "/api/trash?space=files", nil, admin, nil)
	status(t, w, 200)
	if !strings.Contains(w.Body.String(), item.ID) {
		t.Fatal(w.Body.String())
	}
	status(t, f.request("POST", "/api/trash/"+item.ID+"/restore", map[string]any{"path": ""}, admin, nil), 204)
	if _, e := os.Stat(filepath.Join(f.root, "b.txt")); e != nil {
		t.Fatal(e)
	}
	w = f.request("POST", "/api/trash", map[string]any{"space": "files", "path": "b.txt"}, admin, nil)
	json.Unmarshal(w.Body.Bytes(), &item)

	// An editor with Modify may trash and restore, but never purge.
	editorGrant := map[string]any{"username": "editor", "password": password, "admin": false, "spaces": map[string]int{"files": 15}, "reauth_password": password}
	status(t, f.request("POST", "/api/users", editorGrant, admin, nil), 201)
	editor := f.login(t, "editor")
	status(t, f.request("DELETE", "/api/trash/"+item.ID, nil, editor, nil), 403)
	status(t, f.request("DELETE", "/api/trash/"+item.ID, nil, admin, nil), 204)
	status(t, f.request("DELETE", "/api/trash/"+item.ID, nil, admin, nil), 404)
	bad := map[string]any{"username": "bad", "password": password, "admin": false, "spaces": map[string]int{"../x": 1}, "reauth_password": password}
	status(t, f.request("POST", "/api/users", bad, admin, nil), 400)
}

func TestHealthzIsLoopbackOnly(t *testing.T) {
	f := setup(t)
	r := httptest.NewRequest("GET", "http://anything/healthz", nil)
	r.RemoteAddr = "127.0.0.1:5"
	w := httptest.NewRecorder()
	f.app.ServeHTTP(w, r)
	if w.Code != 200 || w.Body.String() != "ok\n" {
		t.Fatal(w.Code, w.Body.String())
	}
	r = httptest.NewRequest("GET", publicOrigin+"/healthz", nil)
	r.RemoteAddr = "192.0.2.1:5"
	r.TLS = &tls.ConnectionState{}
	w = httptest.NewRecorder()
	f.app.ServeHTTP(w, r)
	if w.Code == 200 {
		t.Fatal("health endpoint exposed to the network")
	}
}

func TestPreviewAndTextEditorOverHTTP(t *testing.T) {
	f := setup(t)
	admin := f.login(t, "admin")
	reader := f.login(t, "reader")
	os.WriteFile(filepath.Join(f.root, "pic.svg"), []byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`), 0600)
	os.WriteFile(filepath.Join(f.root, "page.html"), []byte(`<script>alert(1)</script>`), 0600)
	os.WriteFile(filepath.Join(f.root, "clip.mp4"), []byte("0123456789"), 0600)
	os.WriteFile(filepath.Join(f.root, "doc.pdf"), []byte("%PDF-1.4"), 0600)

	w := f.request("GET", "/api/preview?space=files&path=pic.svg", nil, reader, nil)
	status(t, w, 200)
	h := w.Header()
	if h.Get("Content-Type") != "image/svg+xml" || !strings.Contains(h.Get("Content-Security-Policy"), "sandbox") || h.Get("X-Content-Type-Options") != "nosniff" || !strings.HasPrefix(h.Get("Content-Disposition"), "inline") {
		t.Fatal(h)
	}
	status(t, f.request("GET", "/api/preview?space=files&path=page.html", nil, reader, nil), 415)
	w = f.request("GET", "/api/preview?space=files&path=clip.mp4", nil, reader, map[string]string{"Range": "bytes=2-4"})
	status(t, w, 206)
	if w.Body.String() != "234" || w.Header().Get("Content-Type") != "video/mp4" {
		t.Fatal(w.Body.String(), w.Header())
	}
	w = f.request("GET", "/api/preview?space=files&path=doc.pdf", nil, reader, nil)
	status(t, w, 200)
	if csp := w.Header().Get("Content-Security-Policy"); csp != "default-src 'none'; frame-ancestors 'self'" {
		t.Fatal(csp)
	}
	status(t, f.request("GET", "/api/preview?space=files&path=pic.svg", nil, client{}, nil), 401)

	save := func(c client, path, content, version string) *httptest.ResponseRecorder {
		return f.request("PUT", "/api/text", map[string]any{"space": "files", "path": path, "content": content, "version": version}, c, nil)
	}
	status(t, save(reader, "notes.md", "# x", ""), 403)
	w = save(admin, "notes.md", "# Notatki\n", "")
	status(t, w, 200)
	w = f.request("GET", "/api/text?space=files&path=notes.md", nil, reader, nil)
	status(t, w, 200)
	var txt core.Text
	json.Unmarshal(w.Body.Bytes(), &txt)
	if txt.Content != "# Notatki\n" || txt.Version == "" {
		t.Fatal(w.Body.String())
	}
	status(t, save(reader, "notes.md", "hack", txt.Version), 403)
	status(t, save(admin, "notes.md", "# v2\n", txt.Version), 200)
	status(t, save(admin, "notes.md", "# stale\n", txt.Version), 409)
	status(t, f.request("GET", "/api/text?space=files&path=clip.mp4", nil, reader, nil), 200)
	os.WriteFile(filepath.Join(f.root, "blob.bin"), []byte{0, 1, 2}, 0600)
	status(t, f.request("GET", "/api/text?space=files&path=blob.bin", nil, reader, nil), 415)
	status(t, save(admin, "big.txt", strings.Repeat("x", core.MaxTextBytes+1), ""), 413)
	b, _ := os.ReadFile(filepath.Join(f.root, "notes.md"))
	if string(b) != "# v2\n" {
		t.Fatal(string(b))
	}
}

func TestTransfersOverHTTP(t *testing.T) {
	f := setup(t)
	admin := f.login(t, "admin")
	reader := f.login(t, "reader")
	os.WriteFile(filepath.Join(f.root, "a.txt"), []byte("a"), 0600)
	start := func(c client, kind, from, to string) *httptest.ResponseRecorder {
		return f.request("POST", "/api/transfers", map[string]any{"kind": kind, "from": map[string]string{"space": "files", "path": from}, "to": map[string]string{"space": "files", "path": to}}, c, nil)
	}
	status(t, start(reader, "copy", "a.txt", "b.txt"), 403)
	status(t, start(admin, "teleport", "a.txt", "b.txt"), 400)
	w := start(admin, "copy", "a.txt", "b.txt")
	status(t, w, 202)
	var j struct {
		ID, State, Error string
	}
	json.Unmarshal(w.Body.Bytes(), &j)
	for i := 0; i < 100 && j.State == "running"; i++ {
		time.Sleep(10 * time.Millisecond)
		json.Unmarshal(f.request("GET", "/api/transfers/"+j.ID, nil, admin, nil).Body.Bytes(), &j)
	}
	if j.State != "done" {
		t.Fatal(j)
	}
	status(t, f.request("GET", "/api/transfers/"+j.ID, nil, reader, nil), 404)
	w = start(admin, "copy", "a.txt", "b.txt")
	json.Unmarshal(w.Body.Bytes(), &j)
	for i := 0; i < 100 && j.State == "running"; i++ {
		time.Sleep(10 * time.Millisecond)
		json.Unmarshal(f.request("GET", "/api/transfers/"+j.ID, nil, admin, nil).Body.Bytes(), &j)
	}
	if j.State != "failed" || j.Error != "conflict" {
		t.Fatal(j)
	}
	status(t, start(admin, "move", "b.txt", "c.txt"), 202)
	if _, e := os.Stat(filepath.Join(f.root, "c.txt")); e != nil {
		t.Fatal(e)
	}
	w = f.request("GET", "/api/transfers", nil, admin, nil)
	if !strings.Contains(w.Body.String(), `"kind":"move"`) {
		t.Fatal(w.Body.String())
	}
}

// Regression tests for File Browser advisories (see docs/SECURITY.md).
func TestAdvisoryRegressions(t *testing.T) {
	f := setup(t)
	admin := f.login(t, "admin")
	os.WriteFile(filepath.Join(f.root, "secret.txt"), []byte("secret"), 0600)
	os.WriteFile(filepath.Join(f.root, "pic.png"), []byte("png"), 0600)
	lister := map[string]any{"username": "lister", "password": password, "admin": false, "spaces": map[string]int{"files": 1}, "reauth_password": password}
	status(t, f.request("POST", "/api/users", lister, admin, nil), 201)
	l := f.login(t, "lister")
	// GHSA-7whw-q6gh-xr59, GHSA-67cg-cpj7-qgc9: every content path requires Read.
	status(t, f.request("GET", "/api/files?space=files", nil, l, nil), 200)
	for _, u := range []string{"/api/content?space=files&path=secret.txt", "/api/text?space=files&path=secret.txt", "/api/preview?space=files&path=pic.png"} {
		w := f.request("GET", u, nil, l, nil)
		status(t, w, 403)
		if strings.Contains(w.Body.String(), "secret") {
			t.Fatal("content leaked", u)
		}
	}
	// GHSA-4mh3-h929-w968: leading slashes never reach a handler with data.
	for _, u := range []string{"//api/files?space=files", "/api//files?space=files", "/api/content?space=files&path=//secret.txt", "/api/content?space=files&path=/secret.txt"} {
		w := f.request("GET", u, nil, admin, nil)
		if w.Code == 200 || strings.Contains(w.Body.String(), "secret") {
			t.Fatal(u, w.Code, w.Body.String())
		}
	}
	// GHSA-9f3r-2vgw-m8xp, GHSA-fgm5-pw99-w2p7: traversal and backslashes in destinations.
	for _, to := range []string{"../x", "a\\..\\x", "/etc/x", ".FileDeck/x", ".filedeck./x"} {
		status(t, f.request("POST", "/api/rename", map[string]any{"space": "files", "from": "secret.txt", "to": to}, admin, nil), 400)
		status(t, f.request("POST", "/api/transfers", map[string]any{"kind": "copy", "from": map[string]string{"space": "files", "path": "secret.txt"}, "to": map[string]string{"space": "files", "path": to}}, admin, nil), 400)
	}
	// Bounded memory for buffered text saves.
	for i := 0; i < cap(f.app.texts); i++ {
		f.app.texts <- struct{}{}
	}
	status(t, f.request("PUT", "/api/text", map[string]any{"space": "files", "path": "n.txt", "content": "x", "version": ""}, admin, nil), 429)
	for i := 0; i < cap(f.app.texts); i++ {
		<-f.app.texts
	}
	status(t, f.request("PUT", "/api/text", map[string]any{"space": "files", "path": "n.txt", "content": "x", "version": ""}, admin, nil), 200)
}
