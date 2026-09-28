package core

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"sort"
	"time"
	"unicode/utf8"

	"github.com/pbuzdygan/filedeck/internal/storage"
	bolt "go.etcd.io/bbolt"
	"golang.org/x/crypto/argon2"
)

// Public links give read-only access to one file or folder without an account.
// A link is bound to the object (device, inode, birth time), not to a path
// prefix, and is re-validated on every request: it stops working when it
// expires or is revoked, when the object is moved, renamed, replaced or
// deleted, and — permanently, the link is deleted — when its owner loses Read
// (or List, for a folder) on the space or is disabled. Only a hash of the token is stored; a link password is stored as an
// Argon2id digest and never returned.

var linksBucket = []byte("links")

var (
	// ErrLinkNotFound is the single answer for unknown, expired, revoked and
	// no longer valid links, so a visitor learns nothing about why.
	ErrLinkNotFound = errors.New("link not found or no longer valid")
	ErrLinkPassword = errors.New("wrong link password")
	ErrLinkInput    = errors.New("invalid link settings")
)

const (
	MinLinkLifetime = time.Hour
	MaxLinkLifetime = 365 * 24 * time.Hour
	MaxLinksPerUser = 200
	MaxLinks        = 5000
	MinLinkPassword = 8
)

// Link is the owner's and administrators' view: never the token or password.
type Link struct {
	ID          string    `json:"id"`
	Owner       string    `json:"owner"`
	Space       string    `json:"space"`
	Path        string    `json:"path"`
	Directory   bool      `json:"directory"`
	Created     time.Time `json:"created"`
	Expires     time.Time `json:"expires"`
	HasPassword bool      `json:"has_password"`
	// Available is computed when listing: false when the link no longer works.
	Available bool `json:"available"`
}

type linkRecord struct {
	Link
	Object storage.ObjectID `json:"object"`
	Salt   []byte           `json:"salt,omitempty"`
	Digest []byte           `json:"digest,omitempty"`
}

// linkKey maps a token (32 random bytes, base64url) to its database key.
// Strict decoding accepts only the canonical form: no second spelling of a token.
func linkKey(token string) (string, bool) {
	b, err := base64.RawURLEncoding.Strict().DecodeString(token)
	if err != nil || len(b) != 32 {
		return "", false
	}
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:]), true
}

func linkDigest(password string, salt []byte) []byte {
	return argon2.IDKey([]byte(password), salt, 3, 32*1024, 1, 32)
}

// hashing bounds concurrent Argon2 work for link passwords.
func (s *Service) hashing(ctx context.Context) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	select {
	case s.linkGate <- struct{}{}:
		return func() { <-s.linkGate }, nil
	default:
		return nil, ErrQuota
	}
}

func linkNeeds(directory bool) Permission {
	if directory {
		return Read | List
	}
	return Read
}

// CreateLink shares an existing file or folder the subject can read. It returns
// the token once; only its hash is kept.
func (s *Service) CreateLink(ctx context.Context, subject, space, name string, lifetime time.Duration, password string) (string, Link, error) {
	if lifetime < MinLinkLifetime || lifetime > MaxLinkLifetime || (password != "" && (!utf8.ValidString(password) || utf8.RuneCountInString(password) < MinLinkPassword || len(password) > 1024)) {
		return "", Link{}, ErrLinkInput
	}
	if !s.allowed(subject, space, Read) {
		return "", Link{}, ErrDenied
	}
	id, _, err := s.spaces[space].Identify(name)
	if err != nil {
		return "", Link{}, err
	}
	if !s.allowed(subject, space, linkNeeds(id.Directory)) {
		return "", Link{}, ErrDenied
	}
	now := s.now().UTC()
	rec := linkRecord{Link: Link{ID: rand.Text(), Owner: subject, Space: space, Path: name, Directory: id.Directory, Created: now, Expires: now.Add(lifetime), HasPassword: password != ""}, Object: id}
	if password != "" {
		release, err := s.hashing(ctx)
		if err != nil {
			return "", Link{}, err
		}
		rec.Salt = []byte(rand.Text())
		rec.Digest = linkDigest(password, rec.Salt)
		release()
	}
	raw := make([]byte, 32)
	rand.Read(raw)
	token := base64.RawURLEncoding.EncodeToString(raw)
	key, _ := linkKey(token)
	data, err := json.Marshal(rec)
	if err != nil {
		return "", Link{}, err
	}
	err = s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(linksBucket)
		if b.Stats().KeyN >= MaxLinks {
			return ErrQuota
		}
		mine := 0
		if e := b.ForEach(func(_, v []byte) error {
			var r linkRecord
			if json.Unmarshal(v, &r) == nil && r.Owner == subject {
				mine++
			}
			return nil
		}); e != nil {
			return e
		}
		if mine >= MaxLinksPerUser {
			return ErrQuota
		}
		return b.Put([]byte(key), data)
	})
	if err != nil {
		return "", Link{}, err
	}
	rec.Available = true
	return token, rec.Link, nil
}

// Links lists the subject's links, or every link for an administrator view.
func (s *Service) Links(subject string, all bool) ([]Link, error) {
	var recs []linkRecord
	err := s.db.View(func(tx *bolt.Tx) error {
		return tx.Bucket(linksBucket).ForEach(func(_, v []byte) error {
			var r linkRecord
			if json.Unmarshal(v, &r) == nil && (all || r.Owner == subject) {
				recs = append(recs, r)
			}
			return nil
		})
	})
	if err != nil {
		return nil, err
	}
	out := make([]Link, 0, len(recs))
	for _, r := range recs {
		if obj, err := s.openLinkObject(r); err == nil {
			obj.Close()
			r.Available = true
		}
		out = append(out, r.Link)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Created.After(out[j].Created) })
	return out, nil
}

// RevokeLink deletes a link owned by the subject (any link for an administrator).
func (s *Service) RevokeLink(subject, id string, admin bool) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(linksBucket)
		var key []byte
		if e := b.ForEach(func(k, v []byte) error {
			var r linkRecord
			if json.Unmarshal(v, &r) == nil && r.ID == id && subject != "" && (r.Owner == subject || admin) {
				key = append([]byte(nil), k...)
			}
			return nil
		}); e != nil {
			return e
		}
		if key == nil {
			return ErrLinkNotFound
		}
		return b.Delete(key)
	})
}

// openLinkObject re-validates a link record: expiry, the owner's current
// permissions and the identity of the object at its path.
func (s *Service) openLinkObject(r linkRecord) (*storage.Object, error) {
	if !s.now().Before(r.Expires) || !s.allowed(r.Owner, r.Space, linkNeeds(r.Directory)) {
		return nil, ErrLinkNotFound
	}
	obj, err := s.spaces[r.Space].OpenObject(r.Path, r.Object)
	if errors.Is(err, storage.ErrGone) || errors.Is(err, storage.ErrPath) || errors.Is(err, fs.ErrNotExist) {
		return nil, ErrLinkNotFound
	}
	return obj, err
}

// Shared is an opened, validated public link. Close it after the request.
type Shared struct {
	Link
	Entry  storage.Entry
	s      *Service
	obj    *storage.Object
	salt   []byte
	digest []byte
}

// OpenLink resolves a token to a valid link.
func (s *Service) OpenLink(token string) (*Shared, error) {
	key, ok := linkKey(token)
	if !ok {
		return nil, ErrLinkNotFound
	}
	var r linkRecord
	found := false
	err := s.db.View(func(tx *bolt.Tx) error {
		if v := tx.Bucket(linksBucket).Get([]byte(key)); v != nil {
			found = json.Unmarshal(v, &r) == nil
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, ErrLinkNotFound
	}
	obj, err := s.openLinkObject(r)
	if err != nil {
		return nil, err
	}
	r.Available = true
	return &Shared{Link: r.Link, Entry: obj.Entry, s: s, obj: obj, salt: r.Salt, digest: r.Digest}, nil
}

func (l *Shared) Close() error { return l.obj.Close() }

// CheckPassword verifies the link password (bounded Argon2 work).
func (l *Shared) CheckPassword(ctx context.Context, password string) error {
	if !l.HasPassword {
		return nil
	}
	if len(password) > 1024 {
		return ErrLinkPassword
	}
	release, err := l.s.hashing(ctx)
	if err != nil {
		return err
	}
	defer release()
	if subtle.ConstantTimeCompare(linkDigest(password, l.salt), l.digest) != 1 {
		return ErrLinkPassword
	}
	return nil
}

// Read opens the shared file (sub ".") or a file beneath a shared folder.
func (l *Shared) Read(sub string) (*os.File, error) { return l.obj.Read(sub) }

// List lists the shared folder or a folder beneath it.
func (l *Shared) List(ctx context.Context, sub string) ([]storage.Entry, error) {
	return l.obj.List(ctx, sub, 10000)
}

// pruneLinks deletes the subject's links it can no longer create.
func (s *Service) pruneLinks(subject string) error {
	return s.deleteLinks(func(r linkRecord) bool {
		return r.Owner == subject && !s.allowed(subject, r.Space, linkNeeds(r.Directory))
	})
}

// expireLinks removes links past their expiry.
func (s *Service) expireLinks() error {
	now := s.now()
	return s.deleteLinks(func(r linkRecord) bool { return !now.Before(r.Expires) })
}

// deleteLinks removes every link matching stale, and unreadable records.
func (s *Service) deleteLinks(stale func(linkRecord) bool) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(linksBucket)
		var keys [][]byte
		if e := b.ForEach(func(k, v []byte) error {
			var r linkRecord
			if json.Unmarshal(v, &r) != nil || stale(r) {
				keys = append(keys, append([]byte(nil), k...))
			}
			return nil
		}); e != nil {
			return e
		}
		for _, k := range keys {
			if e := b.Delete(k); e != nil {
				return e
			}
		}
		return nil
	})
}
