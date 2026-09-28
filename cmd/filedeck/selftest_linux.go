package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/pbuzdygan/filedeck/internal/storage"
)

// selftest checks, on a real mounted directory (e.g. an SMB/NFS share), every
// filesystem property Filedeck relies on. It works inside a temporary folder
// "filedeck-selftest-<random>" in dir and removes it afterwards. It does not
// need the state directory, so it can run next to a running server:
//
//	docker compose run --rm filedeck selftest /spaces/nas
func selftest(dir string, modes storage.Modes, out io.Writer) error {
	sp, err := storage.OpenSpace(dir, modes)
	if err != nil {
		return fmt.Errorf("cannot open %s as a space: %w", dir, err)
	}
	defer sp.Close()
	if sp.ReadOnly() {
		return fmt.Errorf("%s is read-only for uid %d (mounted ro, or not writable by this user: check FILEDECK_USER and the mount's uid/gid)", dir, os.Geteuid())
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	failed := 0
	check := func(name string, err error) bool {
		if err != nil {
			failed++
			fmt.Fprintf(out, "FAIL  %s: %v\n", name, err)
			return false
		}
		fmt.Fprintf(out, "PASS  %s\n", name)
		return true
	}
	info := func(name, value string) { fmt.Fprintf(out, "INFO  %s: %s\n", name, value) }
	token := func(n int) string {
		b := make([]byte, n)
		rand.Read(b)
		return hex.EncodeToString(b)
	}
	item := func() string { return "item-" + token(32) }
	stage := func(content string, mode os.FileMode) (string, error) {
		name, f, err := sp.CreateStaging()
		if err != nil {
			return "", err
		}
		_, err = f.WriteString(content)
		if err == nil && mode != 0 {
			err = f.Chmod(mode)
		}
		if err == nil {
			err = f.Sync()
		}
		if err = errors.Join(err, f.Close()); err != nil {
			return "", errors.Join(err, sp.RemoveStaging(name))
		}
		return name, nil
	}
	read := func(path string) (string, error) {
		f, err := sp.Read(path)
		if err != nil {
			return "", err
		}
		defer f.Close()
		b, err := io.ReadAll(f)
		return string(b), err
	}
	expect := func(path, want string) error {
		got, err := read(path)
		if err == nil && got != want {
			err = fmt.Errorf("content %q, want %q", got, want)
		}
		return err
	}

	base := "filedeck-selftest-" + token(6)
	info("directory", dir)
	info("process uid/gid", fmt.Sprintf("%d/%d", os.Geteuid(), os.Getegid()))
	if !check("create test folder "+base, sp.Mkdir(base)) {
		return fmt.Errorf("%d check(s) failed", failed)
	}
	cleaned := false
	cleanup := func() {
		if cleaned {
			return
		}
		cleaned = true
		it := item()
		_, err := sp.Trash(base, it)
		if err == nil {
			err = sp.Purge(it)
		}
		check("clean up test folder", err)
	}
	defer cleanup() // also on early returns

	// Publication must be atomic and must never replace an existing entry.
	staged, err := stage("one", modes.File)
	if check("write and sync a staging file", err) {
		ok, err := sp.Publish(staged, base+"/a.txt")
		if !ok && err == nil {
			err = errors.New("not published")
		}
		check("publish with renameat2(RENAME_NOREPLACE)", err)
	}
	if staged, err = stage("two", 0); err == nil {
		ok, err := sp.Publish(staged, base+"/a.txt")
		switch {
		case ok:
			check("publish never overwrites", errors.New("an existing file was REPLACED — this filesystem ignores RENAME_NOREPLACE; do not use it for writing"))
		case !errors.Is(err, storage.ErrConflict):
			check("publish never overwrites", fmt.Errorf("unexpected error %v", err))
		default:
			check("publish never overwrites", expect(base+"/a.txt", "one"))
		}
		sp.RemoveStaging(staged)
	}
	if st, err := sp.Inspect(base + "/a.txt"); err == nil {
		info("mode of a new file", fmt.Sprintf("%04o (configured %04o; SMB mounts may fix modes via file_mode)", st.Mode, modes.File))
	}

	// Rename and move without overwriting.
	check("rename", sp.Rename(base+"/a.txt", base+"/b.txt"))
	check("create subfolder", sp.Mkdir(base+"/sub"))
	if st, err := sp.Inspect(base + "/sub"); err == nil {
		info("mode of a new folder", fmt.Sprintf("%04o (configured %04o)", st.Mode, modes.Dir))
	}
	if staged, err = stage("three", modes.File); err == nil {
		if ok, _ := sp.Publish(staged, base+"/sub/c.txt"); !ok {
			sp.RemoveStaging(staged)
		}
	}
	if err := sp.Rename(base+"/b.txt", base+"/sub/c.txt"); !errors.Is(err, storage.ErrConflict) {
		check("rename never overwrites", fmt.Errorf("got %v, want a conflict", err))
	} else {
		check("rename never overwrites", expect(base+"/sub/c.txt", "three"))
	}

	// Stable identity: editor versions compare inode, size and mtime.
	v1, err1 := sp.Inspect(base + "/b.txt")
	v2, err2 := sp.Inspect(base + "/b.txt")
	if err = errors.Join(err1, err2); err == nil && v1.Version != v2.Version {
		err = fmt.Errorf("version changed without a write (%s, %s): the editor would always report a conflict", v1.Version, v2.Version)
	}
	check("stable file identity for editor versions", err)

	// Editor save: replace with version check, previous version to the trash.
	if staged, err = stage("edited", 0); err == nil {
		it := item()
		err = sp.Replace(staged, base+"/b.txt", it, v1.Version)
		if check("editor save (replace, previous version to trash)", errors.Join(err, expect(base+"/b.txt", "edited"))) {
			_, terr := sp.TrashEntry(it)
			check("previous version kept in trash", terr)
			sp.Purge(it)
		} else {
			sp.RemoveStaging(staged)
		}
	}
	if staged, err = stage("stale", 0); err == nil {
		err = sp.Replace(staged, base+"/b.txt", item(), v1.Version)
		if !errors.Is(err, storage.ErrChanged) {
			check("editor refuses a stale version", fmt.Errorf("got %v", err))
		} else {
			check("editor refuses a stale version", expect(base+"/b.txt", "edited"))
		}
		sp.RemoveStaging(staged)
	}

	// Trash and restore a folder.
	it := item()
	if _, err = sp.Trash(base+"/sub", it); check("move folder to trash", err) {
		check("restore folder from trash", errors.Join(sp.Restore(it, base+"/sub"), expect(base+"/sub/c.txt", "three")))
	}

	// Copy a tree within the space (as between spaces on the same mount).
	name, res, err := storage.CopyToStaging(ctx, sp, base+"/sub", sp, storage.CopyLimits{MaxBytes: 1 << 20, MaxEntries: 100}, nil)
	if check(fmt.Sprintf("copy a folder (%d file(s))", res.Files), err) {
		ok, perr := sp.Publish(name, base+"/sub-copy")
		if !ok {
			sp.RemoveCopy(name)
		}
		check("publish the copy", errors.Join(perr, expect(base+"/sub-copy/c.txt", "three")))
	}

	// Search.
	hits, _, err := sp.Search(ctx, base, "c.txt", storage.SearchLimits{MaxScanned: 1000, MaxResults: 10, MaxDepth: 5})
	if err == nil && len(hits) != 2 {
		err = fmt.Errorf("found %d, want 2", len(hits))
	}
	check("search by name", err)

	// Case sensitivity and hiding of the metadata directory.
	if staged, err = stage("x", 0); err == nil {
		if ok, _ := sp.Publish(staged, base+"/CaseTest.txt"); !ok {
			sp.RemoveStaging(staged)
		}
	}
	if _, err = sp.Inspect(base + "/casetest.txt"); err == nil {
		info("case-insensitive filesystem", "yes (typical for SMB) — .filedeck aliases are blocked by name and by inode")
	} else {
		info("case-insensitive filesystem", "no")
	}
	for _, alias := range []string{".filedeck/staging", ".FILEDECK/staging", ".FileDeck./trash"} {
		if _, err = sp.Stat(alias); !errors.Is(err, storage.ErrPath) {
			check("metadata directory unreachable via "+alias, fmt.Errorf("got %v", err))
		}
	}
	check("metadata directory unreachable (name variants)", nil)
	if entries, err := sp.List(ctx, ".", 10000); err == nil {
		for _, e := range entries {
			if storage.Reserved(e.Name) {
				err = errors.New(".filedeck is listed")
			}
		}
		check("metadata directory hidden from listings", err)
	} else {
		info("root listing", "skipped: "+err.Error())
	}

	cleanup()
	fmt.Fprintln(out)
	if failed > 0 {
		return fmt.Errorf("%d check(s) failed — do not use this directory for writing until they pass", failed)
	}
	fmt.Fprintln(out, "All checks passed: this directory supports everything Filedeck needs.")
	return nil
}
