package api

import (
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSearchOverHTTP(t *testing.T) {
	f := setup(t)
	admin := f.login(t, "admin")
	os.MkdirAll(filepath.Join(f.root, "docs", "2024"), 0700)
	os.WriteFile(filepath.Join(f.root, "docs", "2024", "Faktura-01.pdf"), []byte("x"), 0600)
	os.WriteFile(filepath.Join(f.root, "faktura.txt"), []byte("x"), 0600)
	lister := map[string]any{"username": "lister", "password": password, "admin": false, "spaces": map[string]int{"files": 1}, "reauth_password": password}
	status(t, f.request("POST", "/api/users", lister, admin, nil), 201)
	nobody := map[string]any{"username": "nobody", "password": password, "admin": false, "spaces": map[string]int{}, "reauth_password": password}
	status(t, f.request("POST", "/api/users", nobody, admin, nil), 201)

	w := f.request("GET", "/api/search?space=files&q=faktura", nil, f.login(t, "lister"), nil)
	status(t, w, 200)
	var res struct {
		Results []struct {
			Path string `json:"path"`
		} `json:"results"`
		Truncated bool `json:"truncated"`
	}
	json.Unmarshal(w.Body.Bytes(), &res)
	if len(res.Results) != 2 || res.Truncated {
		t.Fatal(w.Body.String())
	}
	w = f.request("GET", "/api/search?space=files&path=docs&q=FAKTURA", nil, admin, nil)
	json.Unmarshal(w.Body.Bytes(), &res)
	if len(res.Results) != 1 || res.Results[0].Path != "docs/2024/Faktura-01.pdf" {
		t.Fatal(w.Body.String())
	}
	status(t, f.request("GET", "/api/search?space=files&q=faktura", nil, f.login(t, "nobody"), nil), 403)
	for _, bad := range []string{"space=files", "space=files&q=", "space=files&q=a%2Fb", "space=files&q=" + url.QueryEscape(strings.Repeat("x", 101)), "space=files&q=x&extra=1", "space=files&q=x&path=../"} {
		if w = f.request("GET", "/api/search?"+bad, nil, admin, nil); w.Code != 400 {
			t.Error(bad, w.Code, w.Body.String())
		}
	}
}
