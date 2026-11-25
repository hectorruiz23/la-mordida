// Package app archives the documents a publication announces: it downloads
// their bytes, stores them content-addressed and records their provenance. It
// knows only the contracts it defines; HTTP, the filesystem and SQLite live in
// adapters.
package app

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/hectorruiz23/la-mordida/internal/archivo/domain"
)

// Fetcher returns the bytes a URL serves now and the MIME type it announced.
type Fetcher interface {
	Fetch(ctx context.Context, url string) (io.ReadCloser, string, error)
}

// Objects stores bytes by their hash. Saving the same content twice keeps one
// file and opening a hash returns exactly the bytes that were stored.
type Objects interface {
	Save(ctx context.Context, body io.Reader) (domain.Object, error)
	Has(sha256 string) bool
	Open(sha256 string) (io.ReadCloser, error)
}

// Index records the lifecycle of a link after its bytes are on disk.
type Index interface {
	Pending(ctx context.Context, link domain.Link, cause error) error
	Store(ctx context.Context, link domain.Link, origin domain.Origin) error
}

// Archiver downloads and stores the documents of one publication at a time.
type Archiver struct {
	fetcher Fetcher
	objects Objects
	index   Index
	now     func() time.Time
}

func New(fetcher Fetcher, objects Objects, index Index) *Archiver {
	return &Archiver{
		fetcher: fetcher,
		objects: objects,
		index:   index,
		now:     func() time.Time { return time.Now().UTC() },
	}
}

// Archive stores one link's bytes and records where they came from. When the
// announced hash is already on disk it does not download again: a retry after
// an interrupted run reuses the bytes. A failed download is recorded as
// pending, never as evidence, so a later run tries it again.
func (a *Archiver) Archive(ctx context.Context, link domain.Link) (domain.Link, error) {
	if err := link.Validate(); err != nil {
		return link, err
	}
	link.Attempts++

	if link.SHA256 != "" && a.objects.Has(link.SHA256) {
		origin := domain.Origin{
			URL:        link.URL,
			ObtainedAt: a.now(),
			Object:     domain.Object{SHA256: link.SHA256, MIME: link.MIME},
		}
		if err := a.index.Store(ctx, link, origin); err != nil {
			return link, fmt.Errorf("record existing object for %s: %w", link.URL, err)
		}
		return stored(link, origin), nil
	}

	body, mime, err := a.fetcher.Fetch(ctx, link.URL)
	if err != nil {
		return a.pending(ctx, link, err)
	}

	object, saveErr := a.objects.Save(ctx, body)
	closeErr := body.Close()
	if saveErr != nil {
		return a.pending(ctx, link, saveErr)
	}
	if closeErr != nil {
		return a.pending(ctx, link, closeErr)
	}

	origin := domain.Origin{
		URL:        link.URL,
		ObtainedAt: a.now(),
		Object:     domain.Object{SHA256: object.SHA256, MIME: mime, Bytes: object.Bytes},
	}
	if err := a.index.Store(ctx, link, origin); err != nil {
		// The bytes are already on disk under their hash; the link stays as it
		// was so a later run records it and reuses the object.
		return link, fmt.Errorf("record evidence for %s: %w", link.URL, err)
	}
	return stored(link, origin), nil
}

// Refresh downloads a link again even when the announced bytes are already on
// disk, so a URL whose content changed keeps a second version beside the first.
func (a *Archiver) Refresh(ctx context.Context, link domain.Link) (domain.Link, error) {
	link.SHA256 = ""
	return a.Archive(ctx, link)
}

func (a *Archiver) pending(ctx context.Context, link domain.Link, cause error) (domain.Link, error) {
	if err := a.index.Pending(ctx, link, cause); err != nil {
		return link, fmt.Errorf("record pending %s: %w", link.URL, err)
	}
	link.State = domain.Pending
	link.LastError = cause.Error()
	return link, cause
}

func stored(link domain.Link, origin domain.Origin) domain.Link {
	link.State = domain.Evidence
	link.SHA256 = origin.Object.SHA256
	link.MIME = origin.Object.MIME
	link.ObtainedAt = origin.ObtainedAt
	link.LastError = ""
	return link
}
