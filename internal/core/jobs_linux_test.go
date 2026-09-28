package core

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/pbuzdygan/filedeck/internal/storage"
)

func waitJob(t *testing.T, s *Service, user, id string) Job {
	t.Helper()
	for i := 0; i < 200; i++ {
		j, err := s.Transfer(user, id)
		if err != nil {
			t.Fatal(err)
		}
		if j.State != JobRunning {
			return j
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("transfer did not finish")
	return Job{}
}

func TestCopyAndMoveBetweenSpaces(t *testing.T) {
	m := setupSpaces(t)
	m.s.SetPermissions("editor", map[string]Permission{AnySpace: AllPermissions})
	m.s.SetPermissions("viewer", map[string]Permission{"files": List | Read, "nas": AllPermissions})
	os.MkdirAll(filepath.Join(m.own, "album", "2024"), 0700)
	os.WriteFile(filepath.Join(m.own, "album", "2024", "a.jpg"), []byte("jpeg"), 0600)
	os.WriteFile(filepath.Join(m.own, "doc.txt"), []byte("doc"), 0600)

	j, err := m.s.StartTransfer("editor", KindCopy, Endpoint{"files", "album"}, Endpoint{"nas", "album"})
	if err != nil {
		t.Fatal(err)
	}
	if j = waitJob(t, m.s, "editor", j.ID); j.State != JobDone || j.Files != 1 || j.Bytes != 4 {
		t.Fatalf("%+v %v", j, j.Error)
	}
	content(t, filepath.Join(m.share, "album", "2024", "a.jpg"), "jpeg")
	content(t, filepath.Join(m.own, "album", "2024", "a.jpg"), "jpeg")
	// Copy onto an existing name fails and leaves both sides intact.
	j, _ = m.s.StartTransfer("editor", KindCopy, Endpoint{"files", "doc.txt"}, Endpoint{"nas", "album"})
	if j = waitJob(t, m.s, "editor", j.ID); j.State != JobFailed || !errors.Is(j.Error, storage.ErrConflict) {
		t.Fatalf("%+v %v", j, j.Error)
	}
	// Move across spaces: the source ends up in its trash, restorable.
	j, _ = m.s.StartTransfer("editor", KindMove, Endpoint{"files", "doc.txt"}, Endpoint{"nas", "doc.txt"})
	if j = waitJob(t, m.s, "editor", j.ID); j.State != JobDone {
		t.Fatalf("%+v %v", j, j.Error)
	}
	content(t, filepath.Join(m.share, "doc.txt"), "doc")
	if _, err = os.Stat(filepath.Join(m.own, "doc.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("source kept after move")
	}
	if items, _ := m.s.TrashList("editor", "files"); len(items) != 1 || items[0].Path != "doc.txt" {
		t.Fatal(items)
	}
	// Move within a space is a rename, finished immediately.
	if j, err = m.s.StartTransfer("editor", KindMove, Endpoint{"nas", "doc.txt"}, Endpoint{"nas", "album/doc.txt"}); err != nil || j.State != JobDone {
		t.Fatal(j, err)
	}
	// Permissions: moving needs Modify on the source, copying needs Create on the target.
	if _, err = m.s.StartTransfer("viewer", KindMove, Endpoint{"files", "album"}, Endpoint{"nas", "x"}); !errors.Is(err, ErrDenied) {
		t.Fatal(err)
	}
	if _, err = m.s.StartTransfer("viewer", KindCopy, Endpoint{"nas", "album"}, Endpoint{"files", "x"}); !errors.Is(err, ErrDenied) {
		t.Fatal(err)
	}
	if j, err = m.s.StartTransfer("viewer", KindCopy, Endpoint{"files", "album"}, Endpoint{"nas", "album-copy"}); err != nil {
		t.Fatal(err)
	}
	waitJob(t, m.s, "viewer", j.ID)
	if _, err = m.s.Transfer("editor", j.ID); !errors.Is(err, ErrJobNotFound) {
		t.Fatal("foreign job visible", err)
	}
	for _, bad := range []Endpoint{{"files", "../x"}, {"files", ".filedeck/trash"}, {"unknown", "a"}} {
		if _, err = m.s.StartTransfer("editor", KindCopy, bad, Endpoint{"nas", "y"}); err == nil {
			t.Error("accepted source", bad)
		}
		if _, err = m.s.StartTransfer("editor", KindCopy, Endpoint{"files", "album"}, bad); err == nil {
			t.Error("accepted destination", bad)
		}
	}
	if len(m.s.Transfers("editor")) < 3 {
		t.Fatal(m.s.Transfers("editor"))
	}
}

func TestMultiItemTransfer(t *testing.T) {
	m := setupSpaces(t)
	m.s.SetPermissions("editor", map[string]Permission{AnySpace: AllPermissions})
	for _, n := range []string{"a.txt", "b.txt", "c.txt"} {
		os.WriteFile(filepath.Join(m.own, n), []byte(n), 0600)
	}
	pairs := []Pair{{Endpoint{"files", "a.txt"}, Endpoint{"nas", "a.txt"}}, {Endpoint{"files", "b.txt"}, Endpoint{"nas", "b.txt"}}}
	j, err := m.s.StartTransfers("editor", KindMove, pairs)
	if err != nil {
		t.Fatal(err)
	}
	if j = waitJob(t, m.s, "editor", j.ID); j.State != JobDone || j.Done != 2 || j.Items != 2 || j.Bytes != 10 {
		t.Fatalf("%+v %v", j, j.Error)
	}
	content(t, filepath.Join(m.share, "b.txt"), "b.txt")
	// The second item conflicts: the job stops there and reports progress.
	os.WriteFile(filepath.Join(m.share, "c-taken.txt"), []byte("x"), 0600)
	pairs = []Pair{{Endpoint{"files", "c.txt"}, Endpoint{"nas", "c1.txt"}}, {Endpoint{"files", "c.txt"}, Endpoint{"nas", "c-taken.txt"}}}
	j, _ = m.s.StartTransfers("editor", KindCopy, pairs)
	if j = waitJob(t, m.s, "editor", j.ID); j.State != JobFailed || j.Done != 1 || !errors.Is(j.Error, storage.ErrConflict) {
		t.Fatalf("%+v %v", j, j.Error)
	}
	// Same-space moves only: synchronous renames.
	os.WriteFile(filepath.Join(m.own, "d.txt"), nil, 0600)
	os.Mkdir(filepath.Join(m.own, "dir"), 0700)
	if j, err = m.s.StartTransfers("editor", KindMove, []Pair{{Endpoint{"files", "c.txt"}, Endpoint{"files", "dir/c.txt"}}, {Endpoint{"files", "d.txt"}, Endpoint{"files", "dir/d.txt"}}}); err != nil || j.State != JobDone || j.Done != 2 {
		t.Fatal(j, err)
	}
	if _, err = m.s.StartTransfers("editor", KindCopy, nil); err == nil {
		t.Fatal("empty job accepted")
	}
	big := make([]Pair, MaxTransferItems+1)
	if _, err = m.s.StartTransfers("editor", KindCopy, big); err == nil {
		t.Fatal("oversized job accepted")
	}
}
