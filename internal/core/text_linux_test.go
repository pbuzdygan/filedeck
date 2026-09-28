package core

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pbuzdygan/filedeck/internal/storage"
)

func TestTextEditorSavesSafely(t *testing.T) {
	m := setupSpaces(t)
	ctx := context.Background()
	m.s.SetPermissions("editor", map[string]Permission{AnySpace: AllPermissions})
	m.s.SetPermissions("creator", map[string]Permission{"files": List | Read | Create})
	m.s.SetPermissions("reader", map[string]Permission{"files": List | Read})

	_, err := m.s.WriteText(ctx, "editor", "files", "config.yaml", "a: 1\n", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.s.WriteText(ctx, "editor", "files", "config.yaml", "x", ""); !errors.Is(err, storage.ErrConflict) {
		t.Fatal("create replaced an existing file", err)
	}
	os.Chmod(filepath.Join(m.own, "config.yaml"), 0604)
	v1, _ := m.s.ReadTextVersion(t, "editor", "config.yaml")
	txt, err := m.s.ReadText(ctx, "reader", "files", "config.yaml")
	if err != nil || txt.Content != "a: 1\n" || txt.Version != v1 {
		t.Fatal(txt, err)
	}
	if _, err = m.s.WriteText(ctx, "reader", "files", "config.yaml", "a: 2\n", v1); !errors.Is(err, ErrDenied) {
		t.Fatal("saved without Modify", err)
	}
	if _, err = m.s.WriteText(ctx, "creator", "files", "config.yaml", "a: 2\n", v1); !errors.Is(err, ErrDenied) {
		t.Fatal("saved without Modify", err)
	}
	if _, err = m.s.WriteText(ctx, "creator", "files", "new.md", "# hi\n", ""); err != nil {
		t.Fatal("create with Create", err)
	}
	v2, err := m.s.WriteText(ctx, "editor", "files", "config.yaml", "a: 2\n", v1)
	if err != nil || v2 == v1 {
		t.Fatal(v2, err)
	}
	content(t, filepath.Join(m.own, "config.yaml"), "a: 2\n")
	if st, _ := os.Stat(filepath.Join(m.own, "config.yaml")); st.Mode().Perm() != 0604 {
		t.Fatal("mode not preserved", st.Mode())
	}
	// A stale version (e.g. another editor or an SMB client saved meanwhile) is refused.
	if _, err = m.s.WriteText(ctx, "editor", "files", "config.yaml", "a: 3\n", v1); !errors.Is(err, storage.ErrChanged) {
		t.Fatal("lost update", err)
	}
	os.WriteFile(filepath.Join(m.own, "config.yaml"), []byte("external\n"), 0604)
	if _, err = m.s.WriteText(ctx, "editor", "files", "config.yaml", "a: 3\n", v2); !errors.Is(err, storage.ErrChanged) {
		t.Fatal("external change overwritten", err)
	}
	content(t, filepath.Join(m.own, "config.yaml"), "external\n")
	// The previous version is in the trash and can be restored under a new name.
	items, _ := m.s.TrashList("editor", "files")
	if len(items) != 1 || !items[0].Replaced || items[0].Path != "config.yaml" {
		t.Fatal(items)
	}
	if err = m.s.Restore(ctx, "editor", items[0].ID, "config.old.yaml"); err != nil {
		t.Fatal(err)
	}
	content(t, filepath.Join(m.own, "config.old.yaml"), "a: 1\n")
	if n, _ := os.ReadDir(filepath.Join(m.own, storage.MetaDir, "staging")); len(n) != 0 {
		t.Fatal("staging leaked", n)
	}
}

// ReadTextVersion is a test helper returning the current version.
func (s *Service) ReadTextVersion(t *testing.T, subject, name string) (string, error) {
	t.Helper()
	txt, err := s.ReadText(context.Background(), subject, "files", name)
	return txt.Version, err
}

func TestTextEditorRejectsUnsuitableFiles(t *testing.T) {
	m := setupSpaces(t)
	ctx := context.Background()
	m.s.SetPermissions("editor", map[string]Permission{AnySpace: AllPermissions})
	os.WriteFile(filepath.Join(m.own, "bin.dat"), []byte{'a', 0, 'b'}, 0600)
	os.WriteFile(filepath.Join(m.own, "latin1.txt"), []byte{0xff, 0xfe}, 0600)
	os.WriteFile(filepath.Join(m.own, "big.txt"), []byte(strings.Repeat("x", MaxTextBytes+1)), 0600)
	os.Mkdir(filepath.Join(m.own, "dir"), 0700)
	os.WriteFile(filepath.Join(t.TempDir(), "x"), nil, 0600)
	os.Symlink("/etc/hostname", filepath.Join(m.own, "link.txt"))
	for name, want := range map[string]error{"bin.dat": ErrNotText, "latin1.txt": ErrNotText, "big.txt": ErrTooLarge, "dir": storage.ErrType} {
		if _, err := m.s.ReadText(ctx, "editor", "files", name); !errors.Is(err, want) {
			t.Error(name, err)
		}
	}
	if _, err := m.s.ReadText(ctx, "editor", "files", "link.txt"); err == nil {
		t.Error("followed symlink")
	}
	if _, err := m.s.WriteText(ctx, "editor", "files", "link.txt", "x", "1-1-1"); err == nil {
		t.Error("replaced a symlink")
	}
	if _, err := m.s.WriteText(ctx, "editor", "files", "dir", "x", "1-1-1"); err == nil {
		t.Error("replaced a directory")
	}
	if _, err := m.s.WriteText(ctx, "editor", "files", "n.txt", "a\x00b", ""); !errors.Is(err, ErrNotText) {
		t.Error(err)
	}
	if _, err := m.s.WriteText(ctx, "editor", "files", "n.txt", strings.Repeat("x", MaxTextBytes+1), ""); !errors.Is(err, ErrTooLarge) {
		t.Error(err)
	}
	if _, err := m.s.WriteText(ctx, "editor", "files", ".filedeck/x.txt", "x", ""); !errors.Is(err, storage.ErrPath) {
		t.Error(err)
	}
}
