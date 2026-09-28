package storage

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"

	"golang.org/x/sys/unix"
)

// SearchLimits bound one search so that a large share cannot exhaust the server.
type SearchLimits struct {
	MaxScanned int // directory entries examined
	MaxResults int
	MaxDepth   int
}

// Hit is one search result; Path is relative to the space root.
type Hit struct {
	Path string `json:"path"`
	Entry
}

// Search finds regular files and directories below dir whose name contains
// query (case-insensitive). The walk uses directory descriptors, never follows
// symlinks, never enters other mounts or the metadata directory, and stops at
// the limits or when ctx ends, reporting truncated=true.
func (s *Space) Search(ctx context.Context, dir, query string, limits SearchLimits) (hits []Hit, truncated bool, err error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	needle := strings.ToLower(query)
	if needle == "" {
		return nil, false, ErrPath
	}
	f, err := s.open(dir, unix.O_RDONLY|unix.O_DIRECTORY)
	if err != nil {
		return nil, false, err
	}
	var self unix.Stat_t
	if err = unix.Fstat(int(f.Fd()), &self); err != nil {
		f.Close()
		return nil, false, err
	}
	w := &walker{ctx: ctx, space: s, needle: needle, limits: limits, dev: self.Dev, hits: make([]Hit, 0)}
	prefix := ""
	if dir != "." {
		prefix = dir + "/"
	}
	err = w.walk(f, prefix, 0)
	f.Close()
	if errors.Is(err, errStop) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return w.hits, true, nil
	}
	return w.hits, false, err
}

var errStop = errors.New("search limit reached")

type walker struct {
	ctx     context.Context
	space   *Space
	needle  string
	limits  SearchLimits
	dev     uint64
	scanned int
	hits    []Hit
}

// walk consumes and closes nothing: the caller owns d.
func (w *walker) walk(d *os.File, prefix string, depth int) error {
	for {
		if err := w.ctx.Err(); err != nil {
			return err
		}
		names, rerr := d.Readdirnames(256)
		for _, name := range names {
			if w.scanned++; w.scanned > w.limits.MaxScanned {
				return errStop
			}
			var st unix.Stat_t
			if err := unix.Fstatat(int(d.Fd()), name, &st, unix.AT_SYMLINK_NOFOLLOW); err != nil {
				continue // vanished meanwhile
			}
			kind := st.Mode & unix.S_IFMT
			if kind != unix.S_IFREG && kind != unix.S_IFDIR {
				continue
			}
			if Reserved(name) || (w.space.hasMeta && st.Dev == w.space.metaDev && st.Ino == w.space.metaIno) {
				continue
			}
			if kind == unix.S_IFDIR && st.Dev != w.dev {
				continue // nested mount
			}
			if strings.Contains(strings.ToLower(name), w.needle) {
				w.hits = append(w.hits, Hit{Path: prefix + name, Entry: Entry{Name: name, Directory: kind == unix.S_IFDIR, Size: st.Size, Modified: mtime(&st)}})
				if len(w.hits) >= w.limits.MaxResults {
					return errStop
				}
			}
			if kind == unix.S_IFDIR && depth < w.limits.MaxDepth {
				fd, err := unix.Openat(int(d.Fd()), name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
				if err != nil {
					continue // unreadable or replaced: skip, do not fail the search
				}
				sub := os.NewFile(uintptr(fd), name)
				var opened unix.Stat_t
				if unix.Fstat(fd, &opened) != nil || opened.Ino != st.Ino || opened.Dev != st.Dev {
					sub.Close()
					continue
				}
				err = w.walk(sub, prefix+name+"/", depth+1)
				sub.Close()
				if err != nil {
					return err
				}
			}
		}
		if errors.Is(rerr, io.EOF) || len(names) == 0 {
			return nil
		}
		if rerr != nil {
			return rerr
		}
	}
}
