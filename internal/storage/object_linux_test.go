package storage

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
)

// contents returns a reader of (file, error) results that fails the test on error.
func contents(t *testing.T) func(*os.File, error) string {
	return func(f *os.File, err error) string {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		b, err := io.ReadAll(f)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
}

// Shared links must follow an object, never a path: a rename, replacement or
// deletion makes the old identity unreachable (GHSA share classes).
func TestObjectIdentityFollowsTheObjectNotThePath(t *testing.T) {
	s, root, _ := fixture(t)
	write(t, filepath.Join(root, "a.txt"), "one")
	id, e, err := s.Identify("a.txt")
	if err != nil || id.Directory || e.Name != "a.txt" || e.Size != 3 {
		t.Fatalf("identify: %+v %+v %v", id, e, err)
	}
	o, err := s.OpenObject("a.txt", id)
	if err != nil {
		t.Fatal(err)
	}
	if got := contents(t)(o.Read(".")); got != "one" {
		t.Fatalf("content %q", got)
	}
	if _, err = o.Read("x"); !errors.Is(err, ErrPath) {
		t.Fatalf("file object accepted a sub path: %v", err)
	}
	if _, err = o.List(context.Background(), ".", 10); !errors.Is(err, ErrType) {
		t.Fatalf("file object listed: %v", err)
	}
	o.Close()

	// Renamed away: the path no longer leads to the object.
	if err = os.Rename(filepath.Join(root, "a.txt"), filepath.Join(root, "b.txt")); err != nil {
		t.Fatal(err)
	}
	if _, err = s.OpenObject("a.txt", id); !errors.Is(err, ErrGone) {
		t.Fatalf("renamed object still reachable: %v", err)
	}
	// Another file put at the old path is a different object.
	write(t, filepath.Join(root, "a.txt"), "other")
	if _, err = s.OpenObject("a.txt", id); !errors.Is(err, ErrGone) {
		t.Fatalf("replacement accepted as the original: %v", err)
	}
	// Replaced by a directory or a symlink with the same name.
	os.Remove(filepath.Join(root, "a.txt"))
	os.Symlink("b.txt", filepath.Join(root, "a.txt"))
	if _, err = s.OpenObject("a.txt", id); !errors.Is(err, ErrGone) {
		t.Fatalf("symlink accepted: %v", err)
	}
	for _, bad := range []string{".", ".filedeck", ".FILEDECK/trash", "../x", ""} {
		if _, _, err = s.Identify(bad); !errors.Is(err, ErrPath) {
			t.Errorf("identify %q: %v", bad, err)
		}
		if _, err = s.OpenObject(bad, id); !errors.Is(err, ErrPath) {
			t.Errorf("open %q: %v", bad, err)
		}
	}
}

func TestDirectoryObjectStaysBeneathItself(t *testing.T) {
	s, root, _ := fixture(t)
	outside := t.TempDir()
	write(t, filepath.Join(outside, "secret"), "secret")
	write(t, filepath.Join(root, "top.txt"), "top")
	os.MkdirAll(filepath.Join(root, "shared", "sub"), 0700)
	write(t, filepath.Join(root, "shared", "sub", "f.txt"), "inside")
	os.Symlink(filepath.Join(outside, "secret"), filepath.Join(root, "shared", "abs-link"))
	os.Symlink("../top.txt", filepath.Join(root, "shared", "rel-link"))
	os.Symlink(outside, filepath.Join(root, "shared", "dir-link"))
	id, _, err := s.Identify("shared")
	if err != nil || !id.Directory {
		t.Fatal(id, err)
	}
	o, err := s.OpenObject("shared", id)
	if err != nil {
		t.Fatal(err)
	}
	defer o.Close()
	list, err := o.List(context.Background(), ".", 100)
	if err != nil || len(list) != 1 || list[0].Name != "sub" {
		t.Fatalf("listing %+v %v", list, err)
	}
	if got := contents(t)(o.Read("sub/f.txt")); got != "inside" {
		t.Fatal(got)
	}
	for _, bad := range []string{"../top.txt", "abs-link", "rel-link", "dir-link/secret", "/etc/passwd", "sub/../../top.txt", "."} {
		if f, err := o.Read(bad); err == nil {
			f.Close()
			t.Errorf("read %q escaped the shared folder", bad)
		}
	}
	if _, err = o.List(context.Background(), "dir-link", 10); err == nil {
		t.Error("listed through a directory symlink")
	}

	// Even if someone moves the metadata directory into the shared folder, it
	// stays hidden and unreachable.
	if err = os.Rename(filepath.Join(root, MetaDir), filepath.Join(root, "shared", "moved")); err != nil {
		t.Fatal(err)
	}
	list, err = o.List(context.Background(), ".", 100)
	if err != nil || len(list) != 1 {
		t.Fatalf("metadata directory listed: %+v %v", list, err)
	}
	if _, err = o.List(context.Background(), "moved/trash", 10); !errors.Is(err, ErrPath) {
		t.Fatalf("metadata directory reachable: %v", err)
	}

	// The folder renamed or replaced: the identity no longer matches.
	os.Rename(filepath.Join(root, "shared"), filepath.Join(root, "renamed"))
	os.Mkdir(filepath.Join(root, "shared"), 0700)
	if _, err = s.OpenObject("shared", id); !errors.Is(err, ErrGone) {
		t.Fatalf("replaced folder accepted: %v", err)
	}
}
