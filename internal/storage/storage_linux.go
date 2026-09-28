// Package storage implements the Linux storage boundary. A Space is one
// directory tree exposed to users; its private metadata (staging, trash) lives
// in a hidden directory inside the same tree, so publication and trash moves
// are single atomic renames on one filesystem.
package storage

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"golang.org/x/sys/unix"
)

var (
	ErrPath     = errors.New("invalid relative path")
	ErrType     = errors.New("only regular files and directories are supported")
	ErrConflict = errors.New("destination already exists")
	ErrLimit    = errors.New("directory entry limit exceeded")
	ErrReadOnly = errors.New("space is read-only")
)

// MetaDir is the reserved per-space directory for staging and trash.
const MetaDir = ".filedeck"

// Modes are applied to files and directories created for users. The process
// umask still applies unless the caller clears it.
type Modes struct {
	File os.FileMode
	Dir  os.FileMode
}

func DefaultModes() Modes { return Modes{File: 0640, Dir: 0750} }

func (m Modes) Valid() bool {
	return m.File&^0777 == 0 && m.Dir&^0777 == 0 && m.File&0600 == 0600 && m.Dir&0700 == 0700
}

// Space owns directory capabilities for one exposed tree. The operator must not
// move an opened directory across trust boundaries (see CONTRACT.md).
type Space struct {
	mu                   sync.RWMutex
	root                 *os.File
	meta, staging, trash *os.File // nil for a read-only space
	metaDev, metaIno     uint64
	hasMeta              bool
	readOnly             bool
	modes                Modes
	closed               bool
}

type Entry struct {
	Name      string    `json:"name"`
	Directory bool      `json:"directory"`
	Size      int64     `json:"size"`
	Modified  time.Time `json:"modified"`
}

func mtime(st *unix.Stat_t) time.Time { return time.Unix(st.Mtim.Sec, st.Mtim.Nsec).UTC() }

func ValidPath(name string, allowRoot bool) bool {
	if name == "." {
		return allowRoot
	}
	if !utf8.ValidString(name) || len(name) > 4096 || !fs.ValidPath(name) || strings.ContainsAny(name, "\\\x00") {
		return false
	}
	parts := strings.Split(name, "/")
	if len(parts) > 128 {
		return false
	}
	for _, p := range parts {
		if len(p) > 255 {
			return false
		}
	}
	return true
}

// ValidUserPath accepts a non-root path that does not start in the metadata
// directory; operations check it early, before doing any work.
func ValidUserPath(name string) bool {
	first, _, _ := strings.Cut(name, "/")
	return ValidPath(name, false) && !Reserved(first)
}

// Reserved reports whether a path segment names the metadata directory,
// including case variants and the trailing dots/spaces SMB servers ignore.
func Reserved(segment string) bool {
	return strings.EqualFold(strings.TrimRight(segment, ". "), MetaDir)
}

func openDir(name string) (*os.File, error) {
	fd, err := unix.Openat2(unix.AT_FDCWD, name, &unix.OpenHow{
		Flags:   unix.O_RDONLY | unix.O_DIRECTORY | unix.O_CLOEXEC,
		Resolve: unix.RESOLVE_NO_SYMLINKS | unix.RESOLVE_NO_MAGICLINKS,
	})
	if err != nil {
		return nil, fmt.Errorf("open directory %s (openat2 required): %w", name, err)
	}
	return os.NewFile(uintptr(fd), name), nil
}

func mountID(fd int) (uint64, error) {
	var sx unix.Statx_t
	if err := unix.Statx(fd, "", unix.AT_EMPTY_PATH, unix.STATX_MNT_ID, &sx); err != nil {
		return 0, err
	}
	if sx.Mask&unix.STATX_MNT_ID == 0 {
		return 0, errors.New("statx mount id unsupported")
	}
	return sx.Mnt_id, nil
}

// privateDir opens (creating if missing) a metadata directory below parent,
// without following symlinks or crossing mounts. It must be owned by the
// process and not writable by group or others.
func privateDir(parent *os.File, name string, mnt uint64) (*os.File, error) {
	if err := unix.Mkdirat(int(parent.Fd()), name, 0700); err != nil && !errors.Is(err, unix.EEXIST) {
		return nil, err
	}
	fd, err := unix.Openat2(int(parent.Fd()), name, &unix.OpenHow{
		Flags:   unix.O_RDONLY | unix.O_DIRECTORY | unix.O_CLOEXEC | unix.O_NOFOLLOW,
		Resolve: unix.RESOLVE_BENEATH | unix.RESOLVE_NO_SYMLINKS | unix.RESOLVE_NO_XDEV,
	})
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), name)
	var st unix.Stat_t
	if err = unix.Fstat(fd, &st); err != nil {
		f.Close()
		return nil, err
	}
	m, err := mountID(fd)
	if err != nil {
		f.Close()
		return nil, err
	}
	if st.Uid != uint32(os.Geteuid()) || st.Mode&0022 != 0 || m != mnt {
		f.Close()
		return nil, fmt.Errorf("%s must be owned by uid %d, not group/world-writable and on the same mount (found uid %d, mode %o)", name, os.Geteuid(), st.Uid, st.Mode&0777)
	}
	return f, nil
}

// OpenSpace opens an existing directory. A read-only filesystem, or a root the
// process cannot write, yields a read-only space; otherwise the metadata
// directory is created or verified.
func OpenSpace(dir string, modes Modes) (_ *Space, err error) {
	if !modes.Valid() {
		return nil, errors.New("invalid file/directory modes")
	}
	dir, err = filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	if dir == "/" {
		return nil, errors.New("space cannot be /")
	}
	root, err := openDir(dir)
	if err != nil {
		return nil, err
	}
	s := &Space{root: root, modes: modes}
	defer func() {
		if err != nil {
			s.Close()
		}
	}()
	mnt, err := mountID(int(root.Fd()))
	if err != nil {
		return nil, err
	}
	var sfs unix.Statfs_t
	if err = unix.Fstatfs(int(root.Fd()), &sfs); err != nil {
		return nil, err
	}
	if sfs.Flags&unix.ST_RDONLY != 0 || unix.Faccessat(int(root.Fd()), ".", unix.W_OK, unix.AT_EACCESS) != nil {
		s.readOnly = true
	}
	if s.readOnly {
		// Still recognize an existing metadata directory so it stays hidden.
		var st unix.Stat_t
		if unix.Fstatat(int(root.Fd()), MetaDir, &st, unix.AT_SYMLINK_NOFOLLOW) == nil {
			s.metaDev, s.metaIno, s.hasMeta = st.Dev, st.Ino, true
		}
		return s, nil
	}
	if s.meta, err = privateDir(root, MetaDir, mnt); err != nil {
		return nil, fmt.Errorf("space %s: %w", dir, err)
	}
	var st unix.Stat_t
	if err = unix.Fstat(int(s.meta.Fd()), &st); err != nil {
		return nil, err
	}
	s.metaDev, s.metaIno, s.hasMeta = st.Dev, st.Ino, true
	if s.staging, err = privateDir(s.meta, "staging", mnt); err != nil {
		return nil, fmt.Errorf("space %s: %w", dir, err)
	}
	if s.trash, err = privateDir(s.meta, "trash", mnt); err != nil {
		return nil, fmt.Errorf("space %s: %w", dir, err)
	}
	return s, nil
}

// MinFreeBytes is kept free on every writable space: writes that would leave
// less are refused (ENOSPC) so that no user can fill the disk completely.
var MinFreeBytes int64 = 512 << 20

// Reserve fails with ENOSPC unless size bytes fit above MinFreeBytes.
func (s *Space) Reserve(size int64) error {
	if s.readOnly {
		return ErrReadOnly
	}
	return reserveAt(int(s.staging.Fd()), size)
}
func reserveAt(fd int, size int64) error {
	var st unix.Statfs_t
	if err := unix.Fstatfs(fd, &st); err != nil {
		return err
	}
	if avail := int64(st.Bavail) * st.Bsize; avail-size < MinFreeBytes {
		return fmt.Errorf("%w: %d bytes available, %d requested, %d reserved", unix.ENOSPC, avail, size, MinFreeBytes)
	}
	return nil
}

func (s *Space) ReadOnly() bool        { return s.readOnly }
func (s *Space) FileMode() os.FileMode { return s.modes.File }

func (s *Space) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	var err error
	for _, f := range []*os.File{s.trash, s.staging, s.meta, s.root} {
		if f != nil {
			err = errors.Join(err, f.Close())
		}
	}
	return err
}

// guard rejects paths into the metadata directory, by name and by identity of
// the first segment (catching aliases such as SMB case-folding or 8.3 names).
func (s *Space) guard(name string) error {
	if name == "." {
		return nil
	}
	first, _, _ := strings.Cut(name, "/")
	if Reserved(first) {
		return ErrPath
	}
	if !s.hasMeta {
		return nil
	}
	var st unix.Stat_t
	err := unix.Fstatat(int(s.root.Fd()), first, &st, unix.AT_SYMLINK_NOFOLLOW)
	if err == nil && st.Dev == s.metaDev && st.Ino == s.metaIno {
		return ErrPath
	}
	return nil
}

func (s *Space) open(name string, flags int) (*os.File, error) {
	if s.closed {
		return nil, fs.ErrClosed
	}
	if !ValidPath(name, true) {
		return nil, ErrPath
	}
	if err := s.guard(name); err != nil {
		return nil, err
	}
	fd, err := unix.Openat2(int(s.root.Fd()), name, &unix.OpenHow{
		Flags:   uint64(flags | unix.O_CLOEXEC | unix.O_NOFOLLOW),
		Resolve: unix.RESOLVE_BENEATH | unix.RESOLVE_NO_SYMLINKS | unix.RESOLVE_NO_XDEV,
	})
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(fd), name), nil
}

// parent opens the directory containing target and returns the final name.
func (s *Space) parent(target string) (*os.File, string, error) {
	if !ValidPath(target, false) {
		return nil, "", ErrPath
	}
	if err := s.guard(target); err != nil {
		return nil, "", err
	}
	dir, err := s.open(path.Dir(target), unix.O_RDONLY|unix.O_DIRECTORY)
	if err != nil {
		return nil, "", err
	}
	return dir, path.Base(target), nil
}

func (s *Space) writable() error {
	if s.closed {
		return fs.ErrClosed
	}
	if s.readOnly {
		return ErrReadOnly
	}
	return nil
}

// Read inspects an O_PATH capability before opening for data, so a FIFO or
// device driver is never opened for I/O. Reopening via the trusted Linux procfs
// binds the read to that same object, even if its directory entry was replaced.
func (s *Space) Read(name string) (*os.File, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if !ValidPath(name, false) {
		return nil, ErrPath
	}
	handle, err := s.open(name, unix.O_PATH)
	if err != nil {
		return nil, err
	}
	defer handle.Close()
	st, err := handle.Stat()
	if err != nil {
		return nil, err
	}
	if !st.Mode().IsRegular() {
		return nil, ErrType
	}
	fd, err := unix.Open(fmt.Sprintf("/proc/self/fd/%d", handle.Fd()), unix.O_RDONLY|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), name)
	actual, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	if !os.SameFile(st, actual) || !actual.Mode().IsRegular() {
		f.Close()
		return nil, ErrType
	}
	return f, nil
}

// Stat describes one regular file or directory without following symlinks.
func (s *Space) Stat(name string) (Entry, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	dir, base, err := s.parent(name)
	if err != nil {
		return Entry{}, err
	}
	defer dir.Close()
	return entryAt(dir, base)
}

func entryAt(dir *os.File, base string) (Entry, error) {
	var st unix.Stat_t
	if err := unix.Fstatat(int(dir.Fd()), base, &st, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return Entry{}, err
	}
	kind := st.Mode & unix.S_IFMT
	if kind != unix.S_IFREG && kind != unix.S_IFDIR {
		return Entry{}, ErrType
	}
	return Entry{Name: base, Directory: kind == unix.S_IFDIR, Size: st.Size, Modified: mtime(&st)}, nil
}

// List returns a bounded, unsorted listing. It fails rather than silently
// returning a misleading partial list. Symlinks, special files and the
// metadata directory are omitted.
func (s *Space) List(ctx context.Context, name string, limit int) ([]Entry, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if limit < 1 || limit > 10000 {
		return nil, ErrLimit
	}
	f, err := s.open(name, unix.O_RDONLY|unix.O_DIRECTORY)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var self unix.Stat_t
	if err = unix.Fstat(int(f.Fd()), &self); err != nil {
		return nil, err
	}
	entries := make([]Entry, 0)
	scanned := 0
	for {
		if err = ctx.Err(); err != nil {
			return nil, err
		}
		batch, e := f.ReadDir(128)
		for _, entry := range batch {
			scanned++
			if scanned > limit {
				return nil, ErrLimit
			}
			// fstatat is relative to the directory capability, never an absolute path
			// reconstructed from the original (possibly renamed) pathname.
			var st unix.Stat_t
			if err = unix.Fstatat(int(f.Fd()), entry.Name(), &st, unix.AT_SYMLINK_NOFOLLOW); err != nil {
				if errors.Is(err, unix.ENOENT) {
					continue
				}
				return nil, err
			}
			kind := st.Mode & unix.S_IFMT
			if kind != unix.S_IFREG && kind != unix.S_IFDIR {
				continue
			}
			if Reserved(entry.Name()) || (s.hasMeta && st.Dev == s.metaDev && st.Ino == s.metaIno) {
				continue
			}
			// Do not expose nested mounts: they could not be opened anyway.
			if kind == unix.S_IFDIR && st.Dev != self.Dev {
				continue
			}
			entries = append(entries, Entry{Name: entry.Name(), Directory: kind == unix.S_IFDIR, Size: st.Size, Modified: mtime(&st)})
		}
		if errors.Is(e, io.EOF) {
			return entries, nil
		}
		if e != nil {
			return nil, e
		}
	}
}

// ---------- staging ----------

func stagingName(name string) bool { return tokenName(name, "upload-", ".part") }
func tokenName(name, prefix, suffix string) bool {
	if len(name) != len(prefix)+64+len(suffix) || !strings.HasPrefix(name, prefix) || !strings.HasSuffix(name, suffix) {
		return false
	}
	raw := strings.TrimSuffix(strings.TrimPrefix(name, prefix), suffix)
	b, err := hex.DecodeString(raw)
	return err == nil && len(b) == 32 && raw == strings.ToLower(raw)
}

// StagingName reports whether name is a staging file name created by this package.
func StagingName(name string) bool { return stagingName(name) }

func (s *Space) CreateStaging() (string, *os.File, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.writable(); err != nil {
		return "", nil, err
	}
	var token [32]byte
	if _, err := rand.Read(token[:]); err != nil {
		return "", nil, err
	}
	name := "upload-" + hex.EncodeToString(token[:]) + ".part"
	fd, err := unix.Openat(int(s.staging.Fd()), name, unix.O_CREAT|unix.O_EXCL|unix.O_RDWR|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if err != nil {
		return "", nil, err
	}
	return name, os.NewFile(uintptr(fd), name), nil
}

func (s *Space) RemoveStaging(name string) error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.writable(); err != nil {
		return err
	}
	if !stagingName(name) {
		return ErrPath
	}
	err := unix.Unlinkat(int(s.staging.Fd()), name, 0)
	if errors.Is(err, unix.ENOENT) {
		return nil
	}
	return err
}

// OpenStaging reopens an existing staging file after a restart. Modes are not
// checked: on SMB mounts they are fixed by mount options; the staging directory
// itself is private.
func (s *Space) OpenStaging(name string) (*os.File, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.writable(); err != nil {
		return nil, err
	}
	if !stagingName(name) {
		return nil, ErrPath
	}
	fd, err := unix.Openat(int(s.staging.Fd()), name, unix.O_RDWR|unix.O_NOFOLLOW|unix.O_CLOEXEC|unix.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), name)
	var st unix.Stat_t
	if err = unix.Fstat(fd, &st); err != nil {
		f.Close()
		return nil, err
	}
	if st.Mode&unix.S_IFMT != unix.S_IFREG || st.Uid != uint32(os.Geteuid()) || st.Nlink != 1 {
		f.Close()
		return nil, errors.New("staging file must be a regular file owned by the process without hardlinks")
	}
	return f, nil
}

// StagingExists distinguishes a missing staging entry from other errors.
func (s *Space) StagingExists(name string) (bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.writable(); err != nil {
		return false, err
	}
	if !stagingName(name) {
		return false, ErrPath
	}
	var st unix.Stat_t
	err := unix.Fstatat(int(s.staging.Fd()), name, &st, unix.AT_SYMLINK_NOFOLLOW)
	if errors.Is(err, unix.ENOENT) {
		return false, nil
	}
	return err == nil, err
}

func names(dir *os.File, max int) ([]string, error) {
	fd, err := unix.Openat(int(dir.Fd()), ".", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	d := os.NewFile(uintptr(fd), "dir")
	defer d.Close()
	list, err := d.Readdirnames(max + 1)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	if len(list) > max {
		return nil, ErrLimit
	}
	return list, nil
}

// Recover discards recognized staging files not listed in keep. Resumable
// uploads are owned by the caller's durable records.
func (s *Space) Recover(keep map[string]bool) (int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.readOnly {
		return 0, nil
	}
	if err := s.writable(); err != nil {
		return 0, err
	}
	list, err := names(s.staging, 10000)
	if err != nil {
		return 0, err
	}
	count := 0
	var dev unix.Stat_t
	if err = unix.Fstat(int(s.staging.Fd()), &dev); err != nil {
		return 0, err
	}
	for _, name := range list {
		// Interrupted copies are never resumed: remove their partial trees.
		if copyName(name) {
			budget := 1000000
			if err = removeTree(int(s.staging.Fd()), name, dev.Dev, 0, &budget); err != nil {
				return count, err
			}
			count++
			continue
		}
		if !stagingName(name) || keep[name] {
			continue
		}
		var st unix.Stat_t
		if err = unix.Fstatat(int(s.staging.Fd()), name, &st, unix.AT_SYMLINK_NOFOLLOW); err != nil {
			return count, err
		}
		if st.Mode&unix.S_IFMT != unix.S_IFREG || st.Nlink != 1 {
			return count, errors.New("unexpected staging type or hardlink; refusing cleanup")
		}
		if err = unix.Unlinkat(int(s.staging.Fd()), name, 0); err != nil {
			return count, err
		}
		count++
	}
	if count > 0 {
		err = s.staging.Sync()
	}
	return count, err
}

// Publish atomically creates a new directory entry, refusing any existing
// destination. No overwrite fallback is allowed. A successfully published
// file is not rolled back if the subsequent directory sync fails.
func (s *Space) Publish(staged, target string) (published bool, err error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err = s.writable(); err != nil {
		return false, err
	}
	if !stagingName(staged) && !copyName(staged) {
		return false, ErrPath
	}
	parent, base, err := s.parent(target)
	if err != nil {
		return false, err
	}
	defer parent.Close()
	err = unix.Renameat2(int(s.staging.Fd()), staged, int(parent.Fd()), base, unix.RENAME_NOREPLACE)
	if errors.Is(err, unix.EEXIST) || errors.Is(err, unix.ENOTEMPTY) {
		return false, ErrConflict
	}
	if err != nil {
		return false, err
	}
	return true, errors.Join(parent.Sync(), s.staging.Sync())
}

// SyncTarget retries the directory syncs that confirm a publication.
func (s *Space) SyncTarget(target string) error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.writable(); err != nil {
		return err
	}
	parent, _, err := s.parent(target)
	if err != nil {
		return err
	}
	defer parent.Close()
	return errors.Join(parent.Sync(), s.staging.Sync())
}

// ---------- directory operations ----------

// Mkdir creates one new directory below an existing parent. It never creates
// intermediate directories and never follows symlinks in the parent path.
func (s *Space) Mkdir(target string) error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.writable(); err != nil {
		return err
	}
	parent, base, err := s.parent(target)
	if err != nil {
		return err
	}
	defer parent.Close()
	err = unix.Mkdirat(int(parent.Fd()), base, uint32(s.modes.Dir))
	if errors.Is(err, unix.EEXIST) {
		return ErrConflict
	}
	if err != nil {
		return err
	}
	return parent.Sync()
}

func renameErr(err error) error {
	switch {
	case errors.Is(err, unix.EEXIST), errors.Is(err, unix.ENOTEMPTY):
		return ErrConflict
	case errors.Is(err, unix.EINVAL):
		return ErrPath // e.g. moving a directory into itself
	}
	return err
}

// Rename moves a regular file or directory within the space, never replacing
// an existing destination.
func (s *Space) Rename(from, to string) error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.writable(); err != nil {
		return err
	}
	src, srcBase, err := s.parent(from)
	if err != nil {
		return err
	}
	defer src.Close()
	dst, dstBase, err := s.parent(to)
	if err != nil {
		return err
	}
	defer dst.Close()
	if _, err = entryAt(src, srcBase); err != nil {
		return err
	}
	if err = unix.Renameat2(int(src.Fd()), srcBase, int(dst.Fd()), dstBase, unix.RENAME_NOREPLACE); err != nil {
		return renameErr(err)
	}
	return errors.Join(src.Sync(), dst.Sync())
}

// ---------- trash ----------

// TrashName reports whether name is a trash item name ("item-<64 hex>").
func TrashName(name string) bool { return tokenName(name, "item-", "") }

// Trash moves a regular file or directory into the space's trash under item.
func (s *Space) Trash(from, item string) (Entry, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.writable(); err != nil {
		return Entry{}, err
	}
	if !TrashName(item) {
		return Entry{}, ErrPath
	}
	src, base, err := s.parent(from)
	if err != nil {
		return Entry{}, err
	}
	defer src.Close()
	entry, err := entryAt(src, base)
	if err != nil {
		return Entry{}, err
	}
	if err = unix.Renameat2(int(src.Fd()), base, int(s.trash.Fd()), item, unix.RENAME_NOREPLACE); err != nil {
		return Entry{}, renameErr(err)
	}
	return entry, errors.Join(src.Sync(), s.trash.Sync())
}

// Restore moves a trash item back to target, never replacing anything.
func (s *Space) Restore(item, target string) error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.writable(); err != nil {
		return err
	}
	if !TrashName(item) {
		return ErrPath
	}
	dst, base, err := s.parent(target)
	if err != nil {
		return err
	}
	defer dst.Close()
	if err = unix.Renameat2(int(s.trash.Fd()), item, int(dst.Fd()), base, unix.RENAME_NOREPLACE); err != nil {
		return renameErr(err)
	}
	return errors.Join(dst.Sync(), s.trash.Sync())
}

// TrashEntry describes a trash item without following symlinks.
func (s *Space) TrashEntry(item string) (Entry, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.writable(); err != nil {
		return Entry{}, err
	}
	if !TrashName(item) {
		return Entry{}, ErrPath
	}
	return entryAt(s.trash, item)
}

// TrashItems lists recognized item names currently in the trash.
func (s *Space) TrashItems() ([]string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.readOnly {
		return nil, nil
	}
	if err := s.writable(); err != nil {
		return nil, err
	}
	list, err := names(s.trash, 100000)
	if err != nil {
		return nil, err
	}
	out := list[:0]
	for _, n := range list {
		if TrashName(n) {
			out = append(out, n)
		}
	}
	return out, nil
}

// Purge permanently removes one trash item. The walk is relative to directory
// descriptors, never follows symlinks and never crosses into another device.
func (s *Space) Purge(item string) error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.writable(); err != nil {
		return err
	}
	if !TrashName(item) {
		return ErrPath
	}
	var st unix.Stat_t
	if err := unix.Fstat(int(s.trash.Fd()), &st); err != nil {
		return err
	}
	budget := 1000000
	if err := removeTree(int(s.trash.Fd()), item, st.Dev, 0, &budget); err != nil {
		return err
	}
	return s.trash.Sync()
}

func removeTree(dirfd int, name string, dev uint64, depth int, budget *int) error {
	if *budget--; *budget < 0 {
		return ErrLimit
	}
	var st unix.Stat_t
	if err := unix.Fstatat(dirfd, name, &st, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		if errors.Is(err, unix.ENOENT) {
			return nil
		}
		return err
	}
	if st.Mode&unix.S_IFMT != unix.S_IFDIR {
		return unix.Unlinkat(dirfd, name, 0)
	}
	if st.Dev != dev {
		return errors.New("refusing to purge across a mount point")
	}
	if depth > 256 {
		return ErrLimit
	}
	fd, err := unix.Openat(dirfd, name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return err
	}
	d := os.NewFile(uintptr(fd), name)
	var opened unix.Stat_t
	if err = unix.Fstat(fd, &opened); err != nil || opened.Ino != st.Ino || opened.Dev != st.Dev {
		d.Close()
		return errors.Join(err, errors.New("directory changed during purge"))
	}
	for {
		batch, e := d.Readdirnames(256)
		for _, child := range batch {
			if err = removeTree(fd, child, dev, depth+1, budget); err != nil {
				d.Close()
				return err
			}
		}
		if errors.Is(e, io.EOF) || len(batch) == 0 {
			break
		}
		if e != nil {
			d.Close()
			return e
		}
	}
	d.Close()
	return unix.Unlinkat(dirfd, name, unix.AT_REMOVEDIR)
}

// ---------- replacing existing files (text editor) ----------

// Info identifies the exact object at a path, for optimistic concurrency.
type Info struct {
	Entry
	Mode    os.FileMode
	Version string // inode, size and modification time; changes on any rewrite
}

func infoOf(base string, st *unix.Stat_t) Info {
	kind := st.Mode & unix.S_IFMT
	return Info{
		Entry:   Entry{Name: base, Directory: kind == unix.S_IFDIR, Size: st.Size, Modified: mtime(st)},
		Mode:    os.FileMode(st.Mode & 0777),
		Version: fmt.Sprintf("%x-%x-%x", st.Ino, st.Size, st.Mtim.Nano()),
	}
}

// Inspect returns the identity of a regular file or directory at name.
func (s *Space) Inspect(name string) (Info, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	dir, base, err := s.parent(name)
	if err != nil {
		return Info{}, err
	}
	defer dir.Close()
	var st unix.Stat_t
	if err = unix.Fstatat(int(dir.Fd()), base, &st, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return Info{}, err
	}
	if k := st.Mode & unix.S_IFMT; k != unix.S_IFREG && k != unix.S_IFDIR {
		return Info{}, ErrType
	}
	return infoOf(base, &st), nil
}

// FileVersion returns the version of an open regular file (see Info.Version).
func FileVersion(f *os.File) (string, error) {
	var st unix.Stat_t
	if err := unix.Fstat(int(f.Fd()), &st); err != nil {
		return "", err
	}
	return infoOf("", &st).Version, nil
}

// ErrChanged reports that the file changed since the version the caller saw.
var ErrChanged = errors.New("file changed since it was read")

// Replace swaps a synced staging file in place of an existing regular file.
// The current file is first moved to the trash as item and checked against
// version; on mismatch it is moved back and ErrChanged is returned. Both moves
// use RENAME_NOREPLACE, so nothing is ever overwritten; between the two steps
// the name is briefly absent. If the second step fails, the previous version
// is put back when its name is still free (otherwise it stays in the trash).
func (s *Space) Replace(staged, target, item, version string) error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.writable(); err != nil {
		return err
	}
	if !stagingName(staged) || !TrashName(item) {
		return ErrPath
	}
	dir, base, err := s.parent(target)
	if err != nil {
		return err
	}
	defer dir.Close()
	if err = unix.Renameat2(int(dir.Fd()), base, int(s.trash.Fd()), item, unix.RENAME_NOREPLACE); err != nil {
		return renameErr(err)
	}
	var st unix.Stat_t
	err = unix.Fstatat(int(s.trash.Fd()), item, &st, unix.AT_SYMLINK_NOFOLLOW)
	if err != nil || st.Mode&unix.S_IFMT != unix.S_IFREG || infoOf(base, &st).Version != version {
		back := unix.Renameat2(int(s.trash.Fd()), item, int(dir.Fd()), base, unix.RENAME_NOREPLACE)
		return errors.Join(ErrChanged, err, back)
	}
	if err = unix.Renameat2(int(s.staging.Fd()), staged, int(dir.Fd()), base, unix.RENAME_NOREPLACE); err != nil {
		back := unix.Renameat2(int(s.trash.Fd()), item, int(dir.Fd()), base, unix.RENAME_NOREPLACE)
		return errors.Join(renameErr(err), back)
	}
	return errors.Join(dir.Sync(), s.trash.Sync(), s.staging.Sync())
}
