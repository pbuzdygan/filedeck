package identity

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pbuzdygan/filedeck/internal/core"
)

const secret = "a-long-test-password-123"

func setup(t *testing.T) (*Store, string, User) {
	t.Helper()
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	u, err := s.Bootstrap(context.Background(), "admin", secret)
	if err != nil {
		t.Fatal(err)
	}
	return s, dir, u
}
func login(t *testing.T, s *Store, name, p string) Login {
	t.Helper()
	l, e := s.Login(context.Background(), name, p)
	if e != nil {
		t.Fatal(e)
	}
	return l
}

func TestBootstrapPersistenceAndSecretStorage(t *testing.T) {
	s, dir, u := setup(t)
	ctx := context.Background()
	if _, e := s.Bootstrap(ctx, "other", secret); !errors.Is(e, ErrExists) {
		t.Fatal(e)
	}
	l := login(t, s, "admin", secret)
	if !ValidCSRF(l.Token, l.CSRF) || ValidCSRF(l.Token, "bad") {
		t.Fatal("invalid csrf derivation")
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "identity.db"))
	if err != nil {
		t.Fatal(err)
	}
	for _, sensitive := range []string{secret, l.Token, l.CSRF} {
		if bytes.Contains(data, []byte(sensitive)) {
			t.Fatal("plaintext credential stored")
		}
	}
	reopened, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	got, err := reopened.Authenticate(l.Token)
	if err != nil || got.User.ID != u.ID {
		t.Fatal(got, err)
	}
	if err = reopened.Logout(l.Token); err != nil {
		t.Fatal(err)
	}
	if _, err = reopened.Authenticate(l.Token); !errors.Is(err, ErrAuth) {
		t.Fatal(err)
	}
	reopened.Close()
	again, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer again.Close()
	if _, err = again.Authenticate(l.Token); !errors.Is(err, ErrAuth) {
		t.Fatal("revoked session resurrected", err)
	}
}
func TestPasswordChangeRevokesAllSessions(t *testing.T) {
	s, _, u := setup(t)
	a := login(t, s, "admin", secret)
	b := login(t, s, "admin", secret)
	ctx := context.Background()
	if e := s.ChangePassword(ctx, u.ID, "wrong", secret+"new"); !errors.Is(e, ErrAuth) {
		t.Fatal(e)
	}
	if _, e := s.Authenticate(a.Token); e != nil {
		t.Fatal("failed password change revoked session", e)
	}
	if e := s.ChangePassword(ctx, u.ID, secret, secret+"new"); e != nil {
		t.Fatal(e)
	}
	for _, l := range []Login{a, b} {
		if _, e := s.Authenticate(l.Token); !errors.Is(e, ErrAuth) {
			t.Fatal(e)
		}
	}
	if _, e := s.Login(ctx, "admin", secret); !errors.Is(e, ErrAuth) {
		t.Fatal(e)
	}
	login(t, s, "admin", secret+"new")
}
func TestAccountDisablePermissionsAndLastAdministrator(t *testing.T) {
	s, _, admin := setup(t)
	ctx := context.Background()
	if _, e := s.Update(admin.ID, false, false, nil); !errors.Is(e, ErrLastAdmin) {
		t.Fatal(e)
	}
	if _, e := s.Update(admin.ID, true, true, map[string]core.Permission{core.AnySpace: 7}); !errors.Is(e, ErrLastAdmin) {
		t.Fatal(e)
	}
	u, e := s.Create(ctx, "reader", secret, false, map[string]core.Permission{"files": core.Read})
	if e != nil {
		t.Fatal(e)
	}
	l := login(t, s, "reader", secret)
	if _, e = s.Update(u.ID, false, true, nil); e != nil {
		t.Fatal(e)
	}
	if _, e = s.Authenticate(l.Token); !errors.Is(e, ErrAuth) {
		t.Fatal(e)
	}
	if _, e = s.Login(ctx, "reader", secret); !errors.Is(e, ErrAuth) {
		t.Fatal(e)
	}
	if _, e = s.Update(u.ID, false, false, map[string]core.Permission{"files": core.List}); e != nil {
		t.Fatal(e)
	}
	l = login(t, s, "reader", secret)
	if l.User.Spaces["files"] != core.List || len(l.User.Spaces) != 1 {
		t.Fatal(l.User)
	}
	if e = s.ResetPassword(ctx, u.ID, secret+"reset"); e != nil {
		t.Fatal(e)
	}
	if _, e = s.Authenticate(l.Token); !errors.Is(e, ErrAuth) {
		t.Fatal(e)
	}
}
func TestIdleAndAbsoluteExpiry(t *testing.T) {
	s, _, _ := setup(t)
	l := login(t, s, "admin", secret)
	start := s.now()
	s.now = func() time.Time { return start.Add(IdleLifetime) }
	if _, e := s.Authenticate(l.Token); !errors.Is(e, ErrAuth) {
		t.Fatal("idle expiry", e)
	}
	s.now = func() time.Time { return start }
	l = login(t, s, "admin", secret)
	for elapsed := 10 * time.Minute; elapsed < SessionLifetime; elapsed += 10 * time.Minute {
		s.now = func() time.Time { return start.Add(elapsed) }
		if _, e := s.Authenticate(l.Token); e != nil {
			t.Fatal(e)
		}
	}
	s.now = func() time.Time { return start.Add(SessionLifetime) }
	if _, e := s.Authenticate(l.Token); !errors.Is(e, ErrAuth) {
		t.Fatal("absolute expiry", e)
	}
}
func TestLoginHasBoundedSessionsAndWork(t *testing.T) {
	s, _, _ := setup(t)
	first := login(t, s, "admin", secret)
	for i := 0; i < maxSessions; i++ {
		login(t, s, "admin", secret)
	}
	if _, e := s.Authenticate(first.Token); !errors.Is(e, ErrAuth) {
		t.Fatal("oldest session not evicted", e)
	}
	s.gate <- struct{}{}
	s.gate <- struct{}{}
	if _, e := s.Login(context.Background(), "admin", secret); !errors.Is(e, ErrBusy) {
		t.Fatal(e)
	}
	<-s.gate
	<-s.gate
	for _, name := range []string{"unknown", "admin"} {
		if _, e := s.Login(context.Background(), name, "wrong"); !errors.Is(e, ErrAuth) {
			t.Fatal(e)
		}
	}
	if _, e := s.Login(context.Background(), "admin", strings.Repeat("x", 1025)); !errors.Is(e, ErrAuth) {
		t.Fatal(e)
	}
}
func TestDatabaseSymlinkAndPublicPermissionsRejected(t *testing.T) {
	dir := t.TempDir()
	os.Chmod(dir, 0700)
	outside := filepath.Join(t.TempDir(), "keep")
	os.WriteFile(outside, []byte("untouched"), 0600)
	if e := os.Symlink(outside, filepath.Join(dir, "identity.db")); e != nil {
		t.Fatal(e)
	}
	if s, e := Open(dir); e == nil {
		s.Close()
		t.Fatal("symlink accepted")
	}
	b, _ := os.ReadFile(outside)
	if string(b) != "untouched" {
		t.Fatal(string(b))
	}
	os.Remove(filepath.Join(dir, "identity.db"))
	os.WriteFile(filepath.Join(dir, "identity.db"), nil, 0644)
	if s, e := Open(dir); e == nil {
		s.Close()
		t.Fatal("public DB accepted")
	}
}

func TestLegacyPermissionsBecomeAllSpacesGrant(t *testing.T) {
	var u User
	if e := json.Unmarshal([]byte(`{"id":"x","username":"old","admin":true,"permissions":7}`), &u); e != nil {
		t.Fatal(e)
	}
	u.normalize()
	if u.Spaces[core.AnySpace] != core.AllPermissions || len(u.Spaces) != 1 || u.Permissions != 0 {
		t.Fatal("legacy full administrator must keep every permission", u)
	}
	var reader User
	json.Unmarshal([]byte(`{"id":"r","username":"reader","permissions":7}`), &reader)
	reader.normalize()
	if reader.Spaces[core.AnySpace] != 7 {
		t.Fatal("ordinary account must not gain Modify", reader)
	}
	out, _ := json.Marshal(u)
	if strings.Contains(string(out), `"permissions"`) {
		t.Fatal("legacy field written back", string(out))
	}
	var fresh User
	json.Unmarshal([]byte(`{"id":"y","username":"new","spaces":{"files":3}}`), &fresh)
	fresh.normalize()
	if fresh.Spaces["files"] != 3 || len(fresh.Spaces) != 1 {
		t.Fatal(fresh)
	}
}

// GHSA-576v-w77m-gr84, GHSA-7rc3-g7h6-22m7: usernames are never normalized, so
// no two accounts can collide; anything but lowercase ASCII is rejected.
func TestUsernamesCannotCollideByCaseOrUnicode(t *testing.T) {
	s, _, _ := setup(t)
	ctx := context.Background()
	var err error
	for _, name := range []string{"Admin", "ADMIN", "admin​", "admın", "admın", "ａdmin", "ad", "admin/../x", " admin"} {
		if _, err = s.Create(ctx, name, secret, false, nil); !errors.Is(err, ErrInvalid) && !errors.Is(err, ErrExists) {
			t.Errorf("%q accepted: %v", name, err)
		}
	}
	if _, err = s.Create(ctx, "admin", secret, false, nil); !errors.Is(err, ErrExists) {
		t.Fatal(err)
	}
}
