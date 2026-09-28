package main

import (
	"github.com/pbuzdygan/filedeck/internal/storage"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOperatorCLI(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "files")
	state := filepath.Join(base, "state")
	source := filepath.Join(base, "source")
	for _, p := range []string{root, state} {
		if err := os.Mkdir(p, 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(source, []byte("hello"), 0600); err != nil {
		t.Fatal(err)
	}
	args := []string{"-root", root, "-state", state}
	if err := run(append(args, "put", source, "new")); err != nil {
		t.Fatal(err)
	}
	if err := run(append(args, "list")); err != nil {
		t.Fatal(err)
	}
	if err := run(append(args, "read", "new")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, []byte("replacement"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := run(append(args, "put", source, "new")); err == nil {
		t.Fatal("overwritten existing destination")
	}
	b, err := os.ReadFile(filepath.Join(root, "new"))
	if err != nil || string(b) != "hello" {
		t.Fatal(string(b), err)
	}
	remaining, err := os.ReadDir(state)
	if err != nil || len(remaining) != 1 || remaining[0].Name() != "uploads.db" {
		t.Fatal(remaining, err)
	}
}

func TestDiscoverSpaces(t *testing.T) {
	base := t.TempDir()
	dir := filepath.Join(base, "spaces")
	for _, p := range []string{filepath.Join(dir, "nas"), filepath.Join(dir, "photos")} {
		if err := os.MkdirAll(p, 0700); err != nil {
			t.Fatal(err)
		}
	}
	got, err := discoverSpaces("/files", dir)
	if err != nil || len(got) != 3 || got["files"] != "/files" || got["nas"] != filepath.Join(dir, "nas") {
		t.Fatal(got, err)
	}
	if got, err = discoverSpaces("/files", filepath.Join(base, "missing")); err != nil || len(got) != 1 {
		t.Fatal(got, err)
	}
	for _, bad := range []string{"Bad Name", "files"} {
		d := t.TempDir()
		os.Mkdir(filepath.Join(d, bad), 0700)
		if _, err = discoverSpaces("/files", d); err == nil {
			t.Fatal("accepted", bad)
		}
	}
	d := t.TempDir()
	os.Symlink(t.TempDir(), filepath.Join(d, "link"))
	if _, err = discoverSpaces("", d); err == nil {
		t.Fatal("symlinked space accepted")
	}
}

func TestFirstStartCreatesPrivateDirectories(t *testing.T) {
	base := t.TempDir()
	// A shared, sticky mount point like the Docker image provides.
	if err := os.Chmod(base, 01777); err != nil {
		t.Fatal(err)
	}
	state, root := filepath.Join(base, "state"), filepath.Join(base, "own")
	if err := run([]string{"-root", root, "-state", state, "list"}); err != nil {
		t.Fatal(err)
	}
	for dir, want := range map[string]os.FileMode{state: 0700, root: 0750} {
		st, err := os.Lstat(dir)
		if err != nil || !st.IsDir() || st.Mode().Perm() != want {
			t.Fatal(dir, st.Mode(), err)
		}
	}
	// An existing symlink in place of state is not replaced, and is rejected.
	link := filepath.Join(t.TempDir(), "state")
	if err := os.Symlink(t.TempDir(), link); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"-root", root, "-state", link, "list"}); err == nil {
		t.Fatal("symlinked state accepted")
	}
}

func TestSelftestPassesOnLocalDirectory(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "existing.txt"), []byte("keep"), 0600)
	var out strings.Builder
	if err := selftest(dir, storage.DefaultModes(), &out); err != nil {
		t.Fatal(err, out.String())
	}
	if !strings.Contains(out.String(), "All checks passed") || strings.Contains(out.String(), "FAIL") {
		t.Fatal(out.String())
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "filedeck-selftest-") {
			t.Fatal("test folder left behind")
		}
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "existing.txt")); string(b) != "keep" {
		t.Fatal("existing data touched")
	}
	if err := selftest(filepath.Join(dir, "missing"), storage.DefaultModes(), &out); err == nil {
		t.Fatal("missing directory accepted")
	}
}

func TestSecretKeyFromEnvironmentOrFile(t *testing.T) {
	good := "BwcHBwcHBwcHBwcHBwcHBwcHBwcHBwcHBwcHBwcHBwc=" // 32 bytes of 7
	file := filepath.Join(t.TempDir(), "key")
	if err := os.WriteFile(file, []byte(good+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		value, file string
		ok, empty   bool
	}{
		{"", "", true, true},
		{good, "", true, false},
		{" " + good + "\n", "", true, false},
		{"", file, true, false},
		{good, file, false, false},
		{"BwcHBwcHBwcHBwcHBwcHBw==", "", false, false}, // 16 bytes
		{"not base64!", "", false, false},
		{"", file + ".missing", false, false},
	} {
		t.Setenv("FILEDECK_SECRET_KEY", c.value)
		t.Setenv("FILEDECK_SECRET_KEY_FILE", c.file)
		key, err := secretKey()
		if (err == nil) != c.ok || (key == nil) != (c.empty || !c.ok) || (key != nil && len(key) != 32) {
			t.Fatalf("%q %q: %v %v", c.value, c.file, key, err)
		}
	}
}
