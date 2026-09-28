package core

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"unicode/utf8"

	"github.com/pbuzdygan/filedeck/internal/storage"
	"golang.org/x/sys/unix"
)

// MaxTextBytes limits files opened in the text editor.
const MaxTextBytes = 2 << 20

var (
	ErrNotText  = errors.New("not a UTF-8 text file")
	ErrTooLarge = errors.New("file too large for the editor")
)

// Text is an editable file with the version it was read at.
type Text struct {
	Content string `json:"content"`
	Version string `json:"version"`
}

func validText(b []byte) bool { return utf8.Valid(b) && bytes.IndexByte(b, 0) < 0 }

// ReadText returns a small UTF-8 file and its version. Requires Read.
func (s *Service) ReadText(ctx context.Context, subject, space, name string) (Text, error) {
	f, err := s.Read(ctx, subject, space, name)
	if err != nil {
		return Text{}, err
	}
	defer f.Close()
	version, err := storage.FileVersion(f)
	if err != nil {
		return Text{}, err
	}
	data, err := io.ReadAll(io.LimitReader(f, MaxTextBytes+1))
	if err != nil {
		return Text{}, err
	}
	if len(data) > MaxTextBytes {
		return Text{}, ErrTooLarge
	}
	if !validText(data) {
		return Text{}, ErrNotText
	}
	// A concurrent external write would make the content and version disagree.
	if after, e := storage.FileVersion(f); e != nil || after != version {
		return Text{}, errors.Join(storage.ErrChanged, e)
	}
	return Text{Content: string(data), Version: version}, nil
}

// WriteText saves content at name. With an empty version it creates a new
// file (requires Create, never replaces anything). Otherwise it replaces the
// existing file only if it is still at version (requires Read and Modify); the
// previous version goes to the trash. Returns the new version.
func (s *Service) WriteText(ctx context.Context, subject, space, name, content, version string) (string, error) {
	if len(content) > MaxTextBytes {
		return "", ErrTooLarge
	}
	if !validText([]byte(content)) {
		return "", ErrNotText
	}
	if !storage.ValidUserPath(name) {
		return "", storage.ErrPath
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	// Lock order everywhere: trashMu before policyMu.
	s.trashMu.Lock()
	defer s.trashMu.Unlock()
	s.policyMu.RLock()
	defer s.policyMu.RUnlock()
	sp, ok := s.spaces[space]
	need := Create
	if version != "" {
		need = Read | Modify
	}
	if !ok || subject == "" || s.grantedLocked(subject, space)&need != need {
		return "", ErrDenied
	}
	if sp.ReadOnly() {
		return "", storage.ErrReadOnly
	}
	mode := sp.FileMode()
	var old storage.Info
	if version != "" {
		var err error
		if old, err = sp.Inspect(name); err != nil {
			return "", err
		}
		if old.Directory {
			return "", storage.ErrType
		}
		if old.Version != version {
			return "", storage.ErrChanged
		}
		mode = old.Mode
	}
	if err := sp.Reserve(int64(len(content))); err != nil {
		return "", err
	}
	staged, f, err := sp.CreateStaging()
	if err != nil {
		return "", err
	}
	_, err = f.WriteString(content)
	if err == nil {
		if e := f.Chmod(mode); e != nil && !errors.Is(e, unix.EPERM) && !errors.Is(e, unix.EOPNOTSUPP) {
			err = e
		}
	}
	if err == nil {
		err = f.Sync()
	}
	if err = errors.Join(err, f.Close()); err != nil {
		return "", errors.Join(err, sp.RemoveStaging(staged))
	}
	if version == "" {
		published, e := sp.Publish(staged, name)
		if !published {
			return "", errors.Join(e, sp.RemoveStaging(staged))
		}
		if e != nil {
			return "", e
		}
	} else {
		var raw [32]byte
		if _, err = rand.Read(raw[:]); err != nil {
			return "", errors.Join(err, sp.RemoveStaging(staged))
		}
		t := TrashItem{ID: hex.EncodeToString(raw[:]), Space: space, Path: name, Size: old.Size, Deleted: s.now(), DeletedBy: subject, Replaced: true}
		if err = s.putTrash(t); err != nil {
			return "", errors.Join(err, sp.RemoveStaging(staged))
		}
		if err = sp.Replace(staged, name, itemName(t.ID), version); err != nil {
			cleanup := sp.RemoveStaging(staged)
			if _, e := sp.TrashEntry(itemName(t.ID)); errors.Is(e, os.ErrNotExist) {
				cleanup = errors.Join(cleanup, s.dropTrash(t.ID))
			}
			return "", errors.Join(err, cleanup)
		}
	}
	now, err := sp.Inspect(name)
	if err != nil {
		return "", err
	}
	return now.Version, nil
}
