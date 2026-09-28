// Package identity owns durable accounts and revocable server-side sessions.
package identity

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"time"
	"unicode/utf8"

	"github.com/pbuzdygan/filedeck/internal/core"
	bolt "go.etcd.io/bbolt"
	"golang.org/x/crypto/argon2"
	"golang.org/x/sys/unix"
)

var (
	ErrAuth      = errors.New("invalid credentials or session")
	ErrInvalid   = errors.New("invalid account input")
	ErrExists    = errors.New("account or bootstrap already exists")
	ErrNotFound  = errors.New("account not found")
	ErrLastAdmin = errors.New("cannot disable or demote the last administrator")
	ErrBusy      = errors.New("authentication capacity exceeded")
)

const (
	SessionLifetime = 12 * time.Hour
	IdleLifetime    = 30 * time.Minute
	maxUsers        = 1000
	maxSessions     = 8
)

var names = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{2,63}$`)
var usersBucket = []byte("users")
var namesBucket = []byte("names")
var sessionsBucket = []byte("sessions")

type User struct {
	ID       string `json:"id"`
	Username string `json:"username"`
	Admin    bool   `json:"admin"`
	Disabled bool   `json:"disabled"`
	// Spaces maps a space name (or core.AnySpace) to granted permissions.
	Spaces map[string]core.Permission `json:"spaces"`
	// TwoFactor is derived from the stored account on every read.
	TwoFactor bool `json:"two_factor"`
	// Permissions is the pre-spaces format, read once and converted to "*".
	Permissions core.Permission `json:"permissions,omitempty"`
}

// normalize converts accounts stored before per-space permissions existed.
func (u *User) normalize() {
	if u.Spaces == nil {
		u.Spaces = make(map[string]core.Permission)
		p := u.Permissions
		// Administrators bootstrapped before Modify existed had every permission
		// of that version (List|Read|Create); keep them fully privileged.
		if u.Admin && p == core.List|core.Read|core.Create {
			p = core.AllPermissions
		}
		if p != 0 {
			u.Spaces[core.AnySpace] = p
		}
	}
	u.Permissions = 0
}
func cleanGrants(g map[string]core.Permission) map[string]core.Permission {
	out := make(map[string]core.Permission, len(g))
	for k, p := range g {
		if p != 0 {
			out[k] = p
		}
	}
	return out
}

type password struct {
	Algorithm string `json:"algorithm"`
	Salt      []byte `json:"salt"`
	Digest    []byte `json:"digest"`
}
type account struct {
	User     User          `json:"user"`
	Password password      `json:"password"`
	Version  uint64        `json:"version"`
	TOTP     *secondFactor `json:"totp,omitempty"`
}
type session struct {
	UserID   string    `json:"user_id"`
	Version  uint64    `json:"version"`
	Created  time.Time `json:"created"`
	Expires  time.Time `json:"expires"`
	LastSeen time.Time `json:"last_seen"`
}
type Login struct {
	User    User      `json:"user"`
	CSRF    string    `json:"csrf"`
	Expires time.Time `json:"expires"`
	Token   string    `json:"-"`
	// RecoveryUsed is set when this sign-in consumed a recovery code.
	RecoveryUsed bool `json:"recovery_used,omitempty"`
}
type Store struct {
	db      *bolt.DB
	dir     string
	gate    chan struct{}
	dummy   password
	now     func() time.Time
	strikes strikes
	// key protects two-factor secrets (see Protect); nil without one.
	key []byte
}

// openDir opens the private state directory without following symlinks.
func openDir(dir string) (int, error) {
	fd, err := unix.Openat2(unix.AT_FDCWD, dir, &unix.OpenHow{Flags: unix.O_RDONLY | unix.O_DIRECTORY | unix.O_CLOEXEC, Resolve: unix.RESOLVE_NO_SYMLINKS})
	if err != nil {
		return -1, err
	}
	var st unix.Stat_t
	if err = unix.Fstat(fd, &st); err != nil {
		unix.Close(fd)
		return -1, err
	}
	if st.Uid != uint32(os.Geteuid()) || st.Mode&0777 != 0700 {
		unix.Close(fd)
		return -1, errors.New("identity directory must be private, owned by process user, mode 0700")
	}
	return fd, nil
}

// openBolt opens a database file relative to the directory handle with
// O_NOFOLLOW and requires a private regular file without hardlinks.
func openBolt(fd int, dir, name string, extra int) (*bolt.DB, error) {
	return bolt.Open(filepath.Join(dir, name), 0600, &bolt.Options{Timeout: time.Second, OpenFile: func(_ string, flags int, mode os.FileMode) (*os.File, error) {
		n, e := unix.Openat(fd, name, flags|extra|unix.O_NOFOLLOW|unix.O_CLOEXEC|unix.O_NONBLOCK, uint32(mode.Perm()))
		if e != nil {
			return nil, e
		}
		f := os.NewFile(uintptr(n), name)
		var stat unix.Stat_t
		if e = unix.Fstat(n, &stat); e != nil {
			f.Close()
			return nil, e
		}
		if stat.Mode&unix.S_IFMT != unix.S_IFREG || stat.Mode&0777 != 0600 || stat.Uid != uint32(os.Geteuid()) || stat.Nlink != 1 {
			f.Close()
			return nil, errors.New("identity database must be a private regular file without hardlinks")
		}
		return f, nil
	}})
}

// Open requires an existing private directory. A descriptor-relative O_NOFOLLOW
// open prevents substitution of the database with a symlink or special file.
func Open(directory string) (*Store, error) {
	dir, err := filepath.Abs(directory)
	if err != nil {
		return nil, err
	}
	fd, err := openDir(dir)
	if err != nil {
		return nil, err
	}
	defer unix.Close(fd)
	db, err := openBolt(fd, dir, "identity.db", 0)
	if err != nil {
		return nil, err
	}
	err = db.Update(func(tx *bolt.Tx) error {
		meta, e := tx.CreateBucketIfNotExists([]byte("schema"))
		if e != nil {
			return e
		}
		version := meta.Get([]byte("version"))
		if version != nil && string(version) != "1" {
			return errors.New("unsupported identity schema")
		}
		for _, name := range [][]byte{usersBucket, namesBucket, sessionsBucket} {
			if _, e = tx.CreateBucketIfNotExists(name); e != nil {
				return e
			}
		}
		return meta.Put([]byte("version"), []byte("1"))
	})
	if err != nil {
		db.Close()
		return nil, err
	}
	if err = unix.Fsync(fd); err != nil {
		db.Close()
		return nil, err
	}
	s := &Store{db: db, dir: dir, gate: make(chan struct{}, 2), now: time.Now, strikes: strikes{list: make(map[string]strike)}}
	s.dummy = makePassword("filedeck-dummy-password-not-an-account")
	return s, nil
}
func (s *Store) Close() error { return s.db.Close() }
func validPassword(p string) bool {
	return utf8.ValidString(p) && utf8.RuneCountInString(p) >= 12 && len(p) <= 1024
}
func makePassword(p string) password {
	// crypto/rand.Text uses the operating system CSPRNG and panics on failure.
	salt := []byte(rand.Text())
	return password{Algorithm: "argon2id-v1", Salt: salt, Digest: argon2.IDKey([]byte(p), salt, 3, 32*1024, 1, 32)}
}
func matches(h password, p string) bool {
	if h.Algorithm != "argon2id-v1" || (len(h.Salt) < 16 || len(h.Salt) > 64) || len(h.Digest) != 32 {
		return false
	}
	actual := argon2.IDKey([]byte(p), h.Salt, 3, 32*1024, 1, 32)
	return subtle.ConstantTimeCompare(actual, h.Digest) == 1
}
func (s *Store) acquire(ctx context.Context) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	select {
	case s.gate <- struct{}{}:
		return func() { <-s.gate }, nil
	default:
		return nil, ErrBusy
	}
}
func put(b *bolt.Bucket, key string, value any) error {
	data, e := json.Marshal(value)
	if e != nil {
		return e
	}
	return b.Put([]byte(key), data)
}
func readAccount(tx *bolt.Tx, id string) (account, error) {
	var a account
	data := tx.Bucket(usersBucket).Get([]byte(id))
	if data == nil {
		return a, ErrNotFound
	}
	err := json.Unmarshal(data, &a)
	a.User.normalize()
	a.User.TwoFactor = a.TOTP != nil
	return a, err
}

func (s *Store) create(ctx context.Context, name, secret string, admin bool, grants map[string]core.Permission, bootstrap bool) (User, error) {
	if !names.MatchString(name) || !validPassword(secret) || !core.ValidGrants(grants) {
		return User{}, ErrInvalid
	}
	release, err := s.acquire(ctx)
	if err != nil {
		return User{}, err
	}
	defer release()
	a := account{User: User{ID: rand.Text(), Username: name, Admin: admin, Spaces: cleanGrants(grants)}, Password: makePassword(secret), Version: 1}
	err = s.db.Update(func(tx *bolt.Tx) error {
		if e := ctx.Err(); e != nil {
			return e
		}
		users := tx.Bucket(usersBucket)
		index := tx.Bucket(namesBucket)
		if (bootstrap && users.Stats().KeyN != 0) || index.Get([]byte(name)) != nil {
			return ErrExists
		}
		if users.Stats().KeyN >= maxUsers {
			return ErrBusy
		}
		if e := put(users, a.User.ID, a); e != nil {
			return e
		}
		return index.Put([]byte(name), []byte(a.User.ID))
	})
	return a.User, err
}

// Bootstrap creates the first administrator with every permission on every space.
func (s *Store) Bootstrap(ctx context.Context, name, secret string) (User, error) {
	return s.create(ctx, name, secret, true, map[string]core.Permission{core.AnySpace: core.AllPermissions}, true)
}
func (s *Store) Create(ctx context.Context, name, secret string, admin bool, grants map[string]core.Permission) (User, error) {
	return s.create(ctx, name, secret, admin, grants, false)
}
func (s *Store) Users() ([]User, error) {
	list := make([]User, 0)
	err := s.db.View(func(tx *bolt.Tx) error {
		return tx.Bucket(usersBucket).ForEach(func(_, v []byte) error {
			var a account
			if e := json.Unmarshal(v, &a); e != nil {
				return e
			}
			a.User.normalize()
			a.User.TwoFactor = a.TOTP != nil
			list = append(list, a.User)
			return nil
		})
	})
	sort.Slice(list, func(i, j int) bool { return list[i].Username < list[j].Username })
	return list, err
}
func (s *Store) find(name string) (account, error) {
	var a account
	err := s.db.View(func(tx *bolt.Tx) error {
		var e error
		a, e = readAccount(tx, string(tx.Bucket(namesBucket).Get([]byte(name))))
		return e
	})
	return a, err
}
func tokenKey(token string) (string, bool) {
	b, e := base64.RawURLEncoding.DecodeString(token)
	if e != nil || len(b) != 32 {
		return "", false
	}
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:]), true
}
func csrf(token string) string {
	m := hmac.New(sha256.New, []byte(token))
	m.Write([]byte("filedeck-csrf-v1"))
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}
func ValidCSRF(token, value string) bool {
	return len(value) == 43 && hmac.Equal([]byte(csrf(token)), []byte(value))
}
func expired(se session, now time.Time) bool {
	return !now.Before(se.Expires) || !now.Before(se.LastSeen.Add(IdleLifetime))
}

// Login checks the password and, for accounts with two-factor authentication,
// the code (TOTP or recovery code). Without a code such an account gets
// ErrTOTPRequired, which is only returned after a correct password.
func (s *Store) Login(ctx context.Context, name, secret, code string) (Login, error) {
	if len(secret) > 1024 || len(code) > 64 {
		return Login{}, ErrAuth
	}
	release, err := s.acquire(ctx)
	if err != nil {
		return Login{}, err
	}
	defer release()
	snapshot, err := s.find(name)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return Login{}, err
	}
	h := snapshot.Password
	if errors.Is(err, ErrNotFound) {
		h = s.dummy
	}
	valid := matches(h, secret)
	if !valid || snapshot.User.ID == "" || snapshot.User.Disabled {
		return Login{}, ErrAuth
	}
	if snapshot.TOTP != nil {
		if code == "" {
			return Login{}, ErrTOTPRequired
		}
		if s.strikes.locked(snapshot.User.ID, s.now()) {
			return Login{}, ErrTOTPLocked
		}
	}
	recoveryUsed := false
	raw := make([]byte, 32)
	if _, err = rand.Read(raw); err != nil {
		return Login{}, err
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	key, _ := tokenKey(token)
	now := s.now()
	se := session{UserID: snapshot.User.ID, Version: snapshot.Version, Created: now, LastSeen: now, Expires: now.Add(SessionLifetime)}
	err = s.db.Update(func(tx *bolt.Tx) error {
		if e := ctx.Err(); e != nil {
			return e
		}
		current, e := readAccount(tx, snapshot.User.ID)
		if e != nil {
			return e
		}
		if current.Version != snapshot.Version || current.User.Disabled {
			return ErrAuth
		}
		if current.TOTP != nil {
			before := len(current.TOTP.Recovery)
			ok, e := s.useCode(current.TOTP, current.User.ID, code, now)
			if e != nil {
				return e
			}
			if !ok {
				return ErrTOTPInvalid
			}
			recoveryUsed = len(current.TOTP.Recovery) < before
			// The code is consumed without a new version: sessions stay.
			if e = put(tx.Bucket(usersBucket), current.User.ID, current); e != nil {
				return e
			}
		}
		bucket := tx.Bucket(sessionsBucket)
		type existing struct {
			key     string
			created time.Time
		}
		var own []existing
		var remove []string
		if e = bucket.ForEach(func(k, v []byte) error {
			var item session
			if er := json.Unmarshal(v, &item); er != nil {
				return er
			}
			if expired(item, now) {
				remove = append(remove, string(k))
			} else if item.UserID == se.UserID {
				own = append(own, existing{string(k), item.Created})
			}
			return nil
		}); e != nil {
			return e
		}
		sort.Slice(own, func(i, j int) bool { return own[i].created.Before(own[j].created) })
		for len(own) >= maxSessions {
			remove = append(remove, own[0].key)
			own = own[1:]
		}
		for _, k := range remove {
			if e = bucket.Delete([]byte(k)); e != nil {
				return e
			}
		}
		return put(bucket, key, se)
	})
	if snapshot.TOTP != nil {
		if errors.Is(err, ErrTOTPInvalid) {
			s.strikes.fail(snapshot.User.ID, now)
		} else if err == nil {
			s.strikes.clear(snapshot.User.ID)
		}
	}
	if err != nil {
		return Login{}, err
	}
	return Login{User: snapshot.User, Token: token, CSRF: csrf(token), Expires: se.Expires, RecoveryUsed: recoveryUsed}, nil
}

func (s *Store) Authenticate(token string) (Login, error) {
	key, ok := tokenKey(token)
	if !ok {
		return Login{}, ErrAuth
	}
	var result Login
	err := s.db.Update(func(tx *bolt.Tx) error {
		data := tx.Bucket(sessionsBucket).Get([]byte(key))
		if data == nil {
			return ErrAuth
		}
		var se session
		if e := json.Unmarshal(data, &se); e != nil {
			return e
		}
		if expired(se, s.now()) {
			return ErrAuth
		}
		a, e := readAccount(tx, se.UserID)
		if e != nil {
			return ErrAuth
		}
		if a.User.Disabled || a.Version != se.Version {
			return ErrAuth
		}
		se.LastSeen = s.now()
		if e = put(tx.Bucket(sessionsBucket), key, se); e != nil {
			return e
		}
		result = Login{User: a.User, Token: token, CSRF: csrf(token), Expires: se.Expires}
		return nil
	})
	return result, err
}
func (s *Store) Logout(token string) error {
	key, ok := tokenKey(token)
	if !ok {
		return ErrAuth
	}
	return s.db.Update(func(tx *bolt.Tx) error { return tx.Bucket(sessionsBucket).Delete([]byte(key)) })
}
func revoke(tx *bolt.Tx, id string) error {
	b := tx.Bucket(sessionsBucket)
	var keys [][]byte
	if err := b.ForEach(func(k, v []byte) error {
		var se session
		if e := json.Unmarshal(v, &se); e != nil {
			return e
		}
		if se.UserID == id {
			keys = append(keys, append([]byte(nil), k...))
		}
		return nil
	}); err != nil {
		return err
	}
	for _, k := range keys {
		if err := b.Delete(k); err != nil {
			return err
		}
	}
	return nil
}
func (s *Store) VerifyPassword(ctx context.Context, id, secret string) error {
	if len(secret) > 1024 {
		return ErrAuth
	}
	release, err := s.acquire(ctx)
	if err != nil {
		return err
	}
	defer release()
	var a account
	err = s.db.View(func(tx *bolt.Tx) error { var e error; a, e = readAccount(tx, id); return e })
	if err != nil {
		return err
	}
	if a.User.Disabled || !matches(a.Password, secret) {
		return ErrAuth
	}
	return nil
}

// ChangePassword verifies and replaces credentials in one version-checked operation.
func (s *Store) ChangePassword(ctx context.Context, id, current, next string) error {
	if !validPassword(next) || len(current) > 1024 {
		return ErrInvalid
	}
	release, err := s.acquire(ctx)
	if err != nil {
		return err
	}
	defer release()
	var snapshot account
	err = s.db.View(func(tx *bolt.Tx) error { var e error; snapshot, e = readAccount(tx, id); return e })
	if err != nil {
		return err
	}
	if snapshot.User.Disabled || !matches(snapshot.Password, current) {
		return ErrAuth
	}
	replacement := makePassword(next)
	return s.db.Update(func(tx *bolt.Tx) error {
		if e := ctx.Err(); e != nil {
			return e
		}
		a, e := readAccount(tx, id)
		if e != nil {
			return e
		}
		if a.Version != snapshot.Version || a.User.Disabled {
			return ErrAuth
		}
		a.Password = replacement
		a.Version++
		if e = put(tx.Bucket(usersBucket), id, a); e != nil {
			return e
		}
		return revoke(tx, id)
	})
}

// ResetPassword is for an already authorized administrator or local operator.
func (s *Store) ResetPassword(ctx context.Context, id, next string) error {
	if !validPassword(next) {
		return ErrInvalid
	}
	release, err := s.acquire(ctx)
	if err != nil {
		return err
	}
	defer release()
	h := makePassword(next)
	return s.db.Update(func(tx *bolt.Tx) error {
		if e := ctx.Err(); e != nil {
			return e
		}
		a, e := readAccount(tx, id)
		if e != nil {
			return e
		}
		a.Password = h
		a.Version++
		if e = put(tx.Bucket(usersBucket), id, a); e != nil {
			return e
		}
		return revoke(tx, id)
	})
}
func (s *Store) Update(id string, admin, disabled bool, grants map[string]core.Permission) (User, error) {
	if !core.ValidGrants(grants) {
		return User{}, ErrInvalid
	}
	var result User
	err := s.db.Update(func(tx *bolt.Tx) error {
		a, e := readAccount(tx, id)
		if e != nil {
			return e
		}
		if a.User.Admin && !a.User.Disabled && (!admin || disabled) {
			if e = requireAnotherAdmin(tx); e != nil {
				return e
			}
		}
		a.User.Admin = admin
		a.User.Disabled = disabled
		a.User.Spaces = cleanGrants(grants)
		a.Version++
		if e = put(tx.Bucket(usersBucket), id, a); e != nil {
			return e
		}
		if e = revoke(tx, id); e != nil {
			return e
		}
		result = a.User
		return nil
	})
	return result, err
}

// requireAnotherAdmin fails unless more than one active administrator exists.
func requireAnotherAdmin(tx *bolt.Tx) error {
	admins := 0
	if e := tx.Bucket(usersBucket).ForEach(func(_, v []byte) error {
		var other account
		if er := json.Unmarshal(v, &other); er != nil {
			return er
		}
		if other.User.Admin && !other.User.Disabled {
			admins++
		}
		return nil
	}); e != nil {
		return e
	}
	if admins <= 1 {
		return ErrLastAdmin
	}
	return nil
}

// Delete removes an account, its name and all its sessions in one transaction.
// The last active administrator cannot be deleted. The caller must also clear
// the account's file permissions (which removes its public links).
func (s *Store) Delete(id string) (User, error) {
	var removed User
	err := s.db.Update(func(tx *bolt.Tx) error {
		a, e := readAccount(tx, id)
		if e != nil {
			return e
		}
		if a.User.Admin && !a.User.Disabled {
			if e = requireAnotherAdmin(tx); e != nil {
				return e
			}
		}
		if e = revoke(tx, id); e != nil {
			return e
		}
		if e = tx.Bucket(namesBucket).Delete([]byte(a.User.Username)); e != nil {
			return e
		}
		removed = a.User
		return tx.Bucket(usersBucket).Delete([]byte(id))
	})
	return removed, err
}

func (s *Store) RequireInitialized() error {
	users, e := s.Users()
	if e != nil {
		return e
	}
	for _, u := range users {
		if u.Admin && !u.Disabled {
			return nil
		}
	}
	return fmt.Errorf("no active administrator: run bootstrap before serving")
}
