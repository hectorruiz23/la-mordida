// Package disk stores the bytes of archived documents in a content-addressed
// tree. Every object is written to a temporary file on the same volume, hashed
// while it is written and renamed into objects/<prefix>/<sha256> only once it
// is complete, so an interruption never leaves a half object behind.
package disk

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/hectorruiz23/la-mordida/internal/archivo/domain"
)

// Store keeps every object under <root>/<first two hex digits>/<sha256>.
type Store struct {
	root string
}

func New(root string) *Store {
	return &Store{root: root}
}

// Save streams body into a temporary file in the store root, computes its
// SHA-256 while it is written, syncs it and renames it under its hash. The
// same bytes stored twice keep one file; the second save discards its copy.
func (s *Store) Save(ctx context.Context, body io.Reader) (domain.Object, error) {
	if err := ctx.Err(); err != nil {
		return domain.Object{}, err
	}
	if err := os.MkdirAll(s.root, 0o755); err != nil {
		return domain.Object{}, fmt.Errorf("create object root %s: %w", s.root, err)
	}

	temp, err := os.CreateTemp(s.root, ".download-*")
	if err != nil {
		return domain.Object{}, fmt.Errorf("create temporary object in %s: %w", s.root, err)
	}
	tempName := temp.Name()
	defer func() {
		_ = temp.Close()
		_ = os.Remove(tempName)
	}()

	hash := sha256.New()
	written, err := io.Copy(io.MultiWriter(temp, hash), &contextReader{ctx: ctx, body: body})
	if err != nil {
		return domain.Object{}, fmt.Errorf("write object: %w", err)
	}
	if err := temp.Sync(); err != nil {
		return domain.Object{}, fmt.Errorf("sync object: %w", err)
	}
	if err := temp.Close(); err != nil {
		return domain.Object{}, fmt.Errorf("close object: %w", err)
	}

	sum := hex.EncodeToString(hash.Sum(nil))
	destination := s.path(sum)
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		return domain.Object{}, fmt.Errorf("create object directory: %w", err)
	}

	switch _, err := os.Stat(destination); {
	case err == nil:
		// The same bytes are already stored: keep one file.
		return domain.Object{SHA256: sum, Bytes: written}, nil
	case !errors.Is(err, os.ErrNotExist):
		return domain.Object{}, fmt.Errorf("stat object %s: %w", sum, err)
	}

	if err := os.Rename(tempName, destination); err != nil {
		return domain.Object{}, fmt.Errorf("rename object %s: %w", sum, err)
	}
	return domain.Object{SHA256: sum, Bytes: written}, nil
}

// Has reports whether the exact bytes are already stored.
func (s *Store) Has(sum string) bool {
	if !validHash(sum) {
		return false
	}
	_, err := os.Stat(s.path(sum))
	return err == nil
}

// Open returns the stored bytes of a hash for a byte-for-byte check or a
// download over HTTP.
func (s *Store) Open(sum string) (io.ReadCloser, error) {
	if !validHash(sum) {
		return nil, fmt.Errorf("object %q: not a SHA-256", sum)
	}
	file, err := os.Open(s.path(sum))
	if err != nil {
		return nil, fmt.Errorf("open object %s: %w", sum, err)
	}
	return file, nil
}

func (s *Store) path(sum string) string {
	return filepath.Join(s.root, sum[:2], sum)
}

func validHash(sum string) bool {
	if len(sum) != sha256.Size*2 {
		return false
	}
	_, err := hex.DecodeString(sum)
	return err == nil
}

// contextReader stops a download that outlives its context.
type contextReader struct {
	ctx  context.Context
	body io.Reader
}

func (r *contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.body.Read(p)
}
