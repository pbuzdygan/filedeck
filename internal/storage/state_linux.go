package storage

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"golang.org/x/sys/unix"
)

// State is the private application directory (databases, certificate). It is
// locked for exclusive use by one process.
type State struct {
	mu     sync.RWMutex
	dir    *os.File
	path   string
	closed bool
}

// OpenState requires an existing directory owned by the process with mode 0700.
func OpenState(dir string) (*State, error) {
	dir, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	f, err := openDir(dir)
	if err != nil {
		return nil, err
	}
	var st unix.Stat_t
	if err = unix.Fstat(int(f.Fd()), &st); err != nil {
		f.Close()
		return nil, err
	}
	if st.Uid != uint32(os.Geteuid()) || st.Mode&0777 != 0700 {
		f.Close()
		return nil, fmt.Errorf("state %s must be owned by uid %d with mode 0700 (found uid %d, mode %o)", dir, os.Geteuid(), st.Uid, st.Mode&0777)
	}
	if err = unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		f.Close()
		return nil, fmt.Errorf("state is already in use or cannot be locked: %w", err)
	}
	s := &State{dir: f, path: dir}
	// Earlier versions staged uploads in state; those files are ours to discard.
	if list, err := names(f, 10000); err == nil {
		for _, n := range list {
			if stagingName(n) {
				_ = unix.Unlinkat(int(f.Fd()), n, 0)
			}
		}
	}
	return s, nil
}

func (s *State) Path() string { return s.path }

// Overlaps reports whether the state directory and dir contain one another.
func (s *State) Overlaps(dir string) bool {
	dir, err := filepath.Abs(dir)
	if err != nil {
		return true
	}
	return dir == s.path || strings.HasPrefix(dir, s.path+"/") || strings.HasPrefix(s.path, dir+"/")
}

func (s *State) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	// Closing the fd releases the advisory process lock.
	return s.dir.Close()
}

// OpenFile opens a fixed application file (e.g. a database) relative to the
// state capability, without following symlinks. It must be a private regular
// file without hardlinks.
func (s *State) OpenFile(name string, flags int) (*os.File, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return nil, fs.ErrClosed
	}
	if name == "" || strings.ContainsAny(name, "/\\\x00") || name == "." || name == ".." {
		return nil, ErrPath
	}
	fd, err := unix.Openat(int(s.dir.Fd()), name, flags|unix.O_NOFOLLOW|unix.O_CLOEXEC|unix.O_NONBLOCK, 0600)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), name)
	var st unix.Stat_t
	if err = unix.Fstat(fd, &st); err != nil {
		f.Close()
		return nil, err
	}
	if st.Mode&unix.S_IFMT != unix.S_IFREG || st.Mode&0777 != 0600 || st.Uid != uint32(os.Geteuid()) || st.Nlink != 1 {
		f.Close()
		return nil, errors.New("state file must be a private regular file without hardlinks")
	}
	return f, nil
}
