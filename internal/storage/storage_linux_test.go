package storage

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func fixture(t *testing.T) (*Space, string, string) {
	t.Helper()
	root := filepath.Join(t.TempDir(), "root")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	s, err := OpenSpace(root, DefaultModes())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s, root, filepath.Join(root, MetaDir, "staging")
}
func write(t *testing.T, name, value string) {
	t.Helper()
	if err := os.WriteFile(name, []byte(value), 0600); err != nil {
		t.Fatal(err)
	}
}
func item(c string) string { return "item-" + strings.Repeat(c, 64) }

func TestPathContract(t *testing.T) {
	for _, name := range []string{"", ".", "..", "../secret", "a/../b", "a/./b", "/etc/passwd", "a//b", "a/", "a\\b", "a\x00b", string([]byte{255}), strings.Repeat("a", 256)} {
		if ValidPath(name, false) {
			t.Errorf("accepted %q", name)
		}
	}
	for _, name := range []string{"file.txt", "katalog/zażółć.txt", "%2e%2e", "a b"} {
		if !ValidPath(name, false) {
			t.Errorf("rejected literal name %q", name)
		}
	}
	if !ValidPath(".", true) {
		t.Fatal("root listing rejected")
	}
	for _, name := range []string{".filedeck", ".FileDeck", ".filedeck.", ".filedeck ", ".filedecK"} {
		if !Reserved(name) {
			t.Errorf("metadata alias not reserved: %q", name)
		}
	}
	if Reserved("filedeck") || Reserved(".filedecks") {
		t.Fatal("ordinary name reserved")
	}
}

func TestMetadataDirectoryIsHiddenAndUnreachable(t *testing.T) {
	s, root, staging := fixture(t)
	write(t, filepath.Join(staging, "upload-"+strings.Repeat("b", 64)+".part"), "partial")
	write(t, filepath.Join(root, "visible"), "ok")
	entries, err := s.List(context.Background(), ".", 10)
	if err != nil || len(entries) != 1 || entries[0].Name != "visible" {
		t.Fatal(entries, err)
	}
	for _, name := range []string{".filedeck/staging", ".FILEDECK/trash", ".filedeck./staging"} {
		if _, err = s.List(context.Background(), name, 10); !errors.Is(err, ErrPath) {
			t.Error(name, err)
		}
		if err = s.Mkdir(name + "/x"); !errors.Is(err, ErrPath) {
			t.Error(name, err)
		}
	}
	// An alias with an unrelated name but the same directory (as an SMB server
	// might present) is detected by identity, not by name.
	if err = os.Link(filepath.Join(root, "visible"), filepath.Join(root, "hardlink")); err != nil {
		t.Fatal(err)
	}
	if err = unix.Renameat2(unix.AT_FDCWD, filepath.Join(root, MetaDir), unix.AT_FDCWD, filepath.Join(root, "alias"), 0); err != nil {
		t.Fatal(err)
	}
	if _, err = s.List(context.Background(), "alias", 10); !errors.Is(err, ErrPath) {
		t.Fatal("metadata reachable through an alias name", err)
	}
	if _, err = s.Stat("alias/staging"); !errors.Is(err, ErrPath) {
		t.Fatal(err)
	}
}

func TestUnsafeMetadataDirectoryIsRejected(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, MetaDir), 0777); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(root, MetaDir), 0777); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenSpace(root, DefaultModes()); err == nil {
		t.Fatal("world-writable metadata accepted")
	}
	other := t.TempDir()
	if err := os.Symlink(t.TempDir(), filepath.Join(other, MetaDir)); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenSpace(other, DefaultModes()); err == nil {
		t.Fatal("symlinked metadata accepted")
	}
	alias := filepath.Join(t.TempDir(), "root")
	if err := os.Symlink(t.TempDir(), alias); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenSpace(alias, DefaultModes()); err == nil {
		t.Fatal("symlink root accepted")
	}
	if _, err := OpenSpace(t.TempDir(), Modes{File: 0200, Dir: 0700}); err == nil {
		t.Fatal("unreadable file mode accepted")
	}
}

func TestReadOnlySpace(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root bypasses permission checks")
	}
	root := t.TempDir()
	write(t, filepath.Join(root, "file"), "data")
	if err := os.Chmod(root, 0500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(root, 0700) })
	s, err := OpenSpace(root, DefaultModes())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if !s.ReadOnly() {
		t.Fatal("not detected as read-only")
	}
	if f, err := s.Read("file"); err != nil {
		t.Fatal(err)
	} else {
		f.Close()
	}
	if err = s.Mkdir("x"); !errors.Is(err, ErrReadOnly) {
		t.Fatal(err)
	}
	if _, _, err = s.CreateStaging(); !errors.Is(err, ErrReadOnly) {
		t.Fatal(err)
	}
	if _, err = s.Trash("file", item("a")); !errors.Is(err, ErrReadOnly) {
		t.Fatal(err)
	}
}

func TestStateIsPrivateLockedAndDisjoint(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "state")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	legacy := filepath.Join(dir, "upload-"+strings.Repeat("c", 64)+".part")
	write(t, legacy, "old staging")
	st, err := OpenState(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if _, err = os.Stat(legacy); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("legacy staging kept", err)
	}
	if _, err = OpenState(dir); err == nil {
		t.Fatal("second instance accepted")
	}
	if !st.Overlaps(filepath.Dir(dir)) || !st.Overlaps(filepath.Join(dir, "x")) || st.Overlaps(t.TempDir()) {
		t.Fatal("overlap detection")
	}
	public := filepath.Join(t.TempDir(), "state")
	if err = os.Mkdir(public, 0755); err != nil {
		t.Fatal(err)
	}
	if _, err = OpenState(public); err == nil {
		t.Fatal("public state accepted")
	}
	if err = os.Symlink(filepath.Join(t.TempDir(), "x"), filepath.Join(dir, "db")); err != nil {
		t.Fatal(err)
	}
	if _, err = st.OpenFile("db", os.O_RDWR|os.O_CREATE); err == nil {
		t.Fatal("symlinked state file accepted")
	}
}

func TestReadRejectsSymlinksAndFIFO(t *testing.T) {
	s, root, _ := fixture(t)
	outside := t.TempDir()
	write(t, filepath.Join(outside, "secret"), "outside")
	write(t, filepath.Join(root, "safe"), "inside")
	for name, target := range map[string]string{"external": filepath.Join(outside, "secret"), "internal": "safe", "dangling": filepath.Join(outside, "missing"), "dirlink": outside} {
		if err := os.Symlink(target, filepath.Join(root, name)); err != nil {
			t.Fatal(err)
		}
	}
	if err := unix.Mkfifo(filepath.Join(root, "pipe"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"external", "internal", "dangling", "dirlink/secret", "pipe"} {
		t.Run(name, func(t *testing.T) {
			done := make(chan error, 1)
			go func() {
				f, err := s.Read(name)
				if f != nil {
					f.Close()
				}
				done <- err
			}()
			select {
			case err := <-done:
				if err == nil {
					t.Fatal("opened forbidden entry")
				}
			case <-time.After(time.Second):
				t.Fatal("open blocked")
			}
		})
	}
	f, err := s.Read("safe")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	data, err := io.ReadAll(f)
	if err != nil || string(data) != "inside" {
		t.Fatalf("%q %v", data, err)
	}
}

func TestListIsBoundedAndOmitsSpecialEntries(t *testing.T) {
	s, root, _ := fixture(t)
	write(t, filepath.Join(root, "file"), "data")
	if err := os.Symlink("file", filepath.Join(root, "alias")); err != nil {
		t.Fatal(err)
	}
	if err := unix.Mkfifo(filepath.Join(root, "fifo"), 0600); err != nil {
		t.Fatal(err)
	}
	// file, alias, fifo and .filedeck are all scanned.
	entries, err := s.List(context.Background(), ".", 4)
	if err != nil || len(entries) != 1 || entries[0].Name != "file" {
		t.Fatalf("%v %v", entries, err)
	}
	if _, err = s.List(context.Background(), ".", 3); !errors.Is(err, ErrLimit) {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = s.List(ctx, ".", 10); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestRecoveryOnlyDeletesOwnedStagingNames(t *testing.T) {
	s, root, staging := fixture(t)
	write(t, filepath.Join(root, "keep"), "user data")
	write(t, filepath.Join(staging, "operator-note"), "keep")
	name, f, err := s.CreateStaging()
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString("partial")
	f.Close()
	kept, g, err := s.CreateStaging()
	if err != nil {
		t.Fatal(err)
	}
	g.Close()
	s.Close()
	reopened, err := OpenSpace(root, DefaultModes())
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	n, err := reopened.Recover(map[string]bool{kept: true})
	if err != nil || n != 1 {
		t.Fatalf("%d %v", n, err)
	}
	if _, err = os.Stat(filepath.Join(staging, name)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	for _, p := range []string{filepath.Join(staging, "operator-note"), filepath.Join(staging, kept), filepath.Join(root, "keep")} {
		if _, err = os.Stat(p); err != nil {
			t.Fatal(err)
		}
	}
	if err = reopened.RemoveStaging("../../keep"); !errors.Is(err, ErrPath) {
		t.Fatal(err)
	}
}

func TestRecoveryRefusesSymlinkInsteadOfFollowingIt(t *testing.T) {
	s, root, staging := fixture(t)
	write(t, filepath.Join(root, "keep"), "original")
	name := "upload-" + strings.Repeat("a", 64) + ".part"
	if err := os.Symlink(filepath.Join(root, "keep"), filepath.Join(staging, name)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Recover(nil); err == nil {
		t.Fatal("suspicious entry accepted")
	}
	b, err := os.ReadFile(filepath.Join(root, "keep"))
	if err != nil || string(b) != "original" {
		t.Fatalf("%q %v", b, err)
	}
}

func TestPublishNeverOverwritesAnyExistingEntry(t *testing.T) {
	s, root, _ := fixture(t)
	outside := t.TempDir()
	write(t, filepath.Join(outside, "secret"), "outside")
	write(t, filepath.Join(root, "existing"), "original")
	if err := os.Mkdir(filepath.Join(root, "directory"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "secret"), filepath.Join(root, "symlink")); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"existing", "directory", "symlink", ".filedeck/staging/x"} {
		name, f, err := s.CreateStaging()
		if err != nil {
			t.Fatal(err)
		}
		f.WriteString("new")
		f.Close()
		published, err := s.Publish(name, target)
		if published || err == nil {
			t.Fatalf("%s: %t %v", target, published, err)
		}
		if err = s.RemoveStaging(name); err != nil {
			t.Fatal(err)
		}
	}
	b, _ := os.ReadFile(filepath.Join(root, "existing"))
	if string(b) != "original" {
		t.Fatal(string(b))
	}
	b, _ = os.ReadFile(filepath.Join(outside, "secret"))
	if string(b) != "outside" {
		t.Fatal(string(b))
	}
}

func TestMkdirUsesConfiguredMode(t *testing.T) {
	old := unix.Umask(0)
	defer unix.Umask(old)
	root := t.TempDir()
	s, err := OpenSpace(root, Modes{File: 0644, Dir: 0755})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err = s.Mkdir("shared"); err != nil {
		t.Fatal(err)
	}
	st, _ := os.Stat(filepath.Join(root, "shared"))
	if st.Mode().Perm() != 0755 || s.FileMode() != 0644 {
		t.Fatal(st.Mode())
	}
	meta, _ := os.Stat(filepath.Join(root, MetaDir))
	if meta.Mode().Perm() != 0700 {
		t.Fatal("metadata not private", meta.Mode())
	}
}

func TestRenameNeverReplacesOrEscapes(t *testing.T) {
	s, root, _ := fixture(t)
	outside := t.TempDir()
	write(t, filepath.Join(root, "a"), "A")
	write(t, filepath.Join(root, "b"), "B")
	if err := os.Mkdir(filepath.Join(root, "dir"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "out")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "x"), filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	cases := map[[2]string]error{
		{"a", "b"}:                 ErrConflict,
		{"a", "dir"}:               ErrConflict,
		{"dir", "dir/inner"}:       ErrPath,
		{"a", ".filedeck/trash/a"}: ErrPath,
		{".filedeck", "meta"}:      ErrPath,
		{"link", "moved-link"}:     ErrType,
	}
	for c, want := range cases {
		if err := s.Rename(c[0], c[1]); !errors.Is(err, want) {
			t.Errorf("%v: %v, want %v", c, err, want)
		}
	}
	if err := s.Rename("a", "out/escaped"); err == nil {
		t.Fatal("renamed through symlink")
	}
	if err := s.Rename("a", "dir/a2"); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(root, "b"))
	c, _ := os.ReadFile(filepath.Join(root, "dir", "a2"))
	if string(b) != "B" || string(c) != "A" {
		t.Fatal(string(b), string(c))
	}
	if entries, _ := os.ReadDir(outside); len(entries) != 0 {
		t.Fatal(entries)
	}
}

func TestTrashRestoreAndPurge(t *testing.T) {
	s, root, _ := fixture(t)
	outside := t.TempDir()
	write(t, filepath.Join(outside, "precious"), "outside")
	if err := os.MkdirAll(filepath.Join(root, "tree", "sub"), 0700); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(root, "tree", "sub", "f"), "x")
	// A symlink inside the trashed tree must be removed, never followed.
	if err := os.Symlink(outside, filepath.Join(root, "tree", "escape")); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(root, "file"), "data")
	entry, err := s.Trash("file", item("1"))
	if err != nil || entry.Directory || entry.Size != 4 {
		t.Fatal(entry, err)
	}
	if _, err = s.Trash("tree", item("2")); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Trash("missing", item("3")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	if _, err = s.Trash("file", "item-../../x"); !errors.Is(err, ErrPath) {
		t.Fatal(err)
	}
	items, err := s.TrashItems()
	if err != nil || len(items) != 2 {
		t.Fatal(items, err)
	}
	write(t, filepath.Join(root, "file"), "new occupant")
	if err = s.Restore(item("1"), "file"); !errors.Is(err, ErrConflict) {
		t.Fatal("restore replaced a file", err)
	}
	if err = s.Restore(item("1"), "file-restored"); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(root, "file-restored"))
	if string(b) != "data" {
		t.Fatal(string(b))
	}
	if err = s.Purge(item("2")); err != nil {
		t.Fatal(err)
	}
	if items, _ = s.TrashItems(); len(items) != 0 {
		t.Fatal(items)
	}
	if b, err = os.ReadFile(filepath.Join(outside, "precious")); err != nil || string(b) != "outside" {
		t.Fatal("purge followed a symlink", err)
	}
	if err = s.Purge(item("9")); err != nil {
		t.Fatal("purging a missing item should be idempotent", err)
	}
}

func TestConcurrentDirectorySymlinkSwapCannotReadOutside(t *testing.T) {
	s, root, _ := fixture(t)
	outside := t.TempDir()
	write(t, filepath.Join(outside, "file"), "outside")
	if err := os.Mkdir(filepath.Join(root, "dir"), 0700); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(root, "dir", "file"), "inside")
	if err := os.Symlink(outside, filepath.Join(root, "swap")); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	wg.Add(1)
	errs := make(chan error, 1)
	go func() {
		defer wg.Done()
		for i := 0; i < 500; i++ {
			if err := unix.Renameat2(unix.AT_FDCWD, filepath.Join(root, "dir"), unix.AT_FDCWD, filepath.Join(root, "swap"), unix.RENAME_EXCHANGE); err != nil {
				errs <- err
				return
			}
		}
	}()
	for i := 0; i < 500; i++ {
		f, err := s.Read("dir/file")
		if err != nil {
			continue
		}
		b, err := io.ReadAll(f)
		f.Close()
		if err != nil || string(b) != "inside" {
			t.Errorf("escaped: %q %v", b, err)
			break
		}
	}
	wg.Wait()
	select {
	case err := <-errs:
		t.Fatal(err)
	default:
	}
}

func FuzzValidPath(f *testing.F) {
	for _, seed := range []string{"a/b", "../x", "a\\b", "%2e%2e", "zażółć"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, name string) {
		if ValidPath(name, false) {
			if filepath.IsAbs(name) || filepath.Clean(name) != name || name == "." || strings.Contains(name, "\\") {
				t.Fatalf("unsafe accepted path %q", name)
			}
		}
	})
}
