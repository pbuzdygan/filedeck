package core

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"sort"
	"time"

	"github.com/pbuzdygan/filedeck/internal/storage"
	bolt "go.etcd.io/bbolt"
)

// ErrTrashNotFound is returned for unknown or foreign trash items.
var ErrTrashNotFound = errors.New("trash item not found")

// TrashItem is the durable description of something moved to a space's trash.
// Path is the original location; it is empty for items found without a record.
type TrashItem struct {
	ID        string    `json:"id"`
	Space     string    `json:"space"`
	Path      string    `json:"path"`
	Directory bool      `json:"directory"`
	Size      int64     `json:"size"`
	Deleted   time.Time `json:"deleted"`
	DeletedBy string    `json:"deleted_by"`
	// Replaced marks a previous version kept when a file was edited.
	Replaced bool `json:"replaced,omitempty"`
}

func itemName(id string) string { return "item-" + id }

func (s *Service) putTrash(t TrashItem) error { return put(s.db, trashBucket, t.ID, t) }
func (s *Service) dropTrash(ids ...string) error {
	return drop(s.db, trashBucket, ids...)
}

// recoverTrash drops records whose item vanished (crash between record and
// rename, or a restore/purge interrupted after the move) and adopts items that
// have no record (crash after rename) so they stay visible and expire normally.
func (s *Service) recoverTrash() error {
	records, err := s.trashRecords()
	if err != nil {
		return err
	}
	present := make(map[string]map[string]bool)
	for name, sp := range s.spaces {
		items, e := sp.TrashItems()
		if e != nil {
			return e
		}
		present[name] = make(map[string]bool, len(items))
		for _, n := range items {
			present[name][n] = true
		}
	}
	known := make(map[string]map[string]bool)
	var stale []string
	for _, t := range records {
		if _, ok := s.spaces[t.Space]; !ok {
			continue // space not mounted now; keep the record for later
		}
		if !present[t.Space][itemName(t.ID)] {
			stale = append(stale, t.ID)
			continue
		}
		if known[t.Space] == nil {
			known[t.Space] = make(map[string]bool)
		}
		known[t.Space][itemName(t.ID)] = true
	}
	if err = s.dropTrash(stale...); err != nil {
		return err
	}
	now := s.now()
	for name, sp := range s.spaces {
		for n := range present[name] {
			if known[name][n] {
				continue
			}
			t := TrashItem{ID: n[len("item-"):], Space: name, Deleted: now}
			if st, e := sp.TrashEntry(n); e == nil {
				t.Directory, t.Size = st.Directory, st.Size
			}
			if err = s.putTrash(t); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *Service) trashRecords() ([]TrashItem, error) {
	var out []TrashItem
	err := s.db.View(func(tx *bolt.Tx) error {
		return tx.Bucket(trashBucket).ForEach(func(k, v []byte) error {
			var t TrashItem
			if json.Unmarshal(v, &t) == nil && t.ID == string(k) && storage.TrashName(itemName(t.ID)) {
				out = append(out, t)
			}
			return nil
		})
	})
	return out, err
}

func (s *Service) trashRecord(id string) (TrashItem, error) {
	var t TrashItem
	err := s.db.View(func(tx *bolt.Tx) error {
		v := tx.Bucket(trashBucket).Get([]byte(id))
		if v == nil {
			return ErrTrashNotFound
		}
		return json.Unmarshal(v, &t)
	})
	return t, err
}

// Rename moves an entry within one space. Requires Modify.
func (s *Service) Rename(ctx context.Context, subject, space, from, to string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.policyMu.RLock()
	defer s.policyMu.RUnlock()
	sp, ok := s.spaces[space]
	if !ok || subject == "" || s.grantedLocked(subject, space)&Modify == 0 {
		return ErrDenied
	}
	return sp.Rename(from, to)
}

// Trash moves an entry into the space's trash. The record is written before
// the move, so a crash leaves either a stale record (dropped at start) or the
// item with its record. Requires Modify.
func (s *Service) Trash(ctx context.Context, subject, space, name string) (TrashItem, error) {
	if err := ctx.Err(); err != nil {
		return TrashItem{}, err
	}
	// Lock order everywhere: trashMu before policyMu.
	s.trashMu.Lock()
	defer s.trashMu.Unlock()
	s.policyMu.RLock()
	defer s.policyMu.RUnlock()
	sp, ok := s.spaces[space]
	if !ok || subject == "" || s.grantedLocked(subject, space)&Modify == 0 {
		return TrashItem{}, ErrDenied
	}
	if sp.ReadOnly() {
		return TrashItem{}, storage.ErrReadOnly
	}
	entry, err := sp.Stat(name)
	if err != nil {
		return TrashItem{}, err
	}
	var raw [32]byte
	if _, err = rand.Read(raw[:]); err != nil {
		return TrashItem{}, err
	}
	t := TrashItem{ID: hex.EncodeToString(raw[:]), Space: space, Path: name, Directory: entry.Directory, Size: entry.Size, Deleted: s.now(), DeletedBy: subject}
	if err = s.putTrash(t); err != nil {
		return TrashItem{}, err
	}
	if _, err = sp.Trash(name, itemName(t.ID)); err != nil {
		return TrashItem{}, errors.Join(err, s.dropTrash(t.ID))
	}
	return t, nil
}

// TrashList returns the space's trash, newest first. Requires Modify.
func (s *Service) TrashList(subject, space string) ([]TrashItem, error) {
	if !s.allowed(subject, space, Modify) {
		return nil, ErrDenied
	}
	records, err := s.trashRecords()
	if err != nil {
		return nil, err
	}
	out := make([]TrashItem, 0)
	for _, t := range records {
		if t.Space == space {
			out = append(out, t)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Deleted.After(out[j].Deleted) })
	return out, nil
}

// Restore moves an item back to target (or its original path). It never
// replaces an existing entry. Requires Modify on the item's space.
func (s *Service) Restore(ctx context.Context, subject, id, target string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.trashMu.Lock()
	defer s.trashMu.Unlock()
	t, err := s.trashRecord(id)
	if err != nil {
		return err
	}
	s.policyMu.RLock()
	defer s.policyMu.RUnlock()
	sp, ok := s.spaces[t.Space]
	if !ok || subject == "" || s.grantedLocked(subject, t.Space)&Modify == 0 {
		return ErrDenied
	}
	if target == "" {
		target = t.Path
	}
	if target == "" {
		return storage.ErrPath
	}
	if err = sp.Restore(itemName(id), target); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			if _, e := sp.TrashEntry(itemName(id)); errors.Is(e, os.ErrNotExist) {
				return errors.Join(ErrTrashNotFound, s.dropTrash(id))
			}
		}
		return err
	}
	return s.dropTrash(id)
}

// Purge permanently deletes an item. Requires Modify; the HTTP layer
// additionally restricts it to administrators.
func (s *Service) Purge(ctx context.Context, subject, id string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.trashMu.Lock()
	defer s.trashMu.Unlock()
	t, err := s.trashRecord(id)
	if err != nil {
		return err
	}
	if !s.allowed(subject, t.Space, Modify) {
		return ErrDenied
	}
	if err = s.spaces[t.Space].Purge(itemName(id)); err != nil {
		return err
	}
	return s.dropTrash(id)
}

func (s *Service) expireTrash() error {
	s.trashMu.Lock()
	defer s.trashMu.Unlock()
	records, err := s.trashRecords()
	if err != nil {
		return err
	}
	limit := s.now().Add(-s.limits.TrashRetention)
	var result error
	for _, t := range records {
		sp, ok := s.spaces[t.Space]
		if !ok || !t.Deleted.Before(limit) {
			continue
		}
		if e := sp.Purge(itemName(t.ID)); e != nil {
			result = errors.Join(result, e)
			continue
		}
		result = errors.Join(result, s.dropTrash(t.ID))
	}
	return result
}
