package core

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

	"github.com/pbuzdygan/filedeck/internal/storage"
)

func setup(t *testing.T) (*Service, string, string) {
	t.Helper()
	base := t.TempDir()
	root := filepath.Join(base, "root")
	state := filepath.Join(base, "state")
	for _, p := range []string{root, state} {
		if err := os.Mkdir(p, 0700); err != nil {
			t.Fatal(err)
		}
	}
	limits := Limits{MaxFileBytes: 16, MaxStagingBytes: 32, MaxChunkBytes: 8, MaxUploads: 4, MaxUploadsPerUser: 2, TTL: time.Minute, TrashRetention: time.Hour}
	s, err := Open(state, map[string]string{"main": root}, limits, storage.DefaultModes())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Error(err)
		}
	})
	for _, user := range []string{"alice", "bob"} {
		if err = s.SetPermissions(user, all); err != nil {
			t.Fatal(err)
		}
	}
	return s, root, state
}
func begin(t *testing.T, s *Service, user, target string, size int64) Upload {
	t.Helper()
	u, err := s.Begin(context.Background(), user, "main", target, size)
	if err != nil {
		t.Fatal(err)
	}
	return u
}
func patch(t *testing.T, s *Service, user string, u Upload, data string) Upload {
	t.Helper()
	u, err := s.Patch(context.Background(), user, u.ID, u.Offset, strings.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	return u
}

var all = map[string]Permission{AnySpace: AllPermissions}

func stagingDir(root string) string { return filepath.Join(root, storage.MetaDir, "staging") }

// staging lists upload staging files of the space at root.
func staging(t *testing.T, root string) []string {
	t.Helper()
	entries, err := os.ReadDir(stagingDir(root))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		if storage.StagingName(e.Name()) {
			names = append(names, e.Name())
		}
	}
	return names
}
func content(t *testing.T, path, want string) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil || string(b) != want {
		t.Fatalf("%s: %q %v", path, b, err)
	}
}

func TestUploadPublishesOnlyAfterCompletion(t *testing.T) {
	s, root, _ := setup(t)
	ctx := context.Background()
	if err := os.Mkdir(filepath.Join(root, "folder"), 0700); err != nil {
		t.Fatal(err)
	}
	u := begin(t, s, "alice", "folder/new", 5)
	u = patch(t, s, "alice", u, "hel")
	if _, err := os.Stat(filepath.Join(root, "folder/new")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("partial target exists", err)
	}
	if ok, err := s.Commit(ctx, "alice", u.ID); ok || !errors.Is(err, ErrIncomplete) {
		t.Fatal(ok, err)
	}
	u = patch(t, s, "alice", u, "lo")
	ok, err := s.Commit(ctx, "alice", u.ID)
	if !ok || err != nil {
		t.Fatal(ok, err)
	}
	content(t, filepath.Join(root, "folder/new"), "hello")
	if parts := staging(t, root); len(parts) != 0 {
		t.Fatal(parts)
	}
	// A retried commit (e.g. lost response) reports the stored result.
	if ok, err = s.Commit(ctx, "alice", u.ID); !ok || err != nil {
		t.Fatal(ok, err)
	}
	if ok, err = s.Commit(ctx, "bob", u.ID); ok || !errors.Is(err, ErrNotFound) {
		t.Fatal(ok, err)
	}
}

func TestFailedUploadCannotDeleteOrTruncateDestination(t *testing.T) {
	s, root, _ := setup(t)
	ctx := context.Background()
	if err := os.Mkdir(filepath.Join(root, "dir"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "dir/keep"), []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"dir", "dir/keep"} {
		u := begin(t, s, "alice", target, 3)
		u = patch(t, s, "alice", u, "new")
		if ok, err := s.Commit(ctx, "alice", u.ID); ok || !errors.Is(err, storage.ErrConflict) {
			t.Fatal(ok, err)
		}
		if err := s.Abort("alice", u.ID); err != nil {
			t.Fatal(err)
		}
		content(t, filepath.Join(root, "dir/keep"), "original")
	}
	if _, err := s.Begin(ctx, "alice", "main", "dir/keep", -1); !errors.Is(err, ErrSize) {
		t.Fatal(err)
	}
	content(t, filepath.Join(root, "dir/keep"), "original")
}

type brokenReader struct{}

func (brokenReader) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }
func TestFailedAndOversizedChunksRollBack(t *testing.T) {
	for name, body := range map[string]io.Reader{
		"over-declared": strings.NewReader("1234567"),
		"io-failure":    io.MultiReader(strings.NewReader("xy"), brokenReader{}),
	} {
		t.Run(name, func(t *testing.T) {
			s, root, _ := setup(t)
			u := begin(t, s, "alice", "new", 8)
			u = patch(t, s, "alice", u, "ab")
			if _, err := s.Patch(context.Background(), "alice", u.ID, 2, body); err == nil {
				t.Fatal("bad chunk accepted")
			}
			parts := staging(t, root)
			if len(parts) != 1 {
				t.Fatal(parts)
			}
			content(t, filepath.Join(stagingDir(root), parts[0]), "ab")
			u = patch(t, s, "alice", u, "cdefgh")
			if ok, err := s.Commit(context.Background(), "alice", u.ID); !ok || err != nil {
				t.Fatal(ok, err)
			}
			content(t, filepath.Join(root, "new"), "abcdefgh")
		})
	}
}
func TestChunkCapIndependentOfFileSize(t *testing.T) {
	s, root, _ := setup(t)
	u := begin(t, s, "alice", "new", 16)
	if _, err := s.Patch(context.Background(), "alice", u.ID, 0, strings.NewReader("123456789")); !errors.Is(err, ErrSize) {
		t.Fatal(err)
	}
	content(t, filepath.Join(stagingDir(root), staging(t, root)[0]), "")
}

func TestOwnershipAndRevocation(t *testing.T) {
	s, root, _ := setup(t)
	ctx := context.Background()
	u := begin(t, s, "alice", "new", 1)
	if _, err := s.Patch(ctx, "bob", u.ID, 0, strings.NewReader("x")); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	if _, err := s.Commit(ctx, "bob", u.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	if err := s.Abort("bob", u.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	u = patch(t, s, "alice", u, "x")
	s.SetPermissions("alice", map[string]Permission{AnySpace: List})
	if ok, err := s.Commit(ctx, "alice", u.ID); ok || !errors.Is(err, ErrDenied) {
		t.Fatal(ok, err)
	}
	if _, err := s.Read(ctx, "alice", "main", "new"); !errors.Is(err, ErrDenied) {
		t.Fatal(err)
	}
	if _, err := s.Begin(ctx, "anonymous", "main", "new", 0); !errors.Is(err, ErrDenied) {
		t.Fatal(err)
	}
	if err := s.Abort("alice", u.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "new")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
}

type gatedReader struct {
	entered, release chan struct{}
	r                io.Reader
	once             sync.Once
}

func (r *gatedReader) Read(p []byte) (int, error) {
	r.once.Do(func() { close(r.entered); <-r.release })
	return r.r.Read(p)
}
func TestConcurrentPatchAtSameOffset(t *testing.T) {
	s, root, _ := setup(t)
	ctx := context.Background()
	u := begin(t, s, "alice", "new", 4)
	gate := &gatedReader{entered: make(chan struct{}), release: make(chan struct{}), r: strings.NewReader("data")}
	first := make(chan error, 1)
	second := make(chan error, 1)
	go func() { _, err := s.Patch(ctx, "alice", u.ID, 0, gate); first <- err }()
	<-gate.entered
	go func() { _, err := s.Patch(ctx, "alice", u.ID, 0, strings.NewReader("evil")); second <- err }()
	close(gate.release)
	if err := <-first; err != nil {
		t.Fatal(err)
	}
	if err := <-second; !errors.Is(err, ErrOffset) {
		t.Fatal(err)
	}
	if ok, err := s.Commit(ctx, "alice", u.ID); !ok || err != nil {
		t.Fatal(ok, err)
	}
	content(t, filepath.Join(root, "new"), "data")
}

func TestConcurrentCommitsNeverOverwrite(t *testing.T) {
	s, root, _ := setup(t)
	ctx := context.Background()
	a := patch(t, s, "alice", begin(t, s, "alice", "same", 4), "aaaa")
	b := patch(t, s, "bob", begin(t, s, "bob", "same", 4), "bbbb")
	type result struct {
		ok   bool
		err  error
		data string
	}
	results := make(chan result, 2)
	start := make(chan struct{})
	for _, in := range []struct {
		user string
		u    Upload
		data string
	}{{"alice", a, "aaaa"}, {"bob", b, "bbbb"}} {
		go func() { <-start; ok, err := s.Commit(ctx, in.user, in.u.ID); results <- result{ok, err, in.data} }()
	}
	close(start)
	success := 0
	for i := 0; i < 2; i++ {
		r := <-results
		if r.ok {
			success++
			if r.err != nil {
				t.Fatal(r.err)
			}
			content(t, filepath.Join(root, "same"), r.data)
		} else if !errors.Is(r.err, storage.ErrConflict) {
			t.Fatal(r.err)
		}
	}
	if success != 1 {
		t.Fatal(success)
	}
}

func TestQuotasExpiryAndAbort(t *testing.T) {
	s, _, _ := setup(t)
	ctx := context.Background()
	a := begin(t, s, "alice", "a", 16)
	begin(t, s, "bob", "b", 16)
	if _, err := s.Begin(ctx, "alice", "main", "c", 1); !errors.Is(err, ErrQuota) {
		t.Fatal(err)
	}
	if err := s.Abort("alice", a.ID); err != nil {
		t.Fatal(err)
	}
	begin(t, s, "alice", "c", 0)
	begin(t, s, "alice", "d", 0)
	if _, err := s.Begin(ctx, "alice", "main", "e", 0); !errors.Is(err, ErrQuota) {
		t.Fatal(err)
	}
	now := time.Now().Add(2 * time.Minute)
	s.now = func() time.Time { return now }
	if err := s.Expire(); err != nil {
		t.Fatal(err)
	}
	if len(s.uploads) != 0 || s.reserved != 0 {
		t.Fatal("quota leaked")
	}
	begin(t, s, "alice", "fresh", 16)
}

func TestCancellationRollsBack(t *testing.T) {
	s, root, _ := setup(t)
	u := begin(t, s, "alice", "new", 4)
	ctx, cancel := context.WithCancel(context.Background())
	body := &cancelReader{cancel: cancel}
	if _, err := s.Patch(ctx, "alice", u.ID, 0, body); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	content(t, filepath.Join(stagingDir(root), staging(t, root)[0]), "")
}

type cancelReader struct{ cancel context.CancelFunc }

func (r *cancelReader) Read(p []byte) (int, error) { copy(p, "data"); r.cancel(); return 4, nil }

func TestCommitRejectsSymlinkParentAndAllowsEmptyFile(t *testing.T) {
	s, root, _ := setup(t)
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	u := begin(t, s, "alice", "link/escape", 0)
	if ok, err := s.Commit(context.Background(), "alice", u.ID); ok || err == nil {
		t.Fatal(ok, err)
	}
	if _, err := os.Stat(filepath.Join(outside, "escape")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	u = begin(t, s, "alice", "empty", 0)
	if ok, err := s.Commit(context.Background(), "alice", u.ID); !ok || err != nil {
		t.Fatal(ok, err)
	}
	content(t, filepath.Join(root, "empty"), "")
}

func TestCommitChecksActualStagingLength(t *testing.T) {
	s, root, _ := setup(t)
	u := patch(t, s, "alice", begin(t, s, "alice", "new", 4), "data")
	// Simulate a storage inconsistency, not a permitted remote operation.
	if _, err := s.uploads[u.ID].file.WriteAt([]byte("extra"), 4); err != nil {
		t.Fatal(err)
	}
	if ok, err := s.Commit(context.Background(), "alice", u.ID); ok || !errors.Is(err, ErrIncomplete) {
		t.Fatal(ok, err)
	}
	if _, err := os.Stat(filepath.Join(root, "new")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
}

func TestExpiredCommitCleansOnlyStaging(t *testing.T) {
	s, root, _ := setup(t)
	u := patch(t, s, "alice", begin(t, s, "alice", "new", 4), "data")
	now := u.Expires
	s.now = func() time.Time { return now }
	if ok, err := s.Commit(context.Background(), "alice", u.ID); ok || !errors.Is(err, ErrNotFound) {
		t.Fatal(ok, err)
	}
	if _, err := os.Stat(filepath.Join(root, "new")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	if len(staging(t, root)) != 0 || s.reserved != 0 {
		t.Fatal("expired staging or quota remains")
	}
}

func TestMkdirIsBoundedAndNeverReplaces(t *testing.T) {
	s, root, _ := setup(t)
	ctx := context.Background()
	if err := s.Mkdir(ctx, "alice", "main", "docs"); err != nil {
		t.Fatal(err)
	}
	if err := s.Mkdir(ctx, "alice", "main", "docs"); !errors.Is(err, storage.ErrConflict) {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "file"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := s.Mkdir(ctx, "alice", "main", "file"); !errors.Is(err, storage.ErrConflict) {
		t.Fatal(err)
	}
	if err := s.Mkdir(ctx, "alice", "main", "a/b"); err == nil {
		t.Fatal("created intermediate directories")
	}
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	if err := s.Mkdir(ctx, "alice", "main", "link/escape"); err == nil {
		t.Fatal("followed symlink")
	}
	if _, err := os.Stat(filepath.Join(outside, "escape")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	for _, bad := range []string{"../x", ".", "/abs", "docs/../x"} {
		if err := s.Mkdir(ctx, "alice", "main", bad); !errors.Is(err, storage.ErrPath) {
			t.Fatal(bad, err)
		}
	}
	s.SetPermissions("bob", map[string]Permission{AnySpace: List | Read})
	if err := s.Mkdir(ctx, "bob", "main", "bobdir"); !errors.Is(err, ErrDenied) {
		t.Fatal(err)
	}
	if st, err := os.Stat(filepath.Join(root, "docs")); err != nil || !st.IsDir() {
		t.Fatal(st, err)
	}
}
