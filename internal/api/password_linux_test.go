package api

import (
	"strings"
	"testing"
)

// A wrong re-entered password must not look like an expired session (401):
// the client would show the login screen although the session is valid.
func TestWrongPasswordKeepsSession(t *testing.T) {
	f := setup(t)
	admin := f.login(t, "admin")
	create := map[string]any{"username": "nowy", "password": password, "admin": false, "spaces": map[string]int{"files": 3}, "reauth_password": "not-the-admin-password"}
	w := f.request("POST", "/api/users", create, admin, nil)
	status(t, w, 403)
	if !strings.Contains(w.Body.String(), "wrong_password") {
		t.Fatal(w.Body.String())
	}
	status(t, f.request("GET", "/api/auth/me", nil, admin, nil), 200)
	w = f.request("POST", "/api/auth/password", map[string]any{"current_password": "wrong-current-password", "new_password": "another-long-password"}, admin, nil)
	status(t, w, 403)
	status(t, f.request("GET", "/api/auth/me", nil, admin, nil), 200)
	create["reauth_password"] = password
	status(t, f.request("POST", "/api/users", create, admin, nil), 201)
	f.login(t, "nowy")
}
