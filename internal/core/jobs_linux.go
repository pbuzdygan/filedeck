package core

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pbuzdygan/filedeck/internal/storage"
)

// Transfer kinds.
const (
	KindCopy = "copy"
	KindMove = "move"
)

// Job states.
const (
	JobRunning  = "running"
	JobDone     = "done"
	JobFailed   = "failed"
	JobCanceled = "canceled"
)

var (
	ErrJobNotFound = errors.New("transfer not found")
	ErrJobLimit    = errors.New("too many running transfers")
)

// Endpoint is one side of a transfer.
type Endpoint struct {
	Space string `json:"space"`
	Path  string `json:"path"`
}

// Pair is one source and destination of a transfer.
type Pair struct {
	From Endpoint `json:"from"`
	To   Endpoint `json:"to"`
}

// MaxTransferItems bounds the number of pairs in one transfer.
const MaxTransferItems = 1000

// Job is a copy or move of one or more items running in the background. Jobs
// live in memory only: an interrupted copy is discarded at the next start
// (see Space.Recover). From/To describe the first item.
type Job struct {
	ID       string    `json:"id"`
	Kind     string    `json:"kind"`
	From     Endpoint  `json:"from"`
	To       Endpoint  `json:"to"`
	Items    int       `json:"items"`
	Done     int       `json:"done"`
	State    string    `json:"state"`
	Error    error     `json:"-"`
	Files    int64     `json:"files"`
	Bytes    int64     `json:"bytes"`
	Skipped  int64     `json:"skipped"`
	Started  time.Time `json:"started"`
	Finished time.Time `json:"finished,omitzero"`
}

type job struct {
	mu     sync.Mutex
	info   Job
	pairs  []Pair
	owner  string
	cancel context.CancelFunc
	files  atomic.Int64
	bytes  atomic.Int64
	done   atomic.Int64
}

func (j *job) snapshot() Job {
	j.mu.Lock()
	defer j.mu.Unlock()
	out := j.info
	if out.State == JobRunning {
		out.Files, out.Bytes, out.Done = j.files.Load(), j.bytes.Load(), int(j.done.Load())
	}
	return out
}

type jobs struct {
	mu   sync.Mutex
	all  map[string]*job
	wg   sync.WaitGroup
	stop bool
}

const (
	maxJobsPerUser = 2
	maxJobs        = 4
	jobRetention   = time.Hour
)

// CopyLimits bound every transfer.
var CopyLimits = storage.CopyLimits{MaxBytes: 256 << 30, MaxEntries: 200000}

// StartTransfer starts a single copy or move; see StartTransfers.
func (s *Service) StartTransfer(subject, kind string, from, to Endpoint) (Job, error) {
	return s.StartTransfers(subject, kind, []Pair{{From: from, To: to}})
}

// StartTransfers validates every item and starts a copy or move job. Moves
// within one space are renames (done synchronously when the whole job is such
// moves). Across spaces a move copies, publishes and then moves the source to
// its space's trash, so no step can lose data. Requires Read on each source
// (plus Modify for a move) and Create on each destination; permissions are
// checked again right before each publication. Items run in order; the first
// failure stops the job, reporting how many items were done.
func (s *Service) StartTransfers(subject, kind string, pairs []Pair) (Job, error) {
	if kind != KindCopy && kind != KindMove {
		return Job{}, storage.ErrPath
	}
	if len(pairs) == 0 || len(pairs) > MaxTransferItems {
		return Job{}, storage.ErrPath
	}
	need := Read
	if kind == KindMove {
		need |= Modify
	}
	renamesOnly := kind == KindMove
	for _, p := range pairs {
		if !storage.ValidUserPath(p.From.Path) || !storage.ValidUserPath(p.To.Path) {
			return Job{}, storage.ErrPath
		}
		if !s.allowed(subject, p.From.Space, need) || !s.allowed(subject, p.To.Space, Create) {
			return Job{}, ErrDenied
		}
		if s.spaces[p.To.Space].ReadOnly() || (kind == KindMove && s.spaces[p.From.Space].ReadOnly()) {
			return Job{}, storage.ErrReadOnly
		}
		if _, err := s.spaces[p.From.Space].Stat(p.From.Path); err != nil {
			return Job{}, err
		}
		renamesOnly = renamesOnly && p.From.Space == p.To.Space
	}
	now := s.now()
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return Job{}, err
	}
	info := Job{ID: hex.EncodeToString(raw[:]), Kind: kind, From: pairs[0].From, To: pairs[0].To, Items: len(pairs), State: JobRunning, Started: now}
	if renamesOnly {
		for i, p := range pairs {
			if err := s.Rename(context.Background(), subject, p.From.Space, p.From.Path, p.To.Path); err != nil {
				if i == 0 {
					return Job{}, err
				}
				info.State, info.Error = JobFailed, err
				break
			}
			info.Done = i + 1
		}
		if info.State == JobRunning {
			info.State = JobDone
		}
		info.Finished = s.now()
		s.jobs.mu.Lock()
		s.jobs.all[info.ID] = &job{info: info, owner: subject}
		s.jobs.mu.Unlock()
		return info, nil
	}
	s.jobs.mu.Lock()
	defer s.jobs.mu.Unlock()
	if s.jobs.stop {
		return Job{}, ErrClosed
	}
	s.pruneJobsLocked(now)
	running, mine := 0, 0
	for _, j := range s.jobs.all {
		if j.snapshot().State == JobRunning {
			running++
			if j.owner == subject {
				mine++
			}
		}
	}
	if running >= maxJobs || mine >= maxJobsPerUser {
		return Job{}, ErrJobLimit
	}
	ctx, cancel := context.WithCancel(context.Background())
	j := &job{info: info, pairs: append([]Pair(nil), pairs...), owner: subject, cancel: cancel}
	s.jobs.all[info.ID] = j
	s.jobs.wg.Add(1)
	go func() {
		defer s.jobs.wg.Done()
		defer cancel()
		var total storage.CopyResult
		var err error
		for _, p := range j.pairs {
			var res storage.CopyResult
			res, err = s.runItem(ctx, j, subject, kind, p, total)
			total.Files += res.Files
			total.Bytes += res.Bytes
			total.Skipped += res.Skipped
			if err != nil {
				break
			}
			j.done.Add(1)
		}
		j.mu.Lock()
		j.info.Files, j.info.Bytes, j.info.Skipped, j.info.Done = total.Files, total.Bytes, total.Skipped, int(j.done.Load())
		j.info.Finished = s.now()
		switch {
		case err == nil:
			j.info.State = JobDone
		case errors.Is(err, context.Canceled):
			j.info.State = JobCanceled
		default:
			j.info.State, j.info.Error = JobFailed, err
		}
		j.mu.Unlock()
	}()
	return info, nil
}

// runItem transfers one pair; before holds totals of earlier items for progress.
func (s *Service) runItem(ctx context.Context, j *job, subject, kind string, p Pair, before storage.CopyResult) (storage.CopyResult, error) {
	if err := ctx.Err(); err != nil {
		return storage.CopyResult{}, err
	}
	if kind == KindMove && p.From.Space == p.To.Space {
		return storage.CopyResult{}, s.Rename(ctx, subject, p.From.Space, p.From.Path, p.To.Path)
	}
	src, dst := s.spaces[p.From.Space], s.spaces[p.To.Space]
	staged, res, err := storage.CopyToStaging(ctx, src, p.From.Path, dst, CopyLimits, func(files, bytes int64) {
		j.files.Store(before.Files + files)
		j.bytes.Store(before.Bytes + bytes)
	})
	if err != nil {
		return res, err
	}
	// Publication and the source move are serialized with revocation, and the
	// permissions are re-checked: a user disabled meanwhile publishes nothing.
	s.trashMu.Lock()
	defer s.trashMu.Unlock()
	s.policyMu.RLock()
	defer s.policyMu.RUnlock()
	need := Read
	if kind == KindMove {
		need |= Modify
	}
	if s.grantedLocked(subject, p.From.Space)&need != need || s.grantedLocked(subject, p.To.Space)&Create == 0 {
		return res, errors.Join(ErrDenied, dst.RemoveCopy(staged))
	}
	if err = ctx.Err(); err != nil {
		return res, errors.Join(err, dst.RemoveCopy(staged))
	}
	published, err := dst.Publish(staged, p.To.Path)
	if !published {
		return res, errors.Join(err, dst.RemoveCopy(staged))
	}
	if err != nil || kind != KindMove {
		return res, err
	}
	var raw [32]byte
	if _, err = rand.Read(raw[:]); err != nil {
		return res, err
	}
	entry, _ := src.Stat(p.From.Path)
	t := TrashItem{ID: hex.EncodeToString(raw[:]), Space: p.From.Space, Path: p.From.Path, Directory: entry.Directory, Size: entry.Size, Deleted: s.now(), DeletedBy: subject}
	if err = s.putTrash(t); err != nil {
		return res, err
	}
	if _, err = src.Trash(p.From.Path, itemName(t.ID)); err != nil {
		return res, errors.Join(err, s.dropTrash(t.ID))
	}
	return res, nil
}

// pruneJobsLocked drops finished jobs past retention; requires jobs.mu.
func (s *Service) pruneJobsLocked(now time.Time) {
	for id, j := range s.jobs.all {
		info := j.snapshot()
		if info.State != JobRunning && now.Sub(info.Finished) > jobRetention {
			delete(s.jobs.all, id)
		}
	}
}

func (s *Service) ownJob(subject, id string) (*job, error) {
	s.jobs.mu.Lock()
	defer s.jobs.mu.Unlock()
	j, ok := s.jobs.all[id]
	if !ok || subject == "" || j.owner != subject {
		return nil, ErrJobNotFound
	}
	return j, nil
}

// Transfers lists the subject's transfers, newest first.
func (s *Service) Transfers(subject string) []Job {
	s.jobs.mu.Lock()
	s.pruneJobsLocked(s.now())
	var out []Job
	for _, j := range s.jobs.all {
		if j.owner == subject {
			out = append(out, j.snapshot())
		}
	}
	s.jobs.mu.Unlock()
	sort.Slice(out, func(a, b int) bool { return out[a].Started.After(out[b].Started) })
	if out == nil {
		out = []Job{}
	}
	return out
}

// Transfer returns one of the subject's transfers.
func (s *Service) Transfer(subject, id string) (Job, error) {
	j, err := s.ownJob(subject, id)
	if err != nil {
		return Job{}, err
	}
	return j.snapshot(), nil
}

// CancelTransfer stops a running transfer; its partial copy is removed.
func (s *Service) CancelTransfer(subject, id string) error {
	j, err := s.ownJob(subject, id)
	if err != nil {
		return err
	}
	if j.cancel != nil {
		j.cancel()
	}
	return nil
}

// stopJobs cancels running transfers and waits for them (used by Close).
func (s *Service) stopJobs() {
	s.jobs.mu.Lock()
	s.jobs.stop = true
	for _, j := range s.jobs.all {
		if j.cancel != nil {
			j.cancel()
		}
	}
	s.jobs.mu.Unlock()
	s.jobs.wg.Wait()
}
