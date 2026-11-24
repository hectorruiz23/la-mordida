package app

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/hectorruiz23/la-mordida/internal/archivo/adapters/disk"
	"github.com/hectorruiz23/la-mordida/internal/archivo/domain"
)

type fetcherFunc func(context.Context, string) (io.ReadCloser, string, error)

func (f fetcherFunc) Fetch(ctx context.Context, url string) (io.ReadCloser, string, error) {
	return f(ctx, url)
}

type storedRecord struct {
	link   domain.Link
	origin domain.Origin
}

type fakeIndex struct {
	stored       []storedRecord
	pending      []storedRecord
	links        []domain.Link
	storeFailure bool
}

func (f *fakeIndex) Links(context.Context, bool) ([]domain.Link, error) {
	return f.links, nil
}

func (f *fakeIndex) Pending(_ context.Context, link domain.Link, cause error) error {
	f.pending = append(f.pending, storedRecord{link: link, origin: domain.Origin{URL: cause.Error()}})
	return nil
}

func (f *fakeIndex) Store(_ context.Context, link domain.Link, origin domain.Origin) error {
	if f.storeFailure {
		f.storeFailure = false
		return errors.New("database is locked")
	}
	f.stored = append(f.stored, storedRecord{link: link, origin: origin})
	return nil
}

func testLink(url string) domain.Link {
	return domain.Link{
		Publication: domain.Publication{
			Source:     "fixture",
			ExternalID: "urn:test:1",
			UpdatedAt:  time.Date(2025, 10, 20, 8, 23, 7, 0, time.UTC),
		},
		TargetKind: "publication",
		URL:        url,
		State:      domain.Announced,
	}
}

func TestFailedDownloadIsRecordedPendingAndARetryStoresIt(t *testing.T) {
	ctx := context.Background()
	objects := disk.New(filepath.Join(t.TempDir(), "objects"))
	index := &fakeIndex{}
	calls := 0
	fetcher := fetcherFunc(func(context.Context, string) (io.ReadCloser, string, error) {
		calls++
		if calls == 1 {
			return nil, "", errors.New("timeout")
		}
		return io.NopCloser(bytes.NewReader([]byte("pliego"))), "application/pdf", nil
	})
	archiver := New(fetcher, objects, index)

	link, err := archiver.Archive(ctx, testLink("https://example.test/pliego"))
	if err == nil {
		t.Fatal("first download should have failed")
	}
	if link.State != domain.Pending || link.Attempts != 1 {
		t.Fatalf("failed link = %+v", link)
	}
	if len(index.pending) != 1 || len(index.stored) != 0 {
		t.Fatalf("index after failure = %d pending / %d stored", len(index.pending), len(index.stored))
	}
	if link.LastError == "" {
		t.Fatal("pending link has no error to show")
	}

	retried, err := archiver.Archive(ctx, link)
	if err != nil {
		t.Fatalf("retry: %v", err)
	}
	if retried.State != domain.Evidence || retried.Attempts != 2 {
		t.Fatalf("retried link = %+v", retried)
	}
	if len(index.stored) != 1 || index.stored[0].origin.Object.MIME != "application/pdf" {
		t.Fatalf("stored = %+v", index.stored)
	}
	if !objects.Has(retried.SHA256) {
		t.Fatal("retried download is not on disk")
	}
}

func TestRetryAfterTheRenameReusesBytesWithoutDownloadingAgain(t *testing.T) {
	ctx := context.Background()
	objects := disk.New(filepath.Join(t.TempDir(), "objects"))
	content := []byte("pliego ya guardado")
	object, err := objects.Save(ctx, bytes.NewReader(content))
	if err != nil {
		t.Fatalf("pre-store object: %v", err)
	}

	link := testLink("https://example.test/pliego")
	link.SHA256 = object.SHA256
	index := &fakeIndex{storeFailure: true}
	fetcher := fetcherFunc(func(context.Context, string) (io.ReadCloser, string, error) {
		t.Fatal("the archiver downloaded again even though the bytes were on disk")
		return nil, "", nil
	})
	archiver := New(fetcher, objects, index)

	if _, err := archiver.Archive(ctx, link); err == nil {
		t.Fatal("the database failure was not reported")
	}
	if len(index.stored) != 0 {
		t.Fatalf("stored after failed transaction = %+v", index.stored)
	}

	recovered, err := archiver.Archive(ctx, link)
	if err != nil {
		t.Fatalf("retry: %v", err)
	}
	if recovered.State != domain.Evidence || recovered.SHA256 != object.SHA256 {
		t.Fatalf("recovered link = %+v", recovered)
	}
	if len(index.stored) != 1 {
		t.Fatalf("stored after retry = %+v", index.stored)
	}
}

func TestOneURLWithSuccessiveContentsKeepsBothHashes(t *testing.T) {
	ctx := context.Background()
	objects := disk.New(filepath.Join(t.TempDir(), "objects"))
	index := &fakeIndex{}
	versions := [][]byte{[]byte("pliego v1"), []byte("pliego v2")}
	call := 0
	fetcher := fetcherFunc(func(context.Context, string) (io.ReadCloser, string, error) {
		body := versions[call]
		call++
		return io.NopCloser(bytes.NewReader(body)), "application/pdf", nil
	})
	archiver := New(fetcher, objects, index)

	link := testLink("https://example.test/pliego")
	first, err := archiver.Archive(ctx, link)
	if err != nil {
		t.Fatalf("first version: %v", err)
	}
	second, err := archiver.Archive(ctx, link)
	if err != nil {
		t.Fatalf("second version: %v", err)
	}
	if first.SHA256 == second.SHA256 {
		t.Fatal("both contents produced the same hash")
	}
	if len(index.stored) != 2 {
		t.Fatalf("stored evidence = %d, want 2", len(index.stored))
	}
	for _, hash := range []string{first.SHA256, second.SHA256} {
		if !objects.Has(hash) {
			t.Errorf("version %s is not on disk", hash)
		}
	}
	if index.stored[0].origin.URL != index.stored[1].origin.URL {
		t.Fatalf("provenances lost the URL: %+v", index.stored)
	}
}

func TestTheSameContentFromTwoURLsKeepsBothProvenances(t *testing.T) {
	ctx := context.Background()
	root := filepath.Join(t.TempDir(), "objects")
	objects := disk.New(root)
	index := &fakeIndex{}
	fetcher := fetcherFunc(func(context.Context, string) (io.ReadCloser, string, error) {
		return io.NopCloser(bytes.NewReader([]byte("mismo pliego"))), "application/pdf", nil
	})
	archiver := New(fetcher, objects, index)

	first, err := archiver.Archive(ctx, testLink("https://example.test/a"))
	if err != nil {
		t.Fatalf("first url: %v", err)
	}
	second, err := archiver.Archive(ctx, testLink("https://example.test/b"))
	if err != nil {
		t.Fatalf("second url: %v", err)
	}
	if first.SHA256 != second.SHA256 {
		t.Fatalf("same content hashes = %s / %s", first.SHA256, second.SHA256)
	}
	if len(index.stored) != 2 || index.stored[0].origin.URL == index.stored[1].origin.URL {
		t.Fatalf("provenances = %+v", index.stored)
	}

	var files []string
	if err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err == nil && !entry.IsDir() {
			files = append(files, path)
		}
		return err
	}); err != nil {
		t.Fatalf("walk objects: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("object files = %v, want one shared by both urls", files)
	}
}
