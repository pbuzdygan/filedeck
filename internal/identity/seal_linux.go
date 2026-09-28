package identity

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"errors"

	bolt "go.etcd.io/bbolt"
	"golang.org/x/sys/unix"
)

// Two-factor secrets can be protected with a key kept outside the state
// directory (FILEDECK_SECRET_KEY), so a leaked database or backup alone does
// not give away the secrets: each TOTP secret is encrypted with AES-256-GCM
// bound to its account, and each recovery code digest becomes
// HMAC-SHA256(key, SHA-256(code)), which cannot be brute-forced offline.

var (
	ErrSecretKeyWrong   = errors.New("the secret key does not match the key the two-factor secrets were protected with")
	ErrSecretKeyMissing = errors.New("two-factor secrets are protected with a secret key: set it again (or turn two-factor authentication off with reset-2fa)")
)

const SecretKeySize = 32

var keyCheckName = []byte("secret-key-check")

// compactName marks (in the schema bucket) that unprotected secrets were just
// protected: their old copies may remain in freed database pages until the
// file is rewritten, which is done (and retried after a crash) by compact.
var compactName = []byte("compact-pending")

func keyCheck(key []byte) []byte {
	m := hmac.New(sha256.New, key)
	m.Write([]byte("filedeck-secret-key-check-v1"))
	return m.Sum(nil)
}
func sealAAD(id string) []byte { return []byte("filedeck-totp-v1\x00" + id) }

func (s *Store) aead() (cipher.AEAD, error) {
	block, err := aes.NewCipher(s.key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// setSecret stores secret in f, encrypted when a key is set.
func (s *Store) setSecret(f *secondFactor, id string, secret []byte) error {
	if s.key == nil {
		f.Secret, f.Sealed = secret, nil
		return nil
	}
	g, err := s.aead()
	if err != nil {
		return err
	}
	nonce := make([]byte, g.NonceSize())
	rand.Read(nonce)
	f.Sealed = g.Seal(nonce, nonce, secret, sealAAD(id))
	f.Secret = nil
	return nil
}

// secret returns the plaintext TOTP secret of f.
func (s *Store) secret(f *secondFactor, id string) ([]byte, error) {
	if f.Sealed == nil {
		return f.Secret, nil
	}
	if s.key == nil {
		return nil, ErrSecretKeyMissing
	}
	g, err := s.aead()
	if err != nil {
		return nil, err
	}
	if len(f.Sealed) < g.NonceSize() {
		return nil, ErrSecretKeyWrong
	}
	plain, err := g.Open(nil, f.Sealed[:g.NonceSize()], f.Sealed[g.NonceSize():], sealAAD(id))
	if err != nil {
		return nil, ErrSecretKeyWrong
	}
	return plain, nil
}

// keyed turns a SHA-256 recovery code digest into its stored form.
func (s *Store) keyed(digest []byte) []byte {
	if s.key == nil {
		return digest
	}
	m := hmac.New(sha256.New, s.key)
	m.Write(digest)
	return m.Sum(nil)
}
func (s *Store) setRecovery(f *secondFactor, digests [][]byte) {
	f.Recovery = make([][]byte, len(digests))
	for i, d := range digests {
		f.Recovery[i] = s.keyed(d)
	}
	f.Keyed = s.key != nil
}

// Protect sets the key for two-factor secrets before serving (nil: none).
// With a key it refuses a different key than before and protects every
// secret and recovery code still stored without it. Without a key it refuses
// to continue when protected secrets exist, and reports how many accounts keep
// an unprotected secret. Commands that only remove two-factor authentication
// (reset-2fa) work without calling it.
func (s *Store) Protect(key []byte) (int, error) {
	if key != nil && len(key) != SecretKeySize {
		return 0, ErrInvalid
	}
	s.key = key
	unprotected := 0
	err := s.db.Update(func(tx *bolt.Tx) error {
		meta := tx.Bucket([]byte("schema"))
		stored := meta.Get(keyCheckName)
		if key != nil && stored != nil && !hmac.Equal(stored, keyCheck(key)) {
			return ErrSecretKeyWrong
		}
		users := tx.Bucket(usersBucket)
		changed := map[string]account{}
		if e := users.ForEach(func(k, v []byte) error {
			var a account
			if er := json.Unmarshal(v, &a); er != nil {
				return er
			}
			f := a.TOTP
			if f == nil {
				return nil
			}
			if key == nil {
				if f.Sealed != nil || f.Keyed {
					return ErrSecretKeyMissing
				}
				unprotected++
				return nil
			}
			if f.Sealed != nil && f.Keyed {
				return nil
			}
			if f.Sealed == nil {
				if er := s.setSecret(f, string(k), f.Secret); er != nil {
					return er
				}
			}
			if !f.Keyed {
				s.setRecovery(f, f.Recovery)
			}
			changed[string(k)] = a
			return nil
		}); e != nil {
			return e
		}
		for id, a := range changed {
			if e := put(users, id, a); e != nil {
				return e
			}
		}
		if len(changed) > 0 {
			if e := meta.Put(compactName, []byte("1")); e != nil {
				return e
			}
		}
		if key == nil {
			return meta.Delete(keyCheckName)
		}
		return meta.Put(keyCheckName, keyCheck(key))
	})
	if err != nil {
		s.key = nil
		return 0, err
	}
	pending := false
	if err = s.db.View(func(tx *bolt.Tx) error {
		pending = tx.Bucket([]byte("schema")).Get(compactName) != nil
		return nil
	}); err != nil {
		return 0, err
	}
	if pending {
		if err = s.compact(); err != nil {
			return 0, err
		}
	}
	return unprotected, nil
}

// compact rewrites the database into a new file holding only live data and
// atomically replaces the old one, so freed pages with unprotected secrets
// are gone. It runs before serving, without concurrent use of the store.
func (s *Store) compact() error {
	const tmp = "identity.db.compact"
	fd, err := openDir(s.dir)
	if err != nil {
		return err
	}
	defer unix.Close(fd)
	if err = unix.Unlinkat(fd, tmp, 0); err != nil && !errors.Is(err, unix.ENOENT) {
		return err
	}
	dst, err := openBolt(fd, s.dir, tmp, unix.O_EXCL)
	if err != nil {
		return err
	}
	err = bolt.Compact(dst, s.db, 1<<20)
	if err == nil {
		err = dst.Update(func(tx *bolt.Tx) error { return tx.Bucket([]byte("schema")).Delete(compactName) })
	}
	if err = errors.Join(err, dst.Close()); err != nil {
		_ = unix.Unlinkat(fd, tmp, 0)
		return err
	}
	if err = s.db.Close(); err != nil {
		return err
	}
	renamed := unix.Renameat(fd, tmp, fd, "identity.db")
	if renamed == nil {
		renamed = unix.Fsync(fd)
	}
	if s.db, err = openBolt(fd, s.dir, "identity.db", 0); err != nil {
		return errors.Join(renamed, err)
	}
	return renamed
}
