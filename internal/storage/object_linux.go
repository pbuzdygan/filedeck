package storage

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

// ObjectID identifies one file or directory independently of its path:
// device, inode and, where the filesystem reports it, birth time — so an inode
// number reused by a new file after deletion is not taken for the original.
type ObjectID struct {
	Dev       uint64 `json:"dev"`
	Ino       uint64 `json:"ino"`
	Birth     int64  `json:"birth,omitempty"` // nanoseconds; 0 when unsupported
	Directory bool   `json:"directory"`
}

// ErrGone reports that the object at a path is no longer the identified one:
// it was moved, renamed, replaced or removed.
var ErrGone = errors.New("object moved, replaced or removed")

func statxID(dirfd int, name string, flags int) (ObjectID, Entry, error) {
	var sx unix.Statx_t
	mask := unix.STATX_TYPE | unix.STATX_INO | unix.STATX_BTIME | unix.STATX_SIZE | unix.STATX_MTIME
	if err := unix.Statx(dirfd, name, flags|unix.AT_SYMLINK_NOFOLLOW|unix.AT_STATX_SYNC_AS_STAT, mask, &sx); err != nil {
		return ObjectID{}, Entry{}, err
	}
	kind := uint32(sx.Mode) & unix.S_IFMT
	if kind != unix.S_IFREG && kind != unix.S_IFDIR {
		return ObjectID{}, Entry{}, ErrType
	}
	id := ObjectID{Dev: unix.Mkdev(sx.Dev_major, sx.Dev_minor), Ino: sx.Ino, Directory: kind == unix.S_IFDIR}
	if sx.Mask&unix.STATX_BTIME != 0 {
		id.Birth = sx.Btime.Sec*1e9 + int64(sx.Btime.Nsec)
	}
	e := Entry{Directory: id.Directory, Size: int64(sx.Size), Modified: time.Unix(sx.Mtime.Sec, int64(sx.Mtime.Nsec)).UTC()}
	return id, e, nil
}

// Identify returns the identity of the regular file or directory at name.
func (s *Space) Identify(name string) (ObjectID, Entry, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if !ValidUserPath(name) {
		return ObjectID{}, Entry{}, ErrPath
	}
	dir, base, err := s.parent(name)
	if err != nil {
		return ObjectID{}, Entry{}, err
	}
	defer dir.Close()
	id, e, err := statxID(int(dir.Fd()), base, 0)
	e.Name = base
	return id, e, err
}

// Object is an opened, identity-checked file or directory. Reads through it
// resolve beneath the object itself, never through its original path again, so
// a rename of the object or its parents cannot redirect them elsewhere.
type Object struct {
	s     *Space
	f     *os.File // O_PATH handle of the object
	id    ObjectID
	Entry Entry
}

// OpenObject opens name only if it is still the object identified by want;
// otherwise it returns ErrGone.
func (s *Space) OpenObject(name string, want ObjectID) (*Object, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if !ValidUserPath(name) {
		return nil, ErrPath
	}
	h, err := s.open(name, unix.O_PATH)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) || errors.Is(err, unix.ENOTDIR) || errors.Is(err, unix.ELOOP) || errors.Is(err, unix.EXDEV) {
			return nil, ErrGone
		}
		return nil, err
	}
	id, e, err := statxID(int(h.Fd()), "", unix.AT_EMPTY_PATH)
	if err == nil && id != want {
		err = ErrGone
	}
	if errors.Is(err, ErrType) {
		err = ErrGone
	}
	if err != nil {
		h.Close()
		return nil, err
	}
	e.Name = path.Base(name)
	return &Object{s: s, f: h, id: id, Entry: e}, nil
}

func (o *Object) Close() error { return o.f.Close() }

// open resolves sub ("." for the object itself) beneath a directory object.
// The caller holds o.s.mu.
func (o *Object) open(sub string, flags int) (*os.File, error) {
	if o.s.closed {
		return nil, fs.ErrClosed
	}
	if !ValidPath(sub, true) || (!o.id.Directory && sub != ".") {
		return nil, ErrPath
	}
	if sub != "." {
		parts := strings.Split(sub, "/")
		for i, seg := range parts {
			if Reserved(seg) {
				return nil, ErrPath
			}
			// The metadata directory can only be reached through a rename by
			// someone else; refuse it by identity as well.
			var st unix.Stat_t
			if o.s.hasMeta && unix.Fstatat(int(o.f.Fd()), strings.Join(parts[:i+1], "/"), &st, unix.AT_SYMLINK_NOFOLLOW) == nil && st.Dev == o.s.metaDev && st.Ino == o.s.metaIno {
				return nil, ErrPath
			}
		}
	}
	fd, err := unix.Openat2(int(o.f.Fd()), sub, &unix.OpenHow{
		Flags:   uint64(flags | unix.O_CLOEXEC | unix.O_NOFOLLOW),
		Resolve: unix.RESOLVE_BENEATH | unix.RESOLVE_NO_SYMLINKS | unix.RESOLVE_NO_XDEV,
	})
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(fd), sub), nil
}

// Read opens a regular file: the object itself (sub ".") or one beneath it.
func (o *Object) Read(sub string) (*os.File, error) {
	o.s.mu.RLock()
	defer o.s.mu.RUnlock()
	if o.s.closed {
		return nil, fs.ErrClosed
	}
	if !o.id.Directory {
		if sub != "." {
			return nil, ErrPath
		}
		return reopenRegular(o.f, o.Entry.Name)
	}
	if sub == "." {
		return nil, ErrType
	}
	h, err := o.open(sub, unix.O_PATH)
	if err != nil {
		return nil, err
	}
	defer h.Close()
	return reopenRegular(h, path.Base(sub))
}

// List lists a directory object or a directory beneath it (see Space.List).
func (o *Object) List(ctx context.Context, sub string, limit int) ([]Entry, error) {
	o.s.mu.RLock()
	defer o.s.mu.RUnlock()
	if limit < 1 || limit > 10000 {
		return nil, ErrLimit
	}
	if !o.id.Directory {
		return nil, ErrType
	}
	f, err := o.open(sub, unix.O_RDONLY|unix.O_DIRECTORY)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return o.s.listDir(ctx, f, limit)
}
