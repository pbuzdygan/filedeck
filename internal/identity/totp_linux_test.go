package identity

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	bolt "go.etcd.io/bbolt"
)

func TestTOTPMatchesRFC6238(t *testing.T) {
	// RFC 6238 appendix B (SHA-1), last six digits of the eight-digit values.
	key := []byte("12345678901234567890")
	for unix, want := range map[int64]string{59: "287082", 1111111109: "081804", 1111111111: "050471", 1234567890: "005924", 2000000000: "279037"} {
		if got := totpAt(key, unix/totpPeriod); got != want {
			t.Fatalf("T=%d: %s, want %s", unix, got, want)
		}
	}
	uri := TOTPURI("Filedeck", "anna@files.example", key)
	if !strings.HasPrefix(uri, "otpauth://totp/Filedeck:anna@files.example?") || !strings.Contains(uri, "secret="+TOTPKey(key)) || !strings.Contains(uri, "issuer=Filedeck") {
		t.Fatal(uri)
	}
}

// shifted returns a different, well-formed code.
func shifted(code string) string {
	c := []byte(code)
	c[0] = '0' + (c[0]-'0'+1)%10
	return string(c)
}

// clock makes the store's time controllable.
func clock(s *Store) *time.Time {
	now := time.Unix(1_900_000_000, 0)
	s.now = func() time.Time { return now }
	return &now
}

func TestTwoFactorLifecycle(t *testing.T) {
	s, _, u := setup(t)
	now := clock(s)
	ctx := context.Background()
	this := login(t, s, "admin", secret)
	other := login(t, s, "admin", secret)
	key := NewTOTPSecret()
	code := func() string { return totpAt(key, now.Unix()/totpPeriod) }

	for _, bad := range []string{shifted(code()), "", "12345", "abcdef", "AAAAA-AAAAA"} {
		if _, e := s.EnableTOTP(ctx, u.ID, this.Token, key, bad); !errors.Is(e, ErrTOTPInvalid) {
			t.Fatal(bad, e)
		}
	}
	codes, err := s.EnableTOTP(ctx, u.ID, this.Token, key, code())
	if err != nil || len(codes) != recoveryCount {
		t.Fatal(codes, err)
	}
	// Enabling keeps this session and ends the others.
	if _, e := s.Authenticate(this.Token); e != nil {
		t.Fatal(e)
	}
	if _, e := s.Authenticate(other.Token); !errors.Is(e, ErrAuth) {
		t.Fatal(e)
	}
	on, left, err := s.TwoFactor(u.ID)
	if !on || left != recoveryCount || err != nil {
		t.Fatal(on, left, err)
	}
	users, _ := s.Users()
	data, _ := json.Marshal(users)
	if !users[0].TwoFactor || strings.Contains(string(data), "secret") || strings.Contains(string(data), TOTPKey(key)) {
		t.Fatal(string(data))
	}

	// A wrong password never reveals that a code would be needed.
	if _, e := s.Login(ctx, "admin", "wrong-password-123", ""); !errors.Is(e, ErrAuth) {
		t.Fatal(e)
	}
	if _, e := s.Login(ctx, "admin", secret, ""); !errors.Is(e, ErrTOTPRequired) {
		t.Fatal(e)
	}
	// The code enabling 2FA was consumed; the next step's code works once.
	if _, e := s.Login(ctx, "admin", secret, code()); !errors.Is(e, ErrTOTPInvalid) {
		t.Fatal("replayed setup code:", e)
	}
	*now = now.Add(totpPeriod * time.Second)
	l, err := s.Login(ctx, "admin", secret, " "+code()[:3]+" "+code()[3:])
	if err != nil || l.RecoveryUsed {
		t.Fatal(l, err)
	}
	if _, e := s.Login(ctx, "admin", secret, code()); !errors.Is(e, ErrTOTPInvalid) {
		t.Fatal("replayed code:", e)
	}
	// A recovery code works once, in any letter case, with or without the dash.
	l, err = s.Login(ctx, "admin", secret, strings.ToLower(strings.ReplaceAll(codes[3], "-", "")))
	if err != nil || !l.RecoveryUsed {
		t.Fatal(l, err)
	}
	if _, e := s.Login(ctx, "admin", secret, codes[3]); !errors.Is(e, ErrTOTPInvalid) {
		t.Fatal(e)
	}
	if _, left, _ = s.TwoFactor(u.ID); left != recoveryCount-1 {
		t.Fatal(left)
	}
	// New recovery codes replace the old ones.
	fresh, err := s.NewRecoveryCodes(u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, e := s.Login(ctx, "admin", secret, codes[4]); !errors.Is(e, ErrTOTPInvalid) {
		t.Fatal(e)
	}
	if _, e := s.Login(ctx, "admin", secret, fresh[0]); e != nil {
		t.Fatal(e)
	}
}

func TestTwoFactorStrikesAndAdminReset(t *testing.T) {
	s, _, u := setup(t)
	now := clock(s)
	ctx := context.Background()
	this := login(t, s, "admin", secret)
	key := NewTOTPSecret()
	code := func() string { return totpAt(key, now.Unix()/totpPeriod) }
	if _, err := s.EnableTOTP(ctx, u.ID, this.Token, key, code()); err != nil {
		t.Fatal(err)
	}
	*now = now.Add(time.Minute)
	for i := 0; i < strikeLimit; i++ {
		if _, e := s.Login(ctx, "admin", secret, shifted(code())); !errors.Is(e, ErrTOTPInvalid) {
			t.Fatal(i, e)
		}
	}
	// Locked: even the right code is refused until the lockout passes.
	if _, e := s.Login(ctx, "admin", secret, code()); !errors.Is(e, ErrTOTPLocked) {
		t.Fatal(e)
	}
	*now = now.Add(strikeBase + time.Second)
	if _, e := s.Login(ctx, "admin", secret, code()); e != nil {
		t.Fatal(e)
	}
	// The person switching 2FA off keeps this session only.
	*now = now.Add(time.Minute)
	other, err := s.Login(ctx, "admin", secret, code())
	if err != nil {
		t.Fatal(err)
	}
	if err = s.DisableTOTP(u.ID, this.Token); err != nil {
		t.Fatal(err)
	}
	if _, e := s.Authenticate(this.Token); e != nil {
		t.Fatal(e)
	}
	if _, e := s.Authenticate(other.Token); !errors.Is(e, ErrAuth) {
		t.Fatal(e)
	}
	login(t, s, "admin", secret)
	// An administrator's reset (no token) ends every session.
	*now = now.Add(time.Minute)
	if _, err = s.EnableTOTP(ctx, u.ID, this.Token, key, code()); err != nil {
		t.Fatal(err)
	}
	if err = s.DisableTOTP(u.ID, ""); err != nil {
		t.Fatal(err)
	}
	if _, e := s.Authenticate(this.Token); !errors.Is(e, ErrAuth) {
		t.Fatal(e)
	}
	if on, _, _ := s.TwoFactor(u.ID); on {
		t.Fatal("still on")
	}
	if _, e := s.NewRecoveryCodes(u.ID); !errors.Is(e, ErrInvalid) {
		t.Fatal(e)
	}
}

func TestSecretKeyProtectsTwoFactorSecrets(t *testing.T) {
	s, dir, u := setup(t)
	now := clock(s)
	ctx := context.Background()
	this := login(t, s, "admin", secret)
	key := NewTOTPSecret()
	code := func() string { return totpAt(key, now.Unix()/totpPeriod) }
	// Enabled before a key existed: stored as plaintext.
	codes, err := s.EnableTOTP(ctx, u.ID, this.Token, key, code())
	if err != nil {
		t.Fatal(err)
	}
	raw := func() string {
		data, e := os.ReadFile(filepath.Join(dir, "identity.db"))
		if e != nil {
			t.Fatal(e)
		}
		return string(data)
	}
	plain := base64.StdEncoding.EncodeToString(key)
	if !strings.Contains(raw(), plain) {
		t.Fatal("expected the unprotected secret in the database")
	}
	if n, e := s.Protect(nil); n != 1 || e != nil {
		t.Fatal(n, e)
	}
	secretKey := bytes.Repeat([]byte{7}, SecretKeySize)
	if _, e := s.Protect(secretKey[:16]); !errors.Is(e, ErrInvalid) {
		t.Fatal(e)
	}
	if n, e := s.Protect(secretKey); n != 0 || e != nil {
		t.Fatal(n, e)
	}
	if strings.Contains(raw(), plain) {
		t.Fatal("secret still readable in the database")
	}
	if _, e := os.Stat(filepath.Join(dir, "identity.db.compact")); !os.IsNotExist(e) {
		t.Fatal("temporary copy left behind", e)
	}
	s.db.View(func(tx *bolt.Tx) error {
		if tx.Bucket([]byte("schema")).Get(compactName) != nil {
			t.Fatal("compaction still pending")
		}
		return nil
	})
	// An interrupted compaction (flag left behind) is repeated on the next start.
	s.db.Update(func(tx *bolt.Tx) error { return tx.Bucket([]byte("schema")).Put(compactName, []byte("1")) })
	if _, e := s.Protect(secretKey); e != nil {
		t.Fatal(e)
	}
	s.db.View(func(tx *bolt.Tx) error {
		if tx.Bucket([]byte("schema")).Get(compactName) != nil {
			t.Fatal("interrupted compaction not repeated")
		}
		return nil
	})
	// Codes keep working: TOTP (decrypted) and an old recovery code (converted).
	*now = now.Add(totpPeriod * time.Second)
	if _, e := s.Login(ctx, "admin", secret, code()); e != nil {
		t.Fatal(e)
	}
	if l, e := s.Login(ctx, "admin", secret, codes[0]); e != nil || !l.RecoveryUsed {
		t.Fatal(l, e)
	}
	// A new setup with the key is stored encrypted right away.
	*now = now.Add(totpPeriod * time.Second)
	key = NewTOTPSecret()
	if codes, err = s.EnableTOTP(ctx, u.ID, this.Token, key, code()); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(raw(), base64.StdEncoding.EncodeToString(key)) {
		t.Fatal("new secret stored in plaintext")
	}

	// After a restart the same key is required; a missing or different key stops the start.
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	if s, err = Open(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	now = clock(s)
	*now = now.Add(3 * totpPeriod * time.Second)
	if _, e := s.Protect(nil); !errors.Is(e, ErrSecretKeyMissing) {
		t.Fatal(e)
	}
	if _, e := s.Protect(bytes.Repeat([]byte{8}, SecretKeySize)); !errors.Is(e, ErrSecretKeyWrong) {
		t.Fatal(e)
	}
	if _, e := s.Protect(secretKey); e != nil {
		t.Fatal(e)
	}
	if _, e := s.Login(ctx, "admin", secret, code()); e != nil {
		t.Fatal(e)
	}
	if _, e := s.Login(ctx, "admin", secret, codes[1]); e != nil {
		t.Fatal(e)
	}
	// reset-2fa works without the key; afterwards no key is needed at all.
	s.key = nil
	if err = s.DisableTOTP(u.ID, ""); err != nil {
		t.Fatal(err)
	}
	if n, e := s.Protect(nil); n != 0 || e != nil {
		t.Fatal(n, e)
	}
}
