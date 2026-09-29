package api

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base32"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync"
	"testing"
	"time"
)

// totpNow computes the current RFC 6238 code independently of the server.
func totpNow(key string) string {
	secret, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(key)
	if err != nil {
		panic(err)
	}
	var msg [8]byte
	binary.BigEndian.PutUint64(msg[:], uint64(time.Now().Unix()/30))
	m := hmac.New(sha1.New, secret)
	m.Write(msg[:])
	h := m.Sum(nil)
	o := h[len(h)-1] & 0x0f
	return fmt.Sprintf("%06d", (binary.BigEndian.Uint32(h[o:o+4])&0x7fffffff)%1000000)
}

// syncBuffer collects log output from concurrent handlers.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}
func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func TestTwoFactorSetupLoginAndReset(t *testing.T) {
	f := setup(t)
	logs := &syncBuffer{}
	f.app.log = slog.New(slog.NewTextHandler(logs, nil))
	admin := f.login(t, "admin")
	other := f.login(t, "admin")

	w := f.request("GET", "/api/auth/totp", nil, admin, nil)
	status(t, w, 200)
	if !strings.Contains(w.Body.String(), `"enabled":false`) {
		t.Fatal(w.Body.String())
	}
	status(t, f.request("POST", "/api/auth/totp/enable", map[string]any{"code": "123456"}, admin, nil), 409)
	status(t, f.request("POST", "/api/auth/totp/setup", map[string]any{"password": "wrong-password-123"}, admin, nil), 403)
	w = f.request("POST", "/api/auth/totp/setup", map[string]any{"password": password}, admin, nil)
	status(t, w, 200)
	var s struct {
		Key string `json:"key"`
		URI string `json:"uri"`
		QR  qrView `json:"qr"`
	}
	json.Unmarshal(w.Body.Bytes(), &s)
	if len(s.Key) != 32 || !strings.HasPrefix(s.URI, "otpauth://totp/Filedeck:admin@files.example.test?") || s.QR.Size < 21 || len(s.QR.Rows) != s.QR.Size || len(s.QR.Rows[0]) != s.QR.Size {
		t.Fatal(s.Key, s.URI, s.QR.Size)
	}
	// Setting up does not switch anything on yet.
	status(t, f.request("POST", "/api/auth/login", map[string]any{"username": "admin", "password": password}, client{}, nil), 200)
	code := totpNow(s.Key)
	wrong := string('0'+(code[0]-'0'+1)%10) + code[1:]
	status(t, f.request("POST", "/api/auth/totp/enable", map[string]any{"code": wrong}, admin, nil), 403)
	w = f.request("POST", "/api/auth/totp/enable", map[string]any{"code": code}, admin, nil)
	status(t, w, 200)
	var enabled struct {
		Codes []string `json:"recovery_codes"`
	}
	json.Unmarshal(w.Body.Bytes(), &enabled)
	if len(enabled.Codes) != 10 {
		t.Fatal(w.Body.String())
	}
	// This session stays, the other one ended.
	status(t, f.request("GET", "/api/auth/me", nil, admin, nil), 200)
	status(t, f.request("GET", "/api/auth/me", nil, other, nil), 401)

	f.app.logins = newLimiter() // this test signs in more often than the limit allows
	w = f.request("POST", "/api/auth/login", map[string]any{"username": "admin", "password": password}, client{}, nil)
	status(t, w, 401)
	if !strings.Contains(w.Body.String(), "totp_required") || len(w.Result().Cookies()) != 0 {
		t.Fatal(w.Body.String())
	}
	w = f.request("POST", "/api/auth/login", map[string]any{"username": "admin", "password": password, "code": "AAAAA-AAAAA"}, client{}, nil)
	status(t, w, 403)
	w = f.request("POST", "/api/auth/login", map[string]any{"username": "admin", "password": password, "code": enabled.Codes[0]}, client{}, nil)
	status(t, w, 200)
	if !strings.Contains(w.Body.String(), `"recovery_used":true`) || !strings.Contains(w.Body.String(), `"two_factor":true`) {
		t.Fatal(w.Body.String())
	}
	w = f.request("GET", "/api/auth/totp", nil, admin, nil)
	if !strings.Contains(w.Body.String(), `"enabled":true,"recovery_codes_left":9`) {
		t.Fatal(w.Body.String())
	}
	w = f.request("POST", "/api/auth/totp/recovery", map[string]any{"password": password}, admin, nil)
	status(t, w, 200)
	if !strings.Contains(w.Body.String(), "recovery_codes") {
		t.Fatal(w.Body.String())
	}

	f.app.logins = newLimiter()
	// Only administrators reset another account's two-factor authentication.
	reader := f.login(t, "reader")
	status(t, f.request("DELETE", "/api/users/"+f.admin.ID+"/totp", map[string]any{"reauth_password": password}, reader, nil), 403)
	status(t, f.request("POST", "/api/auth/totp/disable", map[string]any{"password": "wrong-password-123"}, admin, nil), 403)
	status(t, f.request("POST", "/api/auth/totp/disable", map[string]any{"password": password}, admin, nil), 204)
	status(t, f.request("GET", "/api/auth/me", nil, admin, nil), 200)
	f.login(t, "admin")
	status(t, f.request("DELETE", "/api/users/"+f.reader.ID+"/totp", map[string]any{"reauth_password": password}, admin, nil), 204)
	status(t, f.request("GET", "/api/auth/me", nil, reader, nil), 200) // nothing was on: nothing changes

	out := logs.String()
	for _, want := range []string{"msg=totp_enabled", "msg=login_failed client=127.0.0.1 user=admin reason=code", "recovery_code=true", "msg=totp_disabled", "msg=reauth_failed"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in log:\n%s", want, out)
		}
	}
	for _, secret := range append([]string{password, s.Key, code}, enabled.Codes...) {
		if strings.Contains(out, secret) {
			t.Fatalf("log contains a secret %q:\n%s", secret, out)
		}
	}
}

func TestClientAddressBehindProxyForLimitsAndLogs(t *testing.T) {
	f := setup(t)
	logs := &syncBuffer{}
	app, e := New(f.store, f.files, Config{Origin: publicOrigin, ProxyCIDR: "10.0.0.0/24", Limits: f.app.config.Limits, Log: slog.New(slog.NewTextHandler(logs, nil))})
	if e != nil {
		t.Fatal(e)
	}
	login := func(peer, xff string) int {
		r := httptest.NewRequest("POST", publicOrigin+"/api/auth/login", strings.NewReader(`{"username":"admin","password":"wrong-password-123"}`))
		r.RemoteAddr = peer + ":1234"
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Origin", publicOrigin)
		if xff != "" {
			r.Header.Set("X-Forwarded-For", xff)
		}
		w := httptest.NewRecorder()
		app.ServeHTTP(w, r)
		if w.Header().Get("Strict-Transport-Security") != "max-age=31536000" {
			t.Fatal("HSTS missing")
		}
		return w.Code
	}
	// The proxy appends the real client to whatever the client sent.
	for i := 0; i < 10; i++ {
		if c := login("10.0.0.2", "198.51.100.7, 203.0.113.5"); c != 401 {
			t.Fatal(i, c)
		}
	}
	if c := login("10.0.0.2", "203.0.113.5"); c != 429 {
		t.Fatal(c)
	}
	// A spoofed left-hand entry does not escape the limit...
	if c := login("10.0.0.2", "203.0.113.9, 203.0.113.5"); c != 429 {
		t.Fatal(c)
	}
	// ...and another visitor is not blocked by the first one.
	if c := login("10.0.0.2", "203.0.113.6"); c != 401 {
		t.Fatal(c)
	}
	// Chained proxies inside the CIDR are skipped; IPv6 is limited per /64.
	if c := login("10.0.0.2", "2001:db8:1:2::1, 10.0.0.3"); c != 401 {
		t.Fatal(c)
	}
	if got := limitKey(app.clientAddr(mustAddr("10.0.0.2"), headerRequest("2001:db8:1:2::99"))); got != "2001:db8:1:2::/64" {
		t.Fatal(got)
	}
	// Without a proxy (or from outside it) the header is ignored.
	if got := app.clientAddr(mustAddr("192.0.2.1"), headerRequest("203.0.113.5")); got.String() != "192.0.2.1" {
		t.Fatal(got)
	}
	if c := login("192.0.2.1", "10.0.0.2"); c != 403 {
		t.Fatal(c)
	}
	out := logs.String()
	for _, want := range []string{"msg=login_failed client=203.0.113.5 user=admin reason=password", "msg=rate_limited client=203.0.113.5", "msg=login_failed client=2001:db8:1:2::1", "msg=untrusted_proxy client=192.0.2.1", "set FILEDECK_PROXY_CIDR=192.0.2.1/32"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in log:\n%s", want, out)
		}
	}
	if strings.Contains(out, "wrong-password-123") || strings.Contains(out, "198.51.100.7") {
		t.Fatal(out)
	}
}

func TestHSTSOnlyForHTTPS(t *testing.T) {
	f := setup(t)
	w := f.request("GET", "/api/setup", nil, client{}, nil)
	if w.Header().Get("Strict-Transport-Security") == "" {
		t.Fatal("HSTS missing over HTTPS")
	}
	app, e := New(f.store, f.files, Config{Origin: "http://localhost:8080", InsecureLocal: true, Limits: f.app.config.Limits})
	if e != nil {
		t.Fatal(e)
	}
	r := httptest.NewRequest("GET", "http://localhost:8080/api/setup", nil)
	r.RemoteAddr = "127.0.0.1:1"
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, r)
	if rec.Code != 200 || rec.Header().Get("Strict-Transport-Security") != "" {
		t.Fatal(rec.Code, rec.Header())
	}
}

func mustAddr(s string) netip.Addr { return netip.MustParseAddr(s) }
func headerRequest(xff string) *http.Request {
	r := httptest.NewRequest("GET", publicOrigin+"/", nil)
	r.Header.Set("X-Forwarded-For", xff)
	return r
}
