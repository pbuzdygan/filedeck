package core

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pbuzdygan/filedeck/internal/storage"
)

type multi struct {
	s                 *Service
	state, own, share string
}

func setupSpaces(t *testing.T) multi {
	t.Helper()
	base := t.TempDir()
	m := multi{state: filepath.Join(base, "state"), own: filepath.Join(base, "files"), share: filepath.Join(base, "nas")}
	for _, p := range []string{m.state, m.own, m.share} {
		if err := os.Mkdir(p, 0700); err != nil {
			t.Fatal(err)
		}
	}
	limits := Limits{MaxFileBytes: 16, MaxStagingBytes: 64, MaxChunkBytes: 16, MaxUploads: 8, MaxUploadsPerUser: 4, TTL: time.Minute, TrashRetention: time.Hour}
	s, err := Open(m.state, map[string]string{"files": m.own, "nas": m.share}, limits, storage.DefaultModes())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	m.s = s
	return m
}

func upload(t *testing.T, s *Service, user, space, target, data string) {
	t.Helper()
	ctx := context.Background()
	u, err := s.Begin(ctx, user, space, target, int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Patch(ctx, user, u.ID, 0, strings.NewReader(data)); err != nil {
		t.Fatal(err)
	}
	if ok, err := s.Commit(ctx, user, u.ID); !ok || err != nil {
		t.Fatal(ok, err)
	}
}

func TestPermissionsArePerSpace(t *testing.T) {
	m := setupSpaces(t)
	ctx := context.Background()
	if err := m.s.SetPermissions("alice", map[string]Permission{"files": AllPermissions, "nas": List | Read}); err != nil {
		t.Fatal(err)
	}
	if err := m.s.SetPermissions("admin", map[string]Permission{AnySpace: AllPermissions}); err != nil {
		t.Fatal(err)
	}
	if err := m.s.SetPermissions("x", map[string]Permission{"../evil": List}); err == nil {
		t.Fatal("invalid space key accepted")
	}
	if err := m.s.SetPermissions("x", map[string]Permission{"files": 1 << 7}); err == nil {
		t.Fatal("unknown bit accepted")
	}
	upload(t, m.s, "alice", "files", "own.txt", "mine")
	if _, err := m.s.Begin(ctx, "alice", "nas", "nope", 1); !errors.Is(err, ErrDenied) {
		t.Fatal("create allowed without grant", err)
	}
	if err := m.s.Mkdir(ctx, "alice", "nas", "dir"); !errors.Is(err, ErrDenied) {
		t.Fatal(err)
	}
	if _, err := m.s.List(ctx, "alice", "unknown", ".", 10); !errors.Is(err, ErrDenied) {
		t.Fatal(err)
	}
	upload(t, m.s, "admin", "nas", "shared.txt", "for all")
	f, err := m.s.Read(ctx, "alice", "nas", "shared.txt")
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	// Paths are relative to the space: the same name in another space is distinct.
	if _, err = m.s.Read(ctx, "alice", "files", "shared.txt"); !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	eff := m.s.Effective("alice")
	if eff["files"] != AllPermissions || eff["nas"] != List|Read || len(eff) != 2 {
		t.Fatal(eff)
	}
	if eff = m.s.Effective("admin"); eff["nas"] != AllPermissions || eff["files"] != AllPermissions {
		t.Fatal(eff)
	}
	if len(m.s.Effective("nobody")) != 0 {
		t.Fatal("grants for unknown subject")
	}
	// Revoking Create on a space blocks publication of an upload in progress.
	u, err := m.s.Begin(ctx, "alice", "files", "late.txt", 1)
	if err != nil {
		t.Fatal(err)
	}
	m.s.Patch(ctx, "alice", u.ID, 0, strings.NewReader("x"))
	m.s.SetPermissions("alice", map[string]Permission{"files": List})
	if ok, err := m.s.Commit(ctx, "alice", u.ID); ok || !errors.Is(err, ErrDenied) {
		t.Fatal(ok, err)
	}
}

func TestRenameRequiresModify(t *testing.T) {
	m := setupSpaces(t)
	ctx := context.Background()
	m.s.SetPermissions("writer", map[string]Permission{AnySpace: List | Read | Create})
	m.s.SetPermissions("editor", map[string]Permission{AnySpace: AllPermissions})
	upload(t, m.s, "writer", "files", "a.txt", "a")
	if err := m.s.Rename(ctx, "writer", "files", "a.txt", "b.txt"); !errors.Is(err, ErrDenied) {
		t.Fatal(err)
	}
	if _, err := m.s.Trash(ctx, "writer", "files", "a.txt"); !errors.Is(err, ErrDenied) {
		t.Fatal(err)
	}
	if err := m.s.Rename(ctx, "editor", "files", "a.txt", "b.txt"); err != nil {
		t.Fatal(err)
	}
	if err := m.s.Rename(ctx, "editor", "files", "b.txt", ".filedeck/x"); !errors.Is(err, storage.ErrPath) {
		t.Fatal(err)
	}
	content(t, filepath.Join(m.own, "b.txt"), "a")
}

func TestTrashLifecycle(t *testing.T) {
	m := setupSpaces(t)
	ctx := context.Background()
	m.s.SetPermissions("editor", map[string]Permission{AnySpace: AllPermissions})
	m.s.SetPermissions("other", map[string]Permission{"nas": AllPermissions, "files": List | Read})
	if err := os.MkdirAll(filepath.Join(m.own, "docs", "sub"), 0700); err != nil {
		t.Fatal(err)
	}
	upload(t, m.s, "editor", "files", "docs/sub/f.txt", "deep")
	upload(t, m.s, "editor", "files", "note.txt", "note")
	dir, err := m.s.Trash(ctx, "editor", "files", "docs")
	if err != nil || !dir.Directory || dir.Path != "docs" || dir.DeletedBy != "editor" {
		t.Fatal(dir, err)
	}
	file, err := m.s.Trash(ctx, "editor", "files", "note.txt")
	if err != nil || file.Size != 4 {
		t.Fatal(file, err)
	}
	if _, err = os.Stat(filepath.Join(m.own, "docs")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("still present")
	}
	list, err := m.s.TrashList("editor", "files")
	if err != nil || len(list) != 2 {
		t.Fatal(list, err)
	}
	if _, err = m.s.TrashList("other", "files"); !errors.Is(err, ErrDenied) {
		t.Fatal("trash visible without Modify", err)
	}
	if list, _ = m.s.TrashList("other", "nas"); len(list) != 0 {
		t.Fatal("trash leaked across spaces", list)
	}
	if err = m.s.Restore(ctx, "other", dir.ID, ""); !errors.Is(err, ErrDenied) {
		t.Fatal("restore from a space without Modify", err)
	}
	// Restore never overwrites: a new file took the original name.
	upload(t, m.s, "editor", "files", "note.txt", "newer")
	if err = m.s.Restore(ctx, "editor", file.ID, ""); !errors.Is(err, storage.ErrConflict) {
		t.Fatal(err)
	}
	if err = m.s.Restore(ctx, "editor", file.ID, "note (restored).txt"); err != nil {
		t.Fatal(err)
	}
	content(t, filepath.Join(m.own, "note.txt"), "newer")
	content(t, filepath.Join(m.own, "note (restored).txt"), "note")
	if err = m.s.Restore(ctx, "editor", dir.ID, ""); err != nil {
		t.Fatal(err)
	}
	content(t, filepath.Join(m.own, "docs", "sub", "f.txt"), "deep")
	if err = m.s.Restore(ctx, "editor", dir.ID, ""); !errors.Is(err, ErrTrashNotFound) {
		t.Fatal(err)
	}
	gone, _ := m.s.Trash(ctx, "editor", "files", "docs")
	if err = m.s.Purge(ctx, "editor", gone.ID); err != nil {
		t.Fatal(err)
	}
	if list, _ = m.s.TrashList("editor", "files"); len(list) != 0 {
		t.Fatal(list)
	}
	if items, _ := os.ReadDir(filepath.Join(m.own, storage.MetaDir, "trash")); len(items) != 0 {
		t.Fatal("purged data remains", items)
	}
}

func TestTrashRecoveryAndRetention(t *testing.T) {
	m := setupSpaces(t)
	ctx := context.Background()
	m.s.SetPermissions("editor", map[string]Permission{AnySpace: AllPermissions})
	upload(t, m.s, "editor", "files", "a.txt", "a")
	upload(t, m.s, "editor", "files", "b.txt", "b")
	kept, _ := m.s.Trash(ctx, "editor", "files", "a.txt")
	// Crash after the record but before the move: a record without an item.
	ghost := TrashItem{ID: strings.Repeat("e", 64), Space: "files", Path: "ghost", Deleted: time.Now()}
	if err := m.s.putTrash(ghost); err != nil {
		t.Fatal(err)
	}
	// Crash after the move but before the record: an item without a record.
	orphan := "item-" + strings.Repeat("f", 64)
	if err := os.Rename(filepath.Join(m.own, "b.txt"), filepath.Join(m.own, storage.MetaDir, "trash", orphan)); err != nil {
		t.Fatal(err)
	}
	limits := m.s.limits
	if err := m.s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err := Open(m.state, map[string]string{"files": m.own, "nas": m.share}, limits, storage.DefaultModes())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	s.SetPermissions("editor", map[string]Permission{AnySpace: AllPermissions})
	list, err := s.TrashList("editor", "files")
	if err != nil || len(list) != 2 {
		t.Fatal(list, err)
	}
	var adopted TrashItem
	for _, it := range list {
		if it.ID == ghost.ID {
			t.Fatal("stale record kept")
		}
		if it.ID == strings.Repeat("f", 64) {
			adopted = it
		}
	}
	if adopted.ID == "" || adopted.Path != "" || adopted.Size != 1 {
		t.Fatal("orphan not adopted", list)
	}
	if err = s.Restore(ctx, "editor", adopted.ID, ""); !errors.Is(err, storage.ErrPath) {
		t.Fatal("restore without a known path", err)
	}
	if err = s.Restore(ctx, "editor", adopted.ID, "b-recovered.txt"); err != nil {
		t.Fatal(err)
	}
	later := time.Now().Add(limits.TrashRetention + time.Minute)
	s.now = func() time.Time { return later }
	if err = s.Expire(); err != nil {
		t.Fatal(err)
	}
	if list, _ = s.TrashList("editor", "files"); len(list) != 0 {
		t.Fatal("expired item kept", list, kept)
	}
}

func TestReadOnlySpaceAndStatePlacement(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root bypasses permission checks")
	}
	base := t.TempDir()
	state, ro, own := filepath.Join(base, "state"), filepath.Join(base, "ro"), filepath.Join(base, "files")
	for _, p := range []string{state, ro, own} {
		if err := os.Mkdir(p, 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(ro, "doc"), []byte("read me"), 0600); err != nil {
		t.Fatal(err)
	}
	os.Chmod(ro, 0500)
	t.Cleanup(func() { os.Chmod(ro, 0700) })
	limits := DefaultLimits()
	if _, err := Open(state, map[string]string{"files": state}, limits, storage.DefaultModes()); err == nil {
		t.Fatal("state inside a space accepted")
	}
	if _, err := Open(state, map[string]string{"Bad Name": own}, limits, storage.DefaultModes()); err == nil {
		t.Fatal("invalid space name accepted")
	}
	s, err := Open(state, map[string]string{"files": own, "archive": ro}, limits, storage.DefaultModes())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	s.SetPermissions("u", map[string]Permission{AnySpace: AllPermissions})
	ctx := context.Background()
	if _, err = s.Begin(ctx, "u", "archive", "x", 1); !errors.Is(err, storage.ErrReadOnly) {
		t.Fatal(err)
	}
	if _, err = s.Trash(ctx, "u", "archive", "doc"); !errors.Is(err, storage.ErrReadOnly) {
		t.Fatal(err)
	}
	f, err := s.Read(ctx, "u", "archive", "doc")
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	infos := s.Spaces()
	if len(infos) != 2 || infos[0].Name != "archive" || !infos[0].ReadOnly || infos[1].ReadOnly {
		t.Fatal(infos)
	}
}
