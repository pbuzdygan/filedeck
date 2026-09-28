// Package core provides the application operations. Subjects must come from a
// trusted authentication boundary; accepting a client-supplied subject is unsafe.
package core

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"regexp"
	"sort"
	"sync"
	"time"

	"github.com/pbuzdygan/filedeck/internal/storage"
	bolt "go.etcd.io/bbolt"
	"golang.org/x/sys/unix"
)

var (
	ErrDenied     = errors.New("permission denied")
	ErrNotFound   = errors.New("upload not found")
	ErrOffset     = errors.New("upload offset conflict")
	ErrSize       = errors.New("invalid upload size or chunk limit exceeded")
	ErrQuota      = errors.New("upload quota exceeded")
	ErrIncomplete = errors.New("upload is incomplete")
	ErrClosed     = errors.New("service closed")
	ErrNoSpace    = errors.New("unknown space")
)

type Permission uint8

const (
	List Permission = 1 << iota
	Read
	Create
	// Modify allows rename, move to trash and restore from trash.
	Modify
)

// AllPermissions is every grantable bit.
const AllPermissions = List | Read | Create | Modify

// AnySpace as a grant key applies to every space, including ones added later.
const AnySpace = "*"

var spaceNames = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)

// ValidSpaceName reports whether name can identify a space.
func ValidSpaceName(name string) bool { return spaceNames.MatchString(name) }

// ValidGrants checks a per-space permission map (keys are space names or AnySpace).
func ValidGrants(g map[string]Permission) bool {
	if len(g) > 64 {
		return false
	}
	for k, p := range g {
		if (k != AnySpace && !ValidSpaceName(k)) || p&^AllPermissions != 0 {
			return false
		}
	}
	return true
}

type Limits struct {
	MaxFileBytes      int64
	MaxStagingBytes   int64
	MaxChunkBytes     int64
	MaxUploads        int
	MaxUploadsPerUser int
	TTL               time.Duration
	TrashRetention    time.Duration
}

func DefaultLimits() Limits {
	return Limits{MaxFileBytes: 1 << 30, MaxStagingBytes: 4 << 30, MaxChunkBytes: 8 << 20, MaxUploads: 32, MaxUploadsPerUser: 4, TTL: time.Hour, TrashRetention: 30 * 24 * time.Hour}
}
func (l Limits) valid() bool {
	return l.MaxFileBytes > 0 && l.MaxStagingBytes >= l.MaxFileBytes && l.MaxChunkBytes > 0 && l.MaxChunkBytes <= l.MaxFileBytes && l.MaxUploads > 0 && l.MaxUploadsPerUser > 0 && l.MaxUploadsPerUser <= l.MaxUploads && l.TTL > 0 && l.TrashRetention > 0
}

// ResultRetention is how long a publication result answers retried commits.
const ResultRetention = 24 * time.Hour

const (
	StateUploading  = "uploading"
	statePublishing = "publishing"
	StatePublished  = "published"
)

var uploadsBucket = []byte("uploads")
var trashBucket = []byte("trash")

// clock is the time source for new services; tests replace it.
var clock = time.Now

type Upload struct {
	ID      string    `json:"id"`
	Space   string    `json:"space"`
	Offset  int64     `json:"offset"`
	Size    int64     `json:"size"`
	Expires time.Time `json:"expires"`
	State   string    `json:"state"`
	Durable bool      `json:"durability_confirmed,omitempty"`
}

// record is the durable form of an upload. Offset is written only after the
// staged bytes up to it were synced, so recovery may truncate but never extend.
type record struct {
	Info   Upload `json:"info"`
	Owner  string `json:"owner"`
	Target string `json:"target"`
}

func stagedName(id string) string { return "upload-" + id + ".part" }

type transfer struct {
	mu                    sync.Mutex
	info                  Upload
	space                 *storage.Space
	spaceName             string // immutable copy of info.Space, readable without mu
	owner, target, staged string
	file                  *os.File
	terminal              bool
	failed                bool
}

func (u *transfer) record() record { return record{Info: u.info, Owner: u.owner, Target: u.target} }

// SpaceInfo describes a configured space.
type SpaceInfo struct {
	Name     string `json:"name"`
	ReadOnly bool   `json:"read_only"`
}

type Service struct {
	state    *storage.State
	spaces   map[string]*storage.Space
	db       *bolt.DB
	limits   Limits
	mu       sync.Mutex
	uploads  map[string]*transfer
	results  map[string]*record
	resultMu sync.Mutex
	trashMu  sync.Mutex
	reserved int64
	closed   bool
	policyMu sync.RWMutex
	grants   map[string]map[string]Permission
	jobs     jobs
	linkGate chan struct{}
	now      func() time.Time
}

// Open takes exclusive ownership of state, opens every space, resumes recorded
// uploads, reconciles the trash and starts with no user grants.
func Open(state string, spaces map[string]string, limits Limits, modes storage.Modes) (_ *Service, err error) {
	if !limits.valid() {
		return nil, ErrSize
	}
	if len(spaces) == 0 {
		return nil, errors.New("at least one space is required")
	}
	st, err := storage.OpenState(state)
	if err != nil {
		return nil, err
	}
	s := &Service{state: st, spaces: make(map[string]*storage.Space), limits: limits, uploads: make(map[string]*transfer), results: make(map[string]*record), grants: make(map[string]map[string]Permission), jobs: jobs{all: make(map[string]*job)}, linkGate: make(chan struct{}, 2), now: clock}
	defer func() {
		if err != nil {
			for _, u := range s.uploads {
				u.file.Close()
			}
			for _, sp := range s.spaces {
				sp.Close()
			}
			if s.db != nil {
				s.db.Close()
			}
			st.Close()
		}
	}()
	for name, dir := range spaces {
		if !ValidSpaceName(name) {
			return nil, errors.New("invalid space name: " + name)
		}
		if st.Overlaps(dir) {
			return nil, errors.New("state must not be inside a space or contain one: " + name)
		}
		sp, e := storage.OpenSpace(dir, modes)
		if e != nil {
			return nil, e
		}
		s.spaces[name] = sp
	}
	s.db, err = bolt.Open("uploads.db", 0600, &bolt.Options{Timeout: time.Second, OpenFile: func(name string, flags int, _ os.FileMode) (*os.File, error) {
		return st.OpenFile(name, flags)
	}})
	if err != nil {
		return nil, err
	}
	err = s.db.Update(func(tx *bolt.Tx) error {
		meta, e := tx.CreateBucketIfNotExists([]byte("schema"))
		if e != nil {
			return e
		}
		// v1 records had no space; they fail validation and are discarded.
		if v := meta.Get([]byte("version")); v != nil && string(v) != "1" && string(v) != "2" {
			return errors.New("unsupported uploads schema")
		}
		for _, b := range [][]byte{uploadsBucket, trashBucket, linksBucket} {
			if _, e = tx.CreateBucketIfNotExists(b); e != nil {
				return e
			}
		}
		return meta.Put([]byte("version"), []byte("2"))
	})
	if err != nil {
		return nil, err
	}
	if err = s.recover(); err != nil {
		return nil, err
	}
	if err = s.recoverTrash(); err != nil {
		return nil, err
	}
	return s, nil
}

// Spaces lists configured spaces in name order.
func (s *Service) Spaces() []SpaceInfo {
	out := make([]SpaceInfo, 0, len(s.spaces))
	for name, sp := range s.spaces {
		out = append(out, SpaceInfo{Name: name, ReadOnly: sp.ReadOnly()})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func (s *Service) space(name string) (*storage.Space, error) {
	sp, ok := s.spaces[name]
	if !ok {
		return nil, ErrNoSpace
	}
	return sp, nil
}

func put(db *bolt.DB, bucket []byte, key string, v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return db.Update(func(tx *bolt.Tx) error { return tx.Bucket(bucket).Put([]byte(key), data) })
}
func drop(db *bolt.DB, bucket []byte, ids ...string) error {
	if len(ids) == 0 {
		return nil
	}
	return db.Update(func(tx *bolt.Tx) error {
		for _, id := range ids {
			if e := tx.Bucket(bucket).Delete([]byte(id)); e != nil {
				return e
			}
		}
		return nil
	})
}
func (s *Service) put(r record) error       { return put(s.db, uploadsBucket, r.Info.ID, r) }
func (s *Service) drop(ids ...string) error { return drop(s.db, uploadsBucket, ids...) }

func (s *Service) plausible(id string, r record) bool {
	i := r.Info
	return i.ID == id && storage.StagingName(stagedName(id)) && r.Owner != "" && ValidSpaceName(i.Space) && storage.ValidPath(r.Target, false) &&
		i.Size >= 0 && i.Size <= s.limits.MaxFileBytes && i.Offset >= 0 && i.Offset <= i.Size
}

// recover reconciles records with staging files. The staging entry decides a
// crash during publication: present means not renamed, absent means renamed.
func (s *Service) recover() error {
	var ids []string
	var recs []record
	var stale []string
	err := s.db.View(func(tx *bolt.Tx) error {
		return tx.Bucket(uploadsBucket).ForEach(func(k, v []byte) error {
			var r record
			if json.Unmarshal(v, &r) != nil || !s.plausible(string(k), r) {
				stale = append(stale, string(k))
				return nil
			}
			ids = append(ids, string(k))
			recs = append(recs, r)
			return nil
		})
	})
	if err != nil {
		return err
	}
	now := s.now()
	keep := make(map[string]map[string]bool)
	for i, r := range recs {
		id := ids[i]
		if r.Info.State == StatePublished {
			if now.Before(r.Info.Expires) {
				s.results[id] = &r
			} else {
				stale = append(stale, id)
			}
			continue
		}
		sp, ok := s.spaces[r.Info.Space]
		if !ok || sp.ReadOnly() {
			// The space is gone or no longer writable; the upload cannot resume.
			stale = append(stale, id)
			continue
		}
		exists, e := sp.StagingExists(stagedName(id))
		if e != nil {
			return e
		}
		if !exists {
			if r.Info.State != statePublishing {
				stale = append(stale, id)
				continue
			}
			r.Info = Upload{ID: id, Space: r.Info.Space, Offset: r.Info.Size, Size: r.Info.Size, State: StatePublished, Durable: sp.SyncTarget(r.Target) == nil, Expires: now.Add(ResultRetention)}
			if e = s.put(r); e != nil {
				return e
			}
			s.results[id] = &r
			continue
		}
		if !now.Before(r.Info.Expires) {
			stale = append(stale, id)
			continue
		}
		f, e := sp.OpenStaging(stagedName(id))
		if e != nil {
			return e
		}
		st, e := f.Stat()
		if e == nil && st.Size() < r.Info.Offset {
			// Acknowledged bytes are missing; the upload cannot be trusted.
			f.Close()
			stale = append(stale, id)
			continue
		}
		if e == nil && st.Size() > r.Info.Offset {
			if e = f.Truncate(r.Info.Offset); e == nil {
				e = f.Sync()
			}
		}
		if e == nil && r.Info.State != StateUploading {
			r.Info.State = StateUploading
			e = s.put(r)
		}
		if e != nil {
			f.Close()
			return e
		}
		s.uploads[id] = &transfer{info: r.Info, space: sp, spaceName: r.Info.Space, owner: r.Owner, target: r.Target, staged: stagedName(id), file: f}
		s.reserved += r.Info.Size
		if keep[r.Info.Space] == nil {
			keep[r.Info.Space] = make(map[string]bool)
		}
		keep[r.Info.Space][stagedName(id)] = true
	}
	stale = append(stale, s.pruneResults(now)...)
	if err = s.drop(stale...); err != nil {
		return err
	}
	for name, sp := range s.spaces {
		if _, err = sp.Recover(keep[name]); err != nil {
			return err
		}
	}
	return nil
}

// pruneResults requires s.mu (or exclusive access) and returns ids to delete.
func (s *Service) pruneResults(now time.Time) []string {
	var ids []string
	for id, r := range s.results {
		if !now.Before(r.Info.Expires) {
			ids = append(ids, id)
			delete(s.results, id)
		}
	}
	if limit := s.limits.MaxUploads * 8; len(s.results) > limit {
		order := make([]string, 0, len(s.results))
		for id := range s.results {
			order = append(order, id)
		}
		sort.Slice(order, func(i, j int) bool { return s.results[order[i]].Info.Expires.Before(s.results[order[j]].Info.Expires) })
		for _, id := range order[:len(order)-limit] {
			ids = append(ids, id)
			delete(s.results, id)
		}
	}
	return ids
}

// finished returns the owner's publication result without revealing others'.
func (s *Service) finished(subject, id string) *record {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.results[id]
	if !ok || subject == "" || r.Owner != subject || !s.now().Before(r.Info.Expires) {
		return nil
	}
	return r
}

// confirm answers a retried commit and retries an unconfirmed durability sync.
func (s *Service) confirm(r *record) (bool, error) {
	s.resultMu.Lock()
	defer s.resultMu.Unlock()
	if r.Info.Durable {
		return true, nil
	}
	sp, err := s.space(r.Info.Space)
	if err != nil {
		return true, err
	}
	if err = sp.SyncTarget(r.Target); err != nil {
		return true, err
	}
	next := *r
	next.Info.Durable = true
	if err = s.put(next); err != nil {
		return true, err
	}
	s.mu.Lock()
	r.Info.Durable = true
	s.mu.Unlock()
	return true, nil
}

// SetPermissions is a trusted administration operation, not an HTTP handler.
// Keys are space names or AnySpace; a nil or empty map revokes everything.
func (s *Service) SetPermissions(subject string, grants map[string]Permission) error {
	if subject == "" || !ValidGrants(grants) {
		return ErrDenied
	}
	copied := make(map[string]Permission, len(grants))
	for k, p := range grants {
		if p != 0 {
			copied[k] = p
		}
	}
	s.policyMu.Lock()
	if len(copied) == 0 {
		delete(s.grants, subject)
	} else {
		s.grants[subject] = copied
	}
	s.policyMu.Unlock()
	// Revocation is permanent for public links: restoring access later must not
	// silently revive links created before.
	return s.pruneLinks(subject)
}

// grantedLocked requires policyMu.
func (s *Service) grantedLocked(subject, space string) Permission {
	g := s.grants[subject]
	return g[space] | g[AnySpace]
}
func (s *Service) allowed(subject, space string, p Permission) bool {
	s.policyMu.RLock()
	defer s.policyMu.RUnlock()
	_, ok := s.spaces[space]
	return ok && subject != "" && s.grantedLocked(subject, space)&p == p
}

// Effective returns the subject's permissions for every space where it has any.
func (s *Service) Effective(subject string) map[string]Permission {
	s.policyMu.RLock()
	defer s.policyMu.RUnlock()
	out := make(map[string]Permission)
	for name := range s.spaces {
		if p := s.grantedLocked(subject, name); p != 0 {
			out[name] = p
		}
	}
	return out
}

func (s *Service) List(ctx context.Context, subject, space, name string, limit int) ([]storage.Entry, error) {
	if !s.allowed(subject, space, List) {
		return nil, ErrDenied
	}
	return s.spaces[space].List(ctx, name, limit)
}
func (s *Service) Read(ctx context.Context, subject, space, name string) (*os.File, error) {
	if !s.allowed(subject, space, Read) {
		return nil, ErrDenied
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return s.spaces[space].Read(name)
}

func (s *Service) Begin(ctx context.Context, subject, space, target string, size int64) (Upload, error) {
	if !s.allowed(subject, space, Create) {
		return Upload{}, ErrDenied
	}
	if err := ctx.Err(); err != nil {
		return Upload{}, err
	}
	sp := s.spaces[space]
	if sp.ReadOnly() {
		return Upload{}, storage.ErrReadOnly
	}
	if !storage.ValidUserPath(target) {
		return Upload{}, storage.ErrPath
	}
	if size < 0 || size > s.limits.MaxFileBytes {
		return Upload{}, ErrSize
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return Upload{}, ErrClosed
	}
	count := 0
	for _, u := range s.uploads {
		if u.owner == subject {
			count++
		}
	}
	if len(s.uploads) >= s.limits.MaxUploads || count >= s.limits.MaxUploadsPerUser || size > s.limits.MaxStagingBytes-s.reserved {
		return Upload{}, ErrQuota
	}
	if err := sp.Reserve(size); err != nil {
		return Upload{}, err
	}
	staged, f, err := sp.CreateStaging()
	if err != nil {
		return Upload{}, err
	}
	// The staging name is a random identifier; the path never comes from clients.
	info := Upload{ID: staged[len("upload-") : len(staged)-len(".part")], Space: space, Size: size, Expires: s.now().Add(s.limits.TTL), State: StateUploading}
	u := &transfer{info: info, space: sp, spaceName: space, owner: subject, target: target, staged: staged, file: f}
	if err = s.put(u.record()); err != nil {
		return Upload{}, errors.Join(err, f.Close(), sp.RemoveStaging(staged))
	}
	s.uploads[info.ID] = u
	s.reserved += size
	return info, nil
}

// lookup does not disclose whether an ID belongs to another user.
func (s *Service) lookup(subject, id string) (*transfer, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, ErrClosed
	}
	u, ok := s.uploads[id]
	if !ok || subject == "" || u.owner != subject {
		return nil, ErrNotFound
	}
	return u, nil
}

// retire requires u.mu. Failed cleanup remains charged against quota until an
// explicit Abort/Expire retry or restart, rather than leaking unaccounted disk.
func (s *Service) retire(u *transfer) error {
	u.failed = true
	if err := u.space.RemoveStaging(u.staged); err != nil {
		return err
	}
	// A record without staging is discarded by recovery, so this order is safe.
	err := errors.Join(s.drop(u.info.ID), u.file.Close())
	u.terminal = true
	s.mu.Lock()
	delete(s.uploads, u.info.ID)
	s.reserved -= u.info.Size
	s.mu.Unlock()
	return err
}
func (s *Service) live(u *transfer) error {
	if u.terminal {
		return ErrNotFound
	}
	if u.failed || !s.now().Before(u.info.Expires) {
		return errors.Join(ErrNotFound, s.retire(u))
	}
	return nil
}

type contextReader struct {
	ctx context.Context
	r   io.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.r.Read(p)
}

// Patch serializes offset validation, bounded I/O and state change for an upload.
// The caller must provide a reader with I/O deadlines: Context cannot interrupt
// an arbitrary Reader blocked inside Read (e.g. an HTTP body without a timeout).
func (s *Service) Patch(ctx context.Context, subject, id string, offset int64, body io.Reader) (Upload, error) {
	if body == nil {
		return Upload{}, ErrSize
	}
	u, err := s.lookup(subject, id)
	if err != nil {
		return Upload{}, err
	}
	if !s.allowed(subject, u.spaceName, Create) {
		return Upload{}, ErrDenied
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	if err = s.live(u); err != nil {
		return Upload{}, err
	}
	if offset < 0 || offset != u.info.Offset {
		return Upload{}, ErrOffset
	}
	if err = ctx.Err(); err != nil {
		return Upload{}, err
	}
	allowance := min(s.limits.MaxChunkBytes, u.info.Size-offset)
	if _, err = u.file.Seek(offset, io.SeekStart); err != nil {
		return Upload{}, err
	}
	reader := contextReader{ctx: ctx, r: body}
	n, copyErr := io.Copy(u.file, io.LimitReader(reader, allowance))
	if copyErr == nil {
		// Probe for excess without writing it; quota is never exceeded even briefly.
		var probe [1]byte
		extra, e := io.ReadFull(reader, probe[:])
		if extra != 0 {
			copyErr = ErrSize
		} else if e != nil && !errors.Is(e, io.EOF) {
			copyErr = e
		}
	}
	if copyErr == nil {
		copyErr = ctx.Err()
	}
	if copyErr == nil {
		copyErr = u.file.Sync()
	}
	next := u.record()
	next.Info.Offset += n
	if copyErr == nil {
		copyErr = s.put(next)
	}
	if copyErr != nil {
		if rollbackErr := u.file.Truncate(offset); rollbackErr != nil {
			return Upload{}, errors.Join(copyErr, rollbackErr, s.retire(u))
		}
		return Upload{}, copyErr
	}
	u.info = next.Info
	return u.info, nil
}

// Commit returns published=true even when a post-publication fsync fails. The
// caller must never retry/undo publication blindly based only on err != nil.
// A retried commit of an already published upload returns its stored result.
func (s *Service) Commit(ctx context.Context, subject, id string) (bool, error) {
	published, err := s.commit(ctx, subject, id)
	if !published && errors.Is(err, ErrNotFound) {
		if r := s.finished(subject, id); r != nil {
			return s.confirm(r)
		}
	}
	return published, err
}
func (s *Service) commit(ctx context.Context, subject, id string) (published bool, err error) {
	u, err := s.lookup(subject, id)
	if err != nil {
		return false, err
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	if err = s.live(u); err != nil {
		return false, err
	}
	if err = ctx.Err(); err != nil {
		return false, err
	}
	if u.info.Offset != u.info.Size {
		return false, ErrIncomplete
	}
	stat, statErr := u.file.Stat()
	if statErr != nil {
		return false, statErr
	}
	if stat.Size() != u.info.Size {
		return false, ErrIncomplete
	}
	// Serialize publication against permission revocation, not the whole upload.
	s.policyMu.RLock()
	defer s.policyMu.RUnlock()
	if subject == "" || s.grantedLocked(subject, u.info.Space)&Create == 0 {
		return false, ErrDenied
	}
	// Apply the configured mode; SMB mounts may fix modes via mount options.
	if e := u.file.Chmod(u.space.FileMode()); e != nil && !errors.Is(e, unix.EPERM) && !errors.Is(e, unix.EOPNOTSUPP) {
		return false, e
	}
	if err = u.file.Sync(); err != nil {
		return false, err
	}
	intent := u.record()
	intent.Info.State = statePublishing
	if err = s.put(intent); err != nil {
		return false, err
	}
	published, err = u.space.Publish(u.staged, u.target)
	if !published {
		// If this revert is lost, recovery still sees staging and reverts it.
		return false, errors.Join(err, s.put(u.record()))
	}
	// Publish consumed the staging entry; never remove target during cleanup.
	u.terminal = true
	closeErr := u.file.Close()
	result := record{Owner: u.owner, Target: u.target, Info: Upload{ID: id, Space: u.info.Space, Offset: u.info.Size, Size: u.info.Size, State: StatePublished, Durable: err == nil, Expires: s.now().Add(ResultRetention)}}
	// If this write is lost, recovery infers publication from missing staging.
	recordErr := s.put(result)
	if recordErr != nil {
		result.Info.Durable = false
	}
	s.mu.Lock()
	delete(s.uploads, id)
	s.reserved -= u.info.Size
	s.results[id] = &result
	stale := s.pruneResults(s.now())
	s.mu.Unlock()
	return true, errors.Join(err, closeErr, recordErr, s.drop(stale...))
}

// Abort only removes owned staging, so remains allowed after Create is revoked.
func (s *Service) Abort(subject, id string) error {
	u, err := s.lookup(subject, id)
	if err != nil {
		return err
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.terminal {
		return ErrNotFound
	}
	return s.retire(u)
}

// Expire reclaims quota for abandoned transfers, drops old publication results
// purges trash items past retention and removes expired links. The server invokes it periodically.
func (s *Service) Expire() error {
	s.mu.Lock()
	items := make([]*transfer, 0, len(s.uploads))
	for _, u := range s.uploads {
		items = append(items, u)
	}
	s.mu.Unlock()
	var result error
	for _, u := range items {
		u.mu.Lock()
		if !u.terminal && !s.now().Before(u.info.Expires) {
			result = errors.Join(result, s.retire(u))
		}
		u.mu.Unlock()
	}
	s.mu.Lock()
	stale := s.pruneResults(s.now())
	s.mu.Unlock()
	return errors.Join(result, s.drop(stale...), s.expireTrash(), s.expireLinks())
}

// Close waits for in-flight operations and keeps recorded uploads for resume
// after restart. Returned read handles belong to callers and must be closed.
func (s *Service) Close() error {
	s.stopJobs()
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	items := make([]*transfer, 0, len(s.uploads))
	for _, u := range s.uploads {
		items = append(items, u)
	}
	s.mu.Unlock()
	var result error
	for _, u := range items {
		u.mu.Lock()
		if !u.terminal {
			u.terminal = true
			result = errors.Join(result, u.file.Close())
		}
		u.mu.Unlock()
	}
	s.trashMu.Lock()
	defer s.trashMu.Unlock()
	result = errors.Join(result, s.db.Close())
	for _, sp := range s.spaces {
		result = errors.Join(result, sp.Close())
	}
	return errors.Join(result, s.state.Close())
}

// Status returns the current offset to the owner, allowing a client to resume
// after a lost response or a restart, or the result of a finished publication.
func (s *Service) Status(subject, id string) (Upload, error) {
	u, err := s.lookup(subject, id)
	if err == nil {
		if !s.allowed(subject, u.spaceName, Create) {
			return Upload{}, ErrDenied
		}
		u.mu.Lock()
		err = s.live(u)
		info := u.info
		u.mu.Unlock()
		if err == nil {
			return info, nil
		}
	}
	if errors.Is(err, ErrNotFound) {
		if r := s.finished(subject, id); r != nil {
			s.mu.Lock()
			defer s.mu.Unlock()
			return r.Info, nil
		}
	}
	return Upload{}, err
}

// Mkdir requires Create and is serialized against permission revocation.
func (s *Service) Mkdir(ctx context.Context, subject, space, target string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.policyMu.RLock()
	defer s.policyMu.RUnlock()
	sp, ok := s.spaces[space]
	if !ok || subject == "" || s.grantedLocked(subject, space)&Create == 0 {
		return ErrDenied
	}
	return sp.Mkdir(target)
}
