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
	bolt "go.etcd.io/bbolt"
)

func reopen(t *testing.T, s *Service, root, state string) *Service {
	t.Helper()
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	next, err := Open(state, map[string]string{"main": root}, s.limits, storage.DefaultModes())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { next.Close() })
	for _, user := range []string{"alice", "bob"} {
		if err = next.SetPermissions(user, all); err != nil {
			t.Fatal(err)
		}
	}
	return next
}

func TestResumeAfterRestart(t *testing.T) {
	s, root, state := setup(t)
	u := patch(t, s, "alice", begin(t, s, "alice", "new", 5), "hel")
	s = reopen(t, s, root, state)
	if _, err := s.Status("bob", u.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("foreign upload visible after restart", err)
	}
	got, err := s.Status("alice", u.ID)
	if err != nil || got.Offset != 3 || got.Size != 5 || got.State != StateUploading {
		t.Fatal(got, err)
	}
	if s.reserved != 5 {
		t.Fatal("quota not restored", s.reserved)
	}
	patch(t, s, "alice", got, "lo")
	if ok, err := s.Commit(context.Background(), "alice", u.ID); !ok || err != nil {
		t.Fatal(ok, err)
	}
	content(t, filepath.Join(root, "new"), "hello")
}

func TestRecoveryTruncatesUnacknowledgedBytes(t *testing.T) {
	s, root, state := setup(t)
	u := patch(t, s, "alice", begin(t, s, "alice", "new", 8), "ab")
	// Simulate a crash after writing a chunk but before recording its offset.
	f, err := os.OpenFile(filepath.Join(stagingDir(root), stagedName(u.ID)), os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString("XYZ")
	f.Close()
	s = reopen(t, s, root, state)
	content(t, filepath.Join(stagingDir(root), stagedName(u.ID)), "ab")
	got, err := s.Status("alice", u.ID)
	if err != nil || got.Offset != 2 {
		t.Fatal(got, err)
	}
	patch(t, s, "alice", got, "cdefgh")
	if ok, err := s.Commit(context.Background(), "alice", u.ID); !ok || err != nil {
		t.Fatal(ok, err)
	}
	content(t, filepath.Join(root, "new"), "abcdefgh")
}

func TestRecoveryDropsUploadWithMissingAcknowledgedBytes(t *testing.T) {
	s, root, state := setup(t)
	u := patch(t, s, "alice", begin(t, s, "alice", "new", 8), "abcd")
	if err := os.Truncate(filepath.Join(stagingDir(root), stagedName(u.ID)), 1); err != nil {
		t.Fatal(err)
	}
	s = reopen(t, s, root, state)
	if _, err := s.Status("alice", u.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	if len(staging(t, root)) != 0 || s.reserved != 0 {
		t.Fatal("untrusted upload kept")
	}
}

func TestCrashBeforeRenameRevertsToUploading(t *testing.T) {
	s, root, state := setup(t)
	u := patch(t, s, "alice", begin(t, s, "alice", "new", 2), "ok")
	intent := s.uploads[u.ID].record()
	intent.Info.State = statePublishing
	if err := s.put(intent); err != nil {
		t.Fatal(err)
	}
	s = reopen(t, s, root, state)
	got, err := s.Status("alice", u.ID)
	if err != nil || got.State != StateUploading || got.Offset != 2 {
		t.Fatal(got, err)
	}
	if _, err = os.Stat(filepath.Join(root, "new")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("published without commit", err)
	}
	if ok, err := s.Commit(context.Background(), "alice", u.ID); !ok || err != nil {
		t.Fatal(ok, err)
	}
	content(t, filepath.Join(root, "new"), "ok")
}

func TestCrashAfterRenameIsReportedAsPublished(t *testing.T) {
	s, root, state := setup(t)
	u := patch(t, s, "alice", begin(t, s, "alice", "new", 2), "ok")
	intent := s.uploads[u.ID].record()
	intent.Info.State = statePublishing
	if err := s.put(intent); err != nil {
		t.Fatal(err)
	}
	// Simulate the rename completing right before the process died.
	if err := os.Rename(filepath.Join(stagingDir(root), stagedName(u.ID)), filepath.Join(root, "new")); err != nil {
		t.Fatal(err)
	}
	s = reopen(t, s, root, state)
	got, err := s.Status("alice", u.ID)
	if err != nil || got.State != StatePublished || !got.Durable {
		t.Fatal(got, err)
	}
	if ok, err := s.Commit(context.Background(), "alice", u.ID); !ok || err != nil {
		t.Fatal(ok, err)
	}
	if err := s.Abort("alice", u.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("published upload can be aborted", err)
	}
	content(t, filepath.Join(root, "new"), "ok")
	if s.reserved != 0 || len(s.uploads) != 0 {
		t.Fatal("quota held by published upload")
	}
}

func TestPublicationResultSurvivesRestartAndExpires(t *testing.T) {
	s, root, state := setup(t)
	u := patch(t, s, "alice", begin(t, s, "alice", "new", 2), "ok")
	if ok, err := s.Commit(context.Background(), "alice", u.ID); !ok || err != nil {
		t.Fatal(ok, err)
	}
	s = reopen(t, s, root, state)
	if ok, err := s.Commit(context.Background(), "alice", u.ID); !ok || err != nil {
		t.Fatal(ok, err)
	}
	if _, err := s.Status("bob", u.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("foreign result visible", err)
	}
	later := time.Now().Add(ResultRetention + time.Minute)
	s.now = func() time.Time { return later }
	if err := s.Expire(); err != nil {
		t.Fatal(err)
	}
	if ok, err := s.Commit(context.Background(), "alice", u.ID); ok || !errors.Is(err, ErrNotFound) {
		t.Fatal(ok, err)
	}
	s = reopen(t, s, root, state)
	if len(s.results) != 0 {
		t.Fatal("expired result reloaded")
	}
}

func TestResultsAreBounded(t *testing.T) {
	s, _, _ := setup(t)
	limit := s.limits.MaxUploads * 8
	for i := 0; i < limit+3; i++ {
		u := begin(t, s, "alice", "f"+strings.Repeat("x", i%3)+string(rune('a'+i%26))+string(rune('a'+i/26)), 0)
		if ok, err := s.Commit(context.Background(), "alice", u.ID); !ok || err != nil {
			t.Fatal(ok, err)
		}
	}
	if len(s.results) != limit {
		t.Fatal(len(s.results))
	}
}

func TestRecoveryDiscardsOrphansCorruptAndExpiredRecords(t *testing.T) {
	s, root, state := setup(t)
	expired := patch(t, s, "alice", begin(t, s, "alice", "old", 2), "ok")
	keep := patch(t, s, "bob", begin(t, s, "bob", "keep", 2), "ok")
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	orphan := stagedName(strings.Repeat("ab", 32))
	if err := os.WriteFile(filepath.Join(stagingDir(root), orphan), []byte("junk"), 0600); err != nil {
		t.Fatal(err)
	}
	db, err := bolt.Open(filepath.Join(state, "uploads.db"), 0600, nil)
	if err != nil {
		t.Fatal(err)
	}
	err = db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(uploadsBucket)
		if e := b.Put([]byte("not-json"), []byte("{")); e != nil {
			return e
		}
		// A record trying to point outside the space or at another staging name.
		return b.Put([]byte(strings.Repeat("cd", 32)), []byte(`{"info":{"id":"`+expired.ID+`","size":1,"state":"uploading"},"owner":"alice","target":"../escape"}`))
	})
	db.Close()
	if err != nil {
		t.Fatal(err)
	}
	later := expired.Expires.Add(time.Second)
	next, err := openAt(root, state, s.limits, later)
	if err != nil {
		t.Fatal(err)
	}
	defer next.Close()
	if len(next.uploads) != 0 {
		t.Fatal("expired upload resumed", next.uploads)
	}
	if parts := staging(t, root); len(parts) != 0 {
		t.Fatal("orphan or expired staging kept", parts)
	}
	_ = keep
	count := 0
	next.db.View(func(tx *bolt.Tx) error { count = tx.Bucket(uploadsBucket).Stats().KeyN; return nil })
	if count != 0 {
		t.Fatal("stale records kept", count)
	}
}

// openAt opens a service whose clock starts at now (for expiry at startup).
func openAt(root, state string, limits Limits, now time.Time) (*Service, error) {
	saved := clock
	clock = func() time.Time { return now }
	defer func() { clock = saved }()
	return Open(state, map[string]string{"main": root}, limits, storage.DefaultModes())
}

func TestDatabaseSymlinkIsRejected(t *testing.T) {
	s, root, state := setup(t)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "elsewhere.db")
	if err := os.Rename(filepath.Join(state, "uploads.db"), outside); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(state, "uploads.db")); err != nil {
		t.Fatal(err)
	}
	if next, err := Open(state, map[string]string{"main": root}, s.limits, storage.DefaultModes()); err == nil {
		next.Close()
		t.Fatal("symlinked database accepted")
	}
}

func TestPatchIsDurableOnlyAfterRecord(t *testing.T) {
	s, root, state := setup(t)
	u := begin(t, s, "alice", "new", 4)
	if _, err := s.Patch(context.Background(), "alice", u.ID, 0, strings.NewReader("abcd")); err != nil {
		t.Fatal(err)
	}
	s = reopen(t, s, root, state)
	got, err := s.Status("alice", u.ID)
	if err != nil || got.Offset != 4 {
		t.Fatal(got, err)
	}
}
