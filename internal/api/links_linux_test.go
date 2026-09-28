package api

import (
	"crypto/tls"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pbuzdygan/filedeck/internal/core"
)

func (f *fixture) createLink(t *testing.T, c client, path, password string) (string, core.Link) {
	t.Helper()
	w := f.request("POST", "/api/links", map[string]any{"space": "files", "path": path, "expires_in_hours": 24, "password": password}, c, nil)
	status(t, w, 201)
	var out struct {
		Link core.Link `json:"link"`
		URL  string    `json:"url"`
	}
	json.Unmarshal(w.Body.Bytes(), &out)
	token, ok := strings.CutPrefix(out.URL, publicOrigin+"/s/")
	if !ok || len(token) != 43 {
		t.Fatal(out.URL)
	}
	return token, out.Link
}

func TestPublicLinksOverHTTP(t *testing.T) {
	f := setup(t)
	reader := f.login(t, "reader")
	admin := f.login(t, "admin")
	anonymous := client{}
	os.WriteFile(filepath.Join(f.root, "report.txt"), []byte("report"), 0600)
	os.MkdirAll(filepath.Join(f.root, "album", "2024"), 0700)
	os.WriteFile(filepath.Join(f.root, "album", "2024", "a.jpg"), []byte("jpeg"), 0600)

	// Creating links: input checks, CSRF, no links to missing paths.
	for _, body := range []map[string]any{
		{"space": "files", "path": "report.txt", "expires_in_hours": 0},
		{"space": "files", "path": "report.txt", "expires_in_hours": 24*365 + 1},
		{"space": "files", "path": "report.txt", "expires_in_hours": 1, "password": "short"},
		{"space": "files", "path": "../x", "expires_in_hours": 1},
	} {
		status(t, f.request("POST", "/api/links", body, reader, nil), 400)
	}
	status(t, f.request("POST", "/api/links", map[string]any{"space": "files", "path": "nope.txt", "expires_in_hours": 1}, reader, nil), 404)
	status(t, f.request("POST", "/api/links", map[string]any{"space": "files", "path": "report.txt", "expires_in_hours": 1}, reader, map[string]string{"X-CSRF-Token": "x"}), 403)

	// A file link works without an account.
	fileToken, fileLink := f.createLink(t, reader, "report.txt", "")
	w := f.request("GET", "/s/"+fileToken, nil, anonymous, nil)
	status(t, w, 200)
	if !strings.Contains(w.Header().Get("Content-Security-Policy"), "require-trusted-types-for") || w.Header().Get("X-Robots-Tag") == "" || !strings.Contains(w.Body.String(), "share.js") {
		t.Fatal(w.Header(), w.Body.String())
	}
	w = f.request("GET", "/api/public/"+fileToken, nil, anonymous, nil)
	status(t, w, 200)
	if !strings.Contains(w.Body.String(), `"name":"report.txt"`) {
		t.Fatal(w.Body.String())
	}
	w = f.request("GET", "/api/public/"+fileToken+"/content", nil, anonymous, nil)
	status(t, w, 200)
	if w.Body.String() != "report" || !strings.HasPrefix(w.Header().Get("Content-Disposition"), "attachment") {
		t.Fatal(w.Header(), w.Body.String())
	}
	status(t, f.request("GET", "/api/public/"+fileToken+"/content?path=x", nil, anonymous, nil), 400)
	status(t, f.request("GET", "/api/public/"+fileToken+"/files", nil, anonymous, nil), 400)

	// A folder link lists and downloads only beneath the folder.
	dirToken, _ := f.createLink(t, reader, "album", "")
	w = f.request("GET", "/api/public/"+dirToken+"/files?path=2024", nil, anonymous, nil)
	status(t, w, 200)
	if !strings.Contains(w.Body.String(), `"name":"a.jpg"`) {
		t.Fatal(w.Body.String())
	}
	w = f.request("GET", "/api/public/"+dirToken+"/content?path=2024/a.jpg", nil, anonymous, nil)
	status(t, w, 200)
	for _, bad := range []string{"../report.txt", "%2e%2e/report.txt", "/etc/passwd", "2024/../../report.txt", ".filedeck/trash"} {
		if w = f.request("GET", "/api/public/"+dirToken+"/content?path="+bad, nil, anonymous, nil); w.Code == 200 {
			t.Errorf("%s: %d %s", bad, w.Code, w.Body.String())
		}
	}

	// Password links reveal nothing until unlocked; the password never returns.
	lockedToken, locked := f.createLink(t, reader, "report.txt", "correct horse")
	w = f.request("GET", "/api/public/"+lockedToken, nil, anonymous, nil)
	if w.Body.String() != "{\"needs_password\":true}\n" {
		t.Fatal(w.Body.String())
	}
	status(t, f.request("GET", "/api/public/"+lockedToken+"/content", nil, anonymous, nil), 403)
	status(t, f.request("POST", "/api/public/"+lockedToken+"/unlock", map[string]string{"password": "wrong password"}, anonymous, nil), 403)
	w = f.request("POST", "/api/public/"+lockedToken+"/unlock", map[string]string{"password": "correct horse"}, anonymous, nil)
	status(t, w, 204)
	cookies := w.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != "__Host-filedeck-link-"+locked.ID || !cookies[0].Secure || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteStrictMode {
		t.Fatal(cookies)
	}
	unlocked := map[string]string{"Cookie": cookies[0].Name + "=" + cookies[0].Value}
	w = f.request("GET", "/api/public/"+lockedToken+"/content", nil, anonymous, unlocked)
	status(t, w, 200)
	forged := map[string]string{"Cookie": cookies[0].Name + "=" + cookies[0].Value[:len(cookies[0].Value)-2] + "AA"}
	status(t, f.request("GET", "/api/public/"+lockedToken+"/content", nil, anonymous, forged), 403)
	// An unlock cookie is bound to its link.
	otherToken, other := f.createLink(t, reader, "report.txt", "another secret")
	moved := map[string]string{"Cookie": "__Host-filedeck-link-" + other.ID + "=" + cookies[0].Value}
	status(t, f.request("GET", "/api/public/"+otherToken+"/content", nil, anonymous, moved), 403)

	// Listing: own links only; the administrator can see all; no secrets.
	w = f.request("GET", "/api/links", nil, reader, nil)
	status(t, w, 200)
	for _, secret := range []string{fileToken, lockedToken, "correct horse", "digest", "salt"} {
		if strings.Contains(w.Body.String(), secret) {
			t.Fatalf("listing leaks %q: %s", secret, w.Body.String())
		}
	}
	status(t, f.request("GET", "/api/links?all=1", nil, reader, nil), 403)
	status(t, f.request("GET", "/api/links", nil, admin, nil), 200)
	if w = f.request("GET", "/api/links", nil, admin, nil); strings.Contains(w.Body.String(), fileLink.ID) {
		t.Fatal("administrator's own list includes other users' links")
	}
	w = f.request("GET", "/api/links?all=1", nil, admin, nil)
	if !strings.Contains(w.Body.String(), fileLink.ID) || !strings.Contains(w.Body.String(), `"owner_name":"reader"`) {
		t.Fatal(w.Body.String())
	}

	// Revocation: by the owner or an administrator, effective at once.
	lister := map[string]any{"username": "lister", "password": password, "admin": false, "spaces": map[string]int{"files": 3}, "reauth_password": password}
	status(t, f.request("POST", "/api/users", lister, admin, nil), 201)
	status(t, f.request("DELETE", "/api/links/"+fileLink.ID, nil, f.login(t, "lister"), nil), 404)
	status(t, f.request("DELETE", "/api/links/"+fileLink.ID, nil, reader, nil), 204)
	w = f.request("GET", "/api/public/"+fileToken, nil, anonymous, nil)
	status(t, w, 404)
	if !strings.Contains(w.Body.String(), "link_unavailable") {
		t.Fatal(w.Body.String())
	}
	status(t, f.request("DELETE", "/api/links/"+other.ID, nil, admin, nil), 204)

	// Taking Read away from the owner removes the links.
	status(t, f.request("PUT", "/api/users/"+f.reader.ID, map[string]any{"admin": false, "disabled": false, "spaces": map[string]int{"files": 1}, "reauth_password": password}, admin, nil), 200)
	status(t, f.request("GET", "/api/public/"+dirToken, nil, anonymous, nil), 404)
	status(t, f.request("PUT", "/api/users/"+f.reader.ID, map[string]any{"admin": false, "disabled": false, "spaces": map[string]int{"files": 3}, "reauth_password": password}, admin, nil), 200)
	status(t, f.request("GET", "/api/public/"+dirToken, nil, anonymous, nil), 404)

	// The public API keeps the usual boundaries.
	status(t, f.request("GET", "/api/public/"+lockedToken, nil, anonymous, map[string]string{"Sec-Fetch-Site": "cross-site"}), 403)
	status(t, f.request("GET", "/s/"+lockedToken, nil, anonymous, map[string]string{"Sec-Fetch-Site": "cross-site"}), 200)
	status(t, f.request("POST", "/api/public/"+lockedToken+"/unlock", map[string]string{"password": "x"}, anonymous, map[string]string{"Origin": "https://evil.example"}), 403)
	status(t, f.request("GET", "/api/public/not-a-token", nil, anonymous, nil), 404)
}

func TestLinkPasswordAttemptsAreLimited(t *testing.T) {
	f := setup(t)
	os.WriteFile(filepath.Join(f.root, "a.txt"), []byte("a"), 0600)
	token, _ := f.createLink(t, f.login(t, "reader"), "a.txt", "correct horse")
	codes := map[int]int{}
	for range 12 {
		codes[f.request("POST", "/api/public/"+token+"/unlock", map[string]string{"password": "wrong password"}, client{}, nil).Code]++
	}
	if codes[403] != 10 || codes[429] != 2 {
		t.Fatal(codes)
	}
}

// A download longer than the request timeout completes while data flows.
func TestSlowDownloadIsNotCutOff(t *testing.T) {
	f := setup(t)
	data := strings.Repeat("0123456789abcdef", 256<<10) // 4 MiB
	os.WriteFile(filepath.Join(f.root, "big.bin"), []byte(data), 0600)
	token, _ := f.createLink(t, f.login(t, "reader"), "big.bin", "")
	f.app.timeout = 500 * time.Millisecond
	srv := httptest.NewUnstartedServer(f.app)
	srv.StartTLS()
	defer srv.Close()
	req, _ := http.NewRequest("GET", srv.URL+"/api/public/"+token+"/content", nil)
	req.Host = strings.TrimPrefix(publicOrigin, "https://")
	c := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}}
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	start := time.Now()
	got := 0
	buf := make([]byte, 256<<10)
	for {
		n, err := io.ReadFull(resp.Body, buf)
		got += n
		if err != nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if got != len(data) || time.Since(start) < time.Second {
		t.Fatalf("received %d of %d bytes in %v", got, len(data), time.Since(start))
	}
}

func TestDeleteUserOverHTTP(t *testing.T) {
	f := setup(t)
	admin := f.login(t, "admin")
	reader := f.login(t, "reader")
	os.WriteFile(filepath.Join(f.root, "a.txt"), []byte("a"), 0600)
	token, _ := f.createLink(t, reader, "a.txt", "")
	del := func(id, reauth string, c client) *httptest.ResponseRecorder {
		return f.request("DELETE", "/api/users/"+id, map[string]string{"reauth_password": reauth}, c, nil)
	}
	status(t, del(f.reader.ID, password, reader), 403) // administrators only
	status(t, del(f.reader.ID, "wrong password!!", admin), 403)
	w := del(f.admin.ID, password, admin)
	status(t, w, 409)
	if !strings.Contains(w.Body.String(), "delete_self") {
		t.Fatal(w.Body.String())
	}
	status(t, del(f.reader.ID, password, admin), 204)
	status(t, f.request("GET", "/api/auth/me", nil, reader, nil), 401)
	status(t, f.request("GET", "/api/public/"+token, nil, client{}, nil), 404)
	status(t, del(f.reader.ID, password, admin), 404)
	w = f.request("PUT", "/api/users/"+f.admin.ID, map[string]any{"admin": false, "disabled": false, "spaces": map[string]int{}, "reauth_password": password}, admin, nil)
	if w.Code != 409 || !strings.Contains(w.Body.String(), "last_admin") {
		t.Fatal(w.Code, w.Body.String())
	}
}
