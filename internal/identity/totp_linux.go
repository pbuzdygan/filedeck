package identity

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base32"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"

	bolt "go.etcd.io/bbolt"
)

// Two-factor authentication with time-based one-time passwords (RFC 6238:
// HMAC-SHA1, 6 digits, 30-second steps), the variant every authenticator app
// supports. Each account turns it on and off by itself; an administrator can
// only switch it off for someone who lost their device.

var (
	ErrTOTPRequired = errors.New("authentication code required")
	ErrTOTPInvalid  = errors.New("invalid authentication code")
	ErrTOTPLocked   = errors.New("too many wrong authentication codes")
)

const (
	totpPeriod    = 30
	totpSkew      = 1 // accepted steps before and after the current one
	recoveryCount = 10
	recoveryLen   = 10
	// After strikeLimit wrong codes in a row the account refuses codes for
	// strikeBase, doubling with every further lockout up to strikeMax.
	strikeLimit = 5
	strikeBase  = 5 * time.Minute
	strikeMax   = time.Hour
)

// secondFactor is stored in the account. The secret is kept either as Secret
// or, with a secret key, encrypted as Sealed (see seal_linux.go). Recovery
// holds digests of the unused recovery codes: SHA-256, or HMAC of it with the
// secret key when Keyed. LastStep blocks replay of an accepted code.
type secondFactor struct {
	Secret   []byte    `json:"secret,omitempty"`
	Sealed   []byte    `json:"sealed,omitempty"`
	LastStep int64     `json:"last_step"`
	Recovery [][]byte  `json:"recovery"`
	Keyed    bool      `json:"keyed,omitempty"`
	Enabled  time.Time `json:"enabled"`
}

// strikes counts wrong codes per account in memory. Entries exist only for
// accounts whose password was correct, so the map is bounded by maxUsers.
type strikes struct {
	mu   sync.Mutex
	list map[string]strike
}
type strike struct {
	fails, locks int
	until        time.Time
}

func (s *strikes) locked(id string, now time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return now.Before(s.list[id].until)
}
func (s *strikes) fail(id string, now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.list[id]
	st.fails++
	if st.fails >= strikeLimit {
		st.fails = 0
		st.locks++
		st.until = now.Add(min(strikeMax, strikeBase<<min(st.locks-1, 8)))
	}
	s.list[id] = st
}
func (s *strikes) clear(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.list, id)
}

var b32 = base32.StdEncoding.WithPadding(base32.NoPadding)

// NewTOTPSecret returns a random 160-bit secret (the RFC 4226 recommendation).
func NewTOTPSecret() []byte {
	s := make([]byte, 20)
	rand.Read(s) // crypto/rand.Read never fails on Linux (panics otherwise)
	return s
}

// TOTPKey is the secret as authenticator apps expect it typed in by hand.
func TOTPKey(secret []byte) string { return b32.EncodeToString(secret) }

// TOTPURI is the otpauth:// address shown as a QR code during setup.
func TOTPURI(issuer, account string, secret []byte) string {
	q := url.Values{"secret": {TOTPKey(secret)}, "issuer": {issuer}, "algorithm": {"SHA1"}, "digits": {"6"}, "period": {"30"}}
	return "otpauth://totp/" + url.PathEscape(issuer+":"+account) + "?" + q.Encode()
}

func totpAt(secret []byte, step int64) string {
	var msg [8]byte
	binary.BigEndian.PutUint64(msg[:], uint64(step))
	m := hmac.New(sha1.New, secret)
	m.Write(msg[:])
	h := m.Sum(nil)
	o := h[len(h)-1] & 0x0f
	return fmt.Sprintf("%06d", (binary.BigEndian.Uint32(h[o:o+4])&0x7fffffff)%1000000)
}

// matchTOTP returns the accepted step for code, or 0 when it does not match
// any step within the allowed skew that is newer than last.
func matchTOTP(secret []byte, code string, now time.Time, last int64) int64 {
	current := now.Unix() / totpPeriod
	for step := current - totpSkew; step <= current+totpSkew; step++ {
		if step > last && subtle.ConstantTimeCompare([]byte(totpAt(secret, step)), []byte(code)) == 1 {
			return step
		}
	}
	return 0
}

// isTOTP reports whether a code is shaped like a TOTP (6 digits) rather than
// a recovery code.
func isTOTP(code string) bool {
	if len(code) != 6 {
		return false
	}
	for _, c := range code {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// cleanCode removes the spaces and dashes people type or copy with a code.
func cleanCode(code string) string {
	return strings.ToUpper(strings.NewReplacer(" ", "", "-", "").Replace(code))
}

const recoveryAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"

// newRecoveryCodes returns codes to show once (XXXXX-XXXXX, 50 bits each) and
// the digests to store.
func newRecoveryCodes() ([]string, [][]byte) {
	codes := make([]string, recoveryCount)
	digests := make([][]byte, recoveryCount)
	for i := range codes {
		b := make([]byte, recoveryLen)
		rand.Read(b)
		for j := range b {
			b[j] = recoveryAlphabet[int(b[j])%len(recoveryAlphabet)] // 256 is a multiple of 32: no bias
		}
		codes[i] = string(b[:5]) + "-" + string(b[5:])
		sum := sha256.Sum256(b)
		digests[i] = sum[:]
	}
	return codes, digests
}

// useCode checks a TOTP or recovery code against the second factor of
// account id and consumes it: the step becomes the last accepted one, a
// recovery code is removed.
func (s *Store) useCode(f *secondFactor, id, code string, now time.Time) (bool, error) {
	code = cleanCode(code)
	if isTOTP(code) {
		secret, err := s.secret(f, id)
		if err != nil {
			return false, err
		}
		step := matchTOTP(secret, code, now, f.LastStep)
		if step == 0 {
			return false, nil
		}
		f.LastStep = step
		return true, nil
	}
	if len(code) != recoveryLen {
		return false, nil
	}
	if f.Keyed && s.key == nil {
		return false, ErrSecretKeyMissing
	}
	sum := sha256.Sum256([]byte(code))
	want := s.keyed(sum[:])
	found := -1
	for i, d := range f.Recovery {
		if subtle.ConstantTimeCompare(d, want) == 1 {
			found = i
		}
	}
	if found < 0 {
		return false, nil
	}
	f.Recovery = append(f.Recovery[:found:found], f.Recovery[found+1:]...)
	return true, nil
}

// keepSession ends every session of the account except the one with token and
// moves that one to the new account version, so the person who changed their
// own security settings stays signed in on this device only.
func keepSession(tx *bolt.Tx, id, token string, version uint64) error {
	keep, _ := tokenKey(token)
	b := tx.Bucket(sessionsBucket)
	var remove [][]byte
	var kept []byte
	if err := b.ForEach(func(k, v []byte) error {
		var se session
		if e := json.Unmarshal(v, &se); e != nil {
			return e
		}
		if se.UserID != id {
			return nil
		}
		if keep != "" && string(k) == keep {
			se.Version = version
			data, e := json.Marshal(se)
			kept = data
			return e
		}
		remove = append(remove, append([]byte(nil), k...))
		return nil
	}); err != nil {
		return err
	}
	for _, k := range remove {
		if err := b.Delete(k); err != nil {
			return err
		}
	}
	if kept != nil {
		return b.Put([]byte(keep), kept)
	}
	return nil
}

// TwoFactor reports whether an account uses two-factor authentication and how
// many unused recovery codes it has left.
func (s *Store) TwoFactor(id string) (bool, int, error) {
	var a account
	err := s.db.View(func(tx *bolt.Tx) error { var e error; a, e = readAccount(tx, id); return e })
	if err != nil || a.TOTP == nil {
		return false, 0, err
	}
	return true, len(a.TOTP.Recovery), nil
}

// EnableTOTP turns on (or replaces) two-factor authentication once code proves
// that the authenticator app holds secret. Other sessions of the account end;
// the session with token stays. It returns the recovery codes to show once.
func (s *Store) EnableTOTP(ctx context.Context, id, token string, secret []byte, code string) ([]string, error) {
	if len(secret) != 20 {
		return nil, ErrInvalid
	}
	f := secondFactor{Enabled: s.now()}
	code = cleanCode(code)
	if !isTOTP(code) {
		return nil, ErrTOTPInvalid
	}
	if f.LastStep = matchTOTP(secret, code, s.now(), 0); f.LastStep == 0 {
		return nil, ErrTOTPInvalid
	}
	if err := s.setSecret(&f, id, secret); err != nil {
		return nil, err
	}
	codes, digests := newRecoveryCodes()
	s.setRecovery(&f, digests)
	err := s.db.Update(func(tx *bolt.Tx) error {
		if e := ctx.Err(); e != nil {
			return e
		}
		a, e := readAccount(tx, id)
		if e != nil {
			return e
		}
		if a.User.Disabled {
			return ErrAuth
		}
		a.TOTP = &f
		a.Version++
		if e = put(tx.Bucket(usersBucket), id, a); e != nil {
			return e
		}
		return keepSession(tx, id, token, a.Version)
	})
	if err != nil {
		return nil, err
	}
	s.strikes.clear(id)
	return codes, nil
}

// DisableTOTP switches two-factor authentication off. With a token (the person
// themselves) that session stays; without one (an administrator or the local
// operator) every session of the account ends.
func (s *Store) DisableTOTP(id, token string) error {
	err := s.db.Update(func(tx *bolt.Tx) error {
		a, e := readAccount(tx, id)
		if e != nil {
			return e
		}
		if a.TOTP == nil {
			return nil
		}
		a.TOTP = nil
		a.Version++
		if e = put(tx.Bucket(usersBucket), id, a); e != nil {
			return e
		}
		return keepSession(tx, id, token, a.Version)
	})
	if err == nil {
		s.strikes.clear(id)
	}
	return err
}

// NewRecoveryCodes replaces the recovery codes; the old ones stop working.
func (s *Store) NewRecoveryCodes(id string) ([]string, error) {
	codes, digests := newRecoveryCodes()
	err := s.db.Update(func(tx *bolt.Tx) error {
		a, e := readAccount(tx, id)
		if e != nil {
			return e
		}
		if a.TOTP == nil {
			return ErrInvalid
		}
		s.setRecovery(a.TOTP, digests)
		return put(tx.Bucket(usersBucket), id, a)
	})
	if err != nil {
		return nil, err
	}
	return codes, nil
}
