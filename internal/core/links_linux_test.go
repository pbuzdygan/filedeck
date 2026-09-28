package core

import (
	"bytes"
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/pbuzdygan/filedeck/internal/storage"
)

func readLink(t *testing.T, l *Shared, sub string) string {
	t.Helper()
	f, err := l.Read(sub)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	b, _ := io.ReadAll(f)
	return string(b)
}

func openLink(t *testing.T, s *Service, token string) *Shared {
	t.Helper()
	l, err := s.OpenLink(token)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	return l
}

// Covers the requirements derived from File Browser's share advisories
// (GHSA-r6pg, -m8v4, -833g, -pp88, -3q2p, -5ww9, -j9jx, -v9w4, -68j5, -mr74,
// -6cqf, -3v48): object binding, revocation, secret handling, ownership.
func TestPublicLinks(t *testing.T) {
	m := setupSpaces(t)
	ctx := context.Background()
	m.s.SetPermissions("alice", map[string]Permission{"files": AllPermissions, "nas": List})
	m.s.SetPermissions("bob", map[string]Permission{"files": AllPermissions})
	os.WriteFile(filepath.Join(m.own, "report.txt"), []byte("report"), 0600)

	// Input and permission checks; no links to missing or hidden paths.
	for _, bad := range []struct {
		space, path string
		life        time.Duration
		password    string
		want        error
	}{
		{"files", "report.txt", time.Minute, "", ErrLinkInput},
		{"files", "report.txt", MaxLinkLifetime + time.Hour, "", ErrLinkInput},
		{"files", "report.txt", time.Hour, "short", ErrLinkInput},
		{"files", "missing.txt", time.Hour, "", fs.ErrNotExist},
		{"files", ".filedeck/trash", time.Hour, "", storage.ErrPath},
		{"files", ".", time.Hour, "", storage.ErrPath},
		{"nas", "x", time.Hour, "", ErrDenied},
		{"other", "x", time.Hour, "", ErrDenied},
	} {
		if _, _, err := m.s.CreateLink(ctx, "alice", bad.space, bad.path, bad.life, bad.password); !errors.Is(err, bad.want) {
			t.Errorf("%+v: got %v", bad, err)
		}
	}

	token, link, err := m.s.CreateLink(ctx, "alice", "files", "report.txt", time.Hour, "")
	if err != nil || link.Directory || link.HasPassword || !link.Available {
		t.Fatal(link, err)
	}
	if got := readLink(t, openLink(t, m.s, token), "."); got != "report" {
		t.Fatal(got)
	}
	if _, err = m.s.OpenLink(token[:len(token)-1] + "A"); !errors.Is(err, ErrLinkNotFound) {
		t.Fatalf("altered token: %v", err)
	}

	// Password: checked with a constant-time digest, never stored in clear.
	secretToken, secret, err := m.s.CreateLink(ctx, "alice", "files", "report.txt", time.Hour, "correct horse")
	if err != nil || !secret.HasPassword {
		t.Fatal(err)
	}
	l := openLink(t, m.s, secretToken)
	if err = l.CheckPassword(ctx, "wrong password"); !errors.Is(err, ErrLinkPassword) {
		t.Fatal(err)
	}
	if err = l.CheckPassword(ctx, "correct horse"); err != nil {
		t.Fatal(err)
	}

	// Ownership: only the owner or an administrator sees and revokes a link.
	if list, _ := m.s.Links("bob", false); len(list) != 0 {
		t.Fatalf("bob sees alice's links: %+v", list)
	}
	if err = m.s.RevokeLink("bob", secret.ID, false); !errors.Is(err, ErrLinkNotFound) {
		t.Fatalf("bob revoked alice's link: %v", err)
	}
	if list, _ := m.s.Links("admin", true); len(list) != 2 {
		t.Fatalf("admin view: %+v", list)
	}
	if err = m.s.RevokeLink("admin", secret.ID, true); err != nil {
		t.Fatal(err)
	}
	if _, err = m.s.OpenLink(secretToken); !errors.Is(err, ErrLinkNotFound) {
		t.Fatalf("revoked link works: %v", err)
	}

	// The database holds neither tokens nor passwords.
	m.s.Close()
	db, _ := os.ReadFile(filepath.Join(m.state, "uploads.db"))
	for _, s := range []string{token, secretToken, "correct horse"} {
		if bytes.Contains(db, []byte(s)) {
			t.Errorf("database contains %q", s)
		}
	}
}

func TestPublicLinkStopsWorking(t *testing.T) {
	m := setupSpaces(t)
	ctx := context.Background()
	m.s.SetPermissions("alice", map[string]Permission{"files": AllPermissions})
	os.WriteFile(filepath.Join(m.own, "a.txt"), []byte("a"), 0600)
	os.MkdirAll(filepath.Join(m.own, "dir", "sub"), 0700)
	os.WriteFile(filepath.Join(m.own, "dir", "sub", "b.txt"), []byte("b"), 0600)
	os.WriteFile(filepath.Join(m.own, "outside.txt"), []byte("outside"), 0600)

	// Folder link: listing and reading only beneath the folder.
	dirToken, _, err := m.s.CreateLink(ctx, "alice", "files", "dir", time.Hour, "")
	if err != nil {
		t.Fatal(err)
	}
	l := openLink(t, m.s, dirToken)
	if list, err := l.List(ctx, "."); err != nil || len(list) != 1 || !list[0].Directory {
		t.Fatalf("%+v %v", list, err)
	}
	if got := readLink(t, l, "sub/b.txt"); got != "b" {
		t.Fatal(got)
	}
	if _, err = l.Read("../outside.txt"); err == nil {
		t.Fatal("read outside the shared folder")
	}

	// Renamed: the link no longer works and is shown as unavailable.
	fileToken, _, _ := m.s.CreateLink(ctx, "alice", "files", "a.txt", time.Hour, "")
	os.Rename(filepath.Join(m.own, "a.txt"), filepath.Join(m.own, "b.txt"))
	os.WriteFile(filepath.Join(m.own, "a.txt"), []byte("new file, same name"), 0600)
	if _, err = m.s.OpenLink(fileToken); !errors.Is(err, ErrLinkNotFound) {
		t.Fatalf("link follows the path: %v", err)
	}
	list, _ := m.s.Links("alice", false)
	available := map[bool]int{}
	for _, x := range list {
		available[x.Available]++
	}
	if available[true] != 1 || available[false] != 1 {
		t.Fatalf("availability %+v", list)
	}

	// Moved to trash through Filedeck: gone as well.
	os.WriteFile(filepath.Join(m.own, "c.txt"), []byte("c"), 0600)
	trashToken, _, _ := m.s.CreateLink(ctx, "alice", "files", "c.txt", time.Hour, "")
	if _, err = m.s.Trash(ctx, "alice", "files", "c.txt"); err != nil {
		t.Fatal(err)
	}
	if _, err = m.s.OpenLink(trashToken); !errors.Is(err, ErrLinkNotFound) {
		t.Fatalf("trashed file still shared: %v", err)
	}

	// Losing List on the space deletes folder links for good.
	m.s.SetPermissions("alice", map[string]Permission{"files": Read | Create | Modify})
	m.s.SetPermissions("alice", map[string]Permission{"files": AllPermissions})
	if _, err = m.s.OpenLink(dirToken); !errors.Is(err, ErrLinkNotFound) {
		t.Fatalf("revoked link revived: %v", err)
	}

	// Disabling the account (no grants) deletes the rest.
	os.WriteFile(filepath.Join(m.own, "d.txt"), []byte("d"), 0600)
	dToken, _, _ := m.s.CreateLink(ctx, "alice", "files", "d.txt", time.Hour, "")
	m.s.SetPermissions("alice", nil)
	m.s.SetPermissions("alice", map[string]Permission{"files": AllPermissions})
	if _, err = m.s.OpenLink(dToken); !errors.Is(err, ErrLinkNotFound) {
		t.Fatalf("link survived account disable: %v", err)
	}

	// Expiry: refused at once, removed by Expire.
	eToken, _, _ := m.s.CreateLink(ctx, "alice", "files", "d.txt", time.Hour, "")
	now := time.Now()
	m.s.now = func() time.Time { return now.Add(time.Hour + time.Second) }
	if _, err = m.s.OpenLink(eToken); !errors.Is(err, ErrLinkNotFound) {
		t.Fatalf("expired link works: %v", err)
	}
	if err = m.s.Expire(); err != nil {
		t.Fatal(err)
	}
	if list, _ := m.s.Links("alice", false); len(list) != 0 {
		t.Fatalf("expired links kept: %+v", list)
	}
}
