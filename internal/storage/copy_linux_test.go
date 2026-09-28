package storage

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

func TestCopyTreeBetweenSpaces(t *testing.T) {
	old := unix.Umask(0)
	defer unix.Umask(old)
	src, srcRoot, _ := fixture(t)
	dstRoot := filepath.Join(t.TempDir(), "dst")
	os.Mkdir(dstRoot, 0700)
	dst, err := OpenSpace(dstRoot, DefaultModes())
	if err != nil {
		t.Fatal(err)
	}
	defer dst.Close()
	outside := t.TempDir()
	write(t, filepath.Join(outside, "secret"), "outside")
	os.MkdirAll(filepath.Join(srcRoot, "tree", "sub"), 0700)
	write(t, filepath.Join(srcRoot, "tree", "a.txt"), "A")
	write(t, filepath.Join(srcRoot, "tree", "sub", "b.txt"), "BB")
	os.Symlink(outside, filepath.Join(srcRoot, "tree", "escape"))
	os.Symlink(filepath.Join(outside, "secret"), filepath.Join(srcRoot, "tree", "secret-link"))
	unix.Mkfifo(filepath.Join(srcRoot, "tree", "pipe"), 0600)

	var seen int64
	name, res, err := CopyToStaging(context.Background(), src, "tree", dst, CopyLimits{MaxBytes: 1 << 20, MaxEntries: 100}, func(_, b int64) { seen = b })
	if err != nil {
		t.Fatal(err)
	}
	if res.Files != 2 || res.Dirs != 2 || res.Bytes != 3 || res.Skipped != 3 || seen != 3 {
		t.Fatalf("%+v %d", res, seen)
	}
	if ok, err := dst.Publish(name, "copied"); !ok || err != nil {
		t.Fatal(ok, err)
	}
	b, _ := os.ReadFile(filepath.Join(dstRoot, "copied", "sub", "b.txt"))
	st, _ := os.Stat(filepath.Join(dstRoot, "copied", "a.txt"))
	dir, _ := os.Stat(filepath.Join(dstRoot, "copied"))
	if string(b) != "BB" || st.Mode().Perm() != 0640 || dir.Mode().Perm() != 0750 {
		t.Fatal(string(b), st.Mode(), dir.Mode())
	}
	for _, skipped := range []string{"escape", "secret-link", "pipe"} {
		if _, err := os.Lstat(filepath.Join(dstRoot, "copied", skipped)); !errors.Is(err, os.ErrNotExist) {
			t.Error("copied", skipped)
		}
	}
	// A second copy to the same target never replaces it.
	name, _, _ = CopyToStaging(context.Background(), src, "tree/a.txt", dst, CopyLimits{}, nil)
	if ok, err := dst.Publish(name, "copied"); ok || !errors.Is(err, ErrConflict) {
		t.Fatal(ok, err)
	}
	if err = dst.RemoveCopy(name); err != nil {
		t.Fatal(err)
	}
	// Limits and cancellation leave nothing behind.
	if _, _, err = CopyToStaging(context.Background(), src, "tree", dst, CopyLimits{MaxBytes: 2}, nil); !errors.Is(err, ErrCopyLimit) {
		t.Fatal(err)
	}
	if _, _, err = CopyToStaging(context.Background(), src, "tree", dst, CopyLimits{MaxEntries: 2}, nil); !errors.Is(err, ErrCopyLimit) {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err = CopyToStaging(ctx, src, "tree", dst, CopyLimits{}, nil); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if left, _ := os.ReadDir(filepath.Join(dstRoot, MetaDir, "staging")); len(left) != 0 {
		t.Fatal("partial copies left", left)
	}
	for _, bad := range []string{".filedeck/staging", "tree/escape", "tree/pipe", "../x"} {
		if _, _, err = CopyToStaging(context.Background(), src, bad, dst, CopyLimits{}, nil); err == nil {
			t.Error("copied", bad)
		}
	}
	// Interrupted copies are removed on recovery.
	os.MkdirAll(filepath.Join(dstRoot, MetaDir, "staging", "copy-"+strings.Repeat("a", 64), "x"), 0700)
	if _, err = dst.Recover(nil); err != nil {
		t.Fatal(err)
	}
	if left, _ := os.ReadDir(filepath.Join(dstRoot, MetaDir, "staging")); len(left) != 0 {
		t.Fatal("interrupted copy kept", left)
	}
	if b, _ := os.ReadFile(filepath.Join(outside, "secret")); string(b) != "outside" {
		t.Fatal("outside touched")
	}
}

func TestFreeSpaceReserve(t *testing.T) {
	s, root, _ := fixture(t)
	write(t, filepath.Join(root, "f"), "data")
	saved := MinFreeBytes
	MinFreeBytes = 1 << 62 // more than any disk
	defer func() { MinFreeBytes = saved }()
	if err := s.Reserve(1); !errors.Is(err, unix.ENOSPC) {
		t.Fatal(err)
	}
	if _, _, err := CopyToStaging(context.Background(), s, "f", s, CopyLimits{}, nil); !errors.Is(err, unix.ENOSPC) {
		t.Fatal(err)
	}
	MinFreeBytes = 0
	if err := s.Reserve(1); err != nil {
		t.Fatal(err)
	}
}
