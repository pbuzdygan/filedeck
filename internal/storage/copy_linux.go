package storage

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"

	"golang.org/x/sys/unix"
)

func copyName(name string) bool { return tokenName(name, "copy-", "") }

// CopyLimits bound one copy.
type CopyLimits struct {
	MaxBytes   int64
	MaxEntries int
}

// CopyProgress receives running totals; it must be cheap and thread-safe.
type CopyProgress func(files, bytes int64)

// CopyResult summarizes a finished copy.
type CopyResult struct {
	Files, Dirs, Bytes int64
	Skipped            int64 // symlinks, special files and nested mounts
}

// ErrCopyLimit reports that a copy exceeded CopyLimits.
var ErrCopyLimit = errors.New("copy exceeds the configured size or entry limit")

type copier struct {
	ctx      context.Context
	srcDev   uint64
	limits   CopyLimits
	modes    Modes
	progress CopyProgress
	res      CopyResult
	entries  int
}

// CopyToStaging copies a regular file or directory tree from src into a new
// staging entry of dst and returns its name; publish it with Publish. The
// source is read relative to directory descriptors without following symlinks
// or crossing mounts; symlinks and special files are skipped, never followed
// or opened. On error the partial copy is removed.
func CopyToStaging(ctx context.Context, src *Space, from string, dst *Space, limits CopyLimits, progress CopyProgress) (string, CopyResult, error) {
	src.mu.RLock()
	defer src.mu.RUnlock()
	if dst != src {
		dst.mu.RLock()
		defer dst.mu.RUnlock()
	}
	if err := dst.writable(); err != nil {
		return "", CopyResult{}, err
	}
	if src.closed {
		return "", CopyResult{}, os.ErrClosed
	}
	parent, base, err := src.parent(from)
	if err != nil {
		return "", CopyResult{}, err
	}
	defer parent.Close()
	var root unix.Stat_t
	if err = unix.Fstat(int(src.root.Fd()), &root); err != nil {
		return "", CopyResult{}, err
	}
	var raw [32]byte
	if _, err = rand.Read(raw[:]); err != nil {
		return "", CopyResult{}, err
	}
	name := "copy-" + hex.EncodeToString(raw[:])
	c := &copier{ctx: ctx, srcDev: root.Dev, limits: limits, modes: dst.modes, progress: progress}
	var st unix.Stat_t
	if err = unix.Fstatat(int(parent.Fd()), base, &st, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return "", CopyResult{}, err
	}
	switch st.Mode & unix.S_IFMT {
	case unix.S_IFREG, unix.S_IFDIR:
	default:
		return "", CopyResult{}, ErrType
	}
	if err = c.copyEntry(int(parent.Fd()), base, int(dst.staging.Fd()), name, 0); err == nil {
		err = dst.staging.Sync()
	}
	if err != nil {
		var stg unix.Stat_t
		budget := 1000000
		if unix.Fstat(int(dst.staging.Fd()), &stg) == nil {
			err = errors.Join(err, removeTree(int(dst.staging.Fd()), name, stg.Dev, 0, &budget))
		}
		return "", c.res, err
	}
	return name, c.res, nil
}

// RemoveCopy discards an unpublished staging copy.
func (s *Space) RemoveCopy(name string) error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.writable(); err != nil {
		return err
	}
	if !copyName(name) {
		return ErrPath
	}
	var st unix.Stat_t
	if err := unix.Fstat(int(s.staging.Fd()), &st); err != nil {
		return err
	}
	budget := 1000000
	return removeTree(int(s.staging.Fd()), name, st.Dev, 0, &budget)
}

func (c *copier) count() error {
	if err := c.ctx.Err(); err != nil {
		return err
	}
	c.entries++
	if c.limits.MaxEntries > 0 && c.entries > c.limits.MaxEntries {
		return ErrCopyLimit
	}
	return nil
}

func (c *copier) copyEntry(srcDir int, name string, dstDir int, dstName string, depth int) error {
	if err := c.count(); err != nil {
		return err
	}
	var st unix.Stat_t
	if err := unix.Fstatat(srcDir, name, &st, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		if errors.Is(err, unix.ENOENT) {
			return nil // vanished meanwhile (external change)
		}
		return err
	}
	switch st.Mode & unix.S_IFMT {
	case unix.S_IFREG:
		return c.copyFile(srcDir, name, &st, dstDir, dstName)
	case unix.S_IFDIR:
		if st.Dev != c.srcDev {
			c.res.Skipped++
			return nil
		}
		if depth > 128 {
			return ErrCopyLimit
		}
		return c.copyDir(srcDir, name, &st, dstDir, dstName, depth)
	default:
		c.res.Skipped++
		return nil
	}
}

func (c *copier) copyDir(srcDir int, name string, st *unix.Stat_t, dstDir int, dstName string, depth int) error {
	fd, err := unix.Openat(srcDir, name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return err
	}
	d := os.NewFile(uintptr(fd), name)
	defer d.Close()
	var opened unix.Stat_t
	if err = unix.Fstat(fd, &opened); err != nil || opened.Ino != st.Ino || opened.Dev != st.Dev {
		return errors.Join(err, errors.New("source directory changed during copy"))
	}
	if err = unix.Mkdirat(dstDir, dstName, 0700); err != nil {
		return err
	}
	out, err := unix.Openat(dstDir, dstName, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return err
	}
	o := os.NewFile(uintptr(out), dstName)
	defer o.Close()
	c.res.Dirs++
	for {
		batch, e := d.Readdirnames(256)
		for _, child := range batch {
			if err = c.copyEntry(fd, child, out, child, depth+1); err != nil {
				return err
			}
		}
		if errors.Is(e, io.EOF) || len(batch) == 0 {
			break
		}
		if e != nil {
			return e
		}
	}
	// Private while being filled; final mode just before it can be published.
	if err = unix.Fchmod(out, uint32(c.modes.Dir)); err != nil && !errors.Is(err, unix.EPERM) && !errors.Is(err, unix.EOPNOTSUPP) {
		return err
	}
	return o.Sync()
}

func (c *copier) copyFile(srcDir int, name string, st *unix.Stat_t, dstDir int, dstName string) error {
	if c.limits.MaxBytes > 0 && c.res.Bytes+st.Size > c.limits.MaxBytes {
		return ErrCopyLimit
	}
	if err := reserveAt(dstDir, st.Size); err != nil {
		return err
	}
	// Check the type through O_PATH, then reopen the same object for reading,
	// so a FIFO or device swapped in meanwhile is never opened for I/O.
	h, err := unix.Openat(srcDir, name, unix.O_PATH|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return err
	}
	var hs unix.Stat_t
	if err = unix.Fstat(h, &hs); err != nil || hs.Mode&unix.S_IFMT != unix.S_IFREG {
		unix.Close(h)
		if err == nil {
			c.res.Skipped++
		}
		return err
	}
	in, err := unix.Open(fmt.Sprintf("/proc/self/fd/%d", h), unix.O_RDONLY|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	unix.Close(h)
	if err != nil {
		return err
	}
	src := os.NewFile(uintptr(in), name)
	defer src.Close()
	var is unix.Stat_t
	if err = unix.Fstat(in, &is); err != nil || is.Ino != hs.Ino || is.Dev != hs.Dev {
		return errors.Join(err, errors.New("source file changed during copy"))
	}
	out, err := unix.Openat(dstDir, dstName, unix.O_CREAT|unix.O_EXCL|unix.O_WRONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if err != nil {
		return err
	}
	dst := os.NewFile(uintptr(out), dstName)
	defer dst.Close()
	remaining := c.limits.MaxBytes - c.res.Bytes
	var r io.Reader = src
	if c.limits.MaxBytes > 0 {
		r = io.LimitReader(src, remaining+1)
	}
	buf := make([]byte, 1<<20)
	for {
		if err = c.ctx.Err(); err != nil {
			return err
		}
		n, rerr := r.Read(buf)
		if n > 0 {
			if _, err = dst.Write(buf[:n]); err != nil {
				return err
			}
			c.res.Bytes += int64(n)
			if c.limits.MaxBytes > 0 && c.res.Bytes > c.limits.MaxBytes {
				return ErrCopyLimit
			}
			if c.progress != nil {
				c.progress(c.res.Files, c.res.Bytes)
			}
		}
		if errors.Is(rerr, io.EOF) {
			break
		}
		if rerr != nil {
			return rerr
		}
	}
	if err = dst.Chmod(c.modes.File); err != nil && !errors.Is(err, unix.EPERM) && !errors.Is(err, unix.EOPNOTSUPP) {
		return err
	}
	if err = dst.Sync(); err != nil {
		return err
	}
	c.res.Files++
	if c.progress != nil {
		c.progress(c.res.Files, c.res.Bytes)
	}
	return nil
}
