package mediastore

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// fsStore stores objects under root as plain files. Writes go to
// "{path}.tmp" first and are renamed into place once the body has been
// fully consumed — a cancelled or partial Put leaves the .tmp file
// behind (cleaned up on the next attempt) but never exposes a
// half-written object at the target path.
type fsStore struct {
	root string
}

func newFSStore(root string) (Store, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("%w: resolve fs root %q: %w", ErrInvalidConfig, root, err)
	}
	if err := os.MkdirAll(abs, 0o755); err != nil {
		return nil, fmt.Errorf("%w: mkdir fs root %q: %w", ErrInvalidConfig, abs, err)
	}
	return &fsStore{root: abs}, nil
}

func (s *fsStore) pathFor(key string) (string, error) {
	clean := filepath.Clean(key)
	if strings.HasPrefix(clean, "..") || filepath.IsAbs(clean) {
		return "", fmt.Errorf("mediastore fs: rejected key %q", key)
	}
	return filepath.Join(s.root, clean), nil
}

func (s *fsStore) Get(ctx context.Context, key string) (io.ReadCloser, ObjectInfo, error) {
	if err := ctx.Err(); err != nil {
		return nil, ObjectInfo{}, fmt.Errorf("mediastore fs get %q: %w", key, err)
	}
	path, err := s.pathFor(key)
	if err != nil {
		return nil, ObjectInfo{}, err
	}
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ObjectInfo{}, fmt.Errorf("mediastore fs get %q: %w", key, ErrNotFound)
		}
		return nil, ObjectInfo{}, fmt.Errorf("mediastore fs get %q: %w", key, err)
	}
	st, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, ObjectInfo{}, fmt.Errorf("mediastore fs stat %q: %w", key, err)
	}
	info := ObjectInfo{
		Key:          key,
		Size:         st.Size(),
		LastModified: st.ModTime().UTC(),
	}
	return f, info, nil
}

func (s *fsStore) Put(ctx context.Context, key string, r io.Reader, _ int64, _ string) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("mediastore fs put %q: %w", key, err)
	}
	path, err := s.pathFor(key)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("mediastore fs mkdir %q: %w", path, err)
	}
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return fmt.Errorf("mediastore fs create %q: %w", tmp, err)
	}
	if _, err := io.Copy(f, r); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return fmt.Errorf("mediastore fs write %q: %w", tmp, err)
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return fmt.Errorf("mediastore fs sync %q: %w", tmp, err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("mediastore fs close %q: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("mediastore fs rename %q: %w", path, err)
	}
	return nil
}

func (s *fsStore) Stat(ctx context.Context, key string) (ObjectInfo, error) {
	if err := ctx.Err(); err != nil {
		return ObjectInfo{}, fmt.Errorf("mediastore fs stat %q: %w", key, err)
	}
	path, err := s.pathFor(key)
	if err != nil {
		return ObjectInfo{}, err
	}
	st, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return ObjectInfo{}, fmt.Errorf("mediastore fs stat %q: %w", key, ErrNotFound)
		}
		return ObjectInfo{}, fmt.Errorf("mediastore fs stat %q: %w", key, err)
	}
	return ObjectInfo{
		Key:          key,
		Size:         st.Size(),
		LastModified: st.ModTime().UTC(),
	}, nil
}

func (s *fsStore) Delete(ctx context.Context, key string) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("mediastore fs delete %q: %w", key, err)
	}
	path, err := s.pathFor(key)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("mediastore fs delete %q: %w", key, err)
	}
	return nil
}

func (s *fsStore) List(ctx context.Context, prefix string, fn func(ObjectInfo) error) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("mediastore fs list %q: %w", prefix, err)
	}
	start, err := s.pathFor(prefix)
	if err != nil {
		return err
	}
	// When prefix points at a non-existent directory, treat it as
	// "no objects" rather than as an error — matches the s3 semantic.
	if st, statErr := os.Stat(start); statErr != nil {
		if errors.Is(statErr, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("mediastore fs list stat %q: %w", start, statErr)
	} else if !st.IsDir() {
		return fn(ObjectInfo{Key: prefix, Size: st.Size(), LastModified: st.ModTime().UTC()})
	}
	return filepath.WalkDir(start, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		if strings.HasSuffix(path, ".tmp") {
			return nil
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		rel, relErr := filepath.Rel(s.root, path)
		if relErr != nil {
			return relErr
		}
		st, statErr := d.Info()
		if statErr != nil {
			return statErr
		}
		return fn(ObjectInfo{
			Key:          filepath.ToSlash(rel),
			Size:         st.Size(),
			LastModified: st.ModTime().UTC(),
		})
	})
}
