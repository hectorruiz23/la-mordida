package placsp

import (
	"archive/zip"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hectorruiz23/la-mordida/internal/ingesta/app"
	"github.com/hectorruiz23/la-mordida/internal/ingesta/domain"
)

func TestFeedResumesAfterTheSecondPageFailsAndReprocessesTheTie(t *testing.T) {
	pages := map[string]string{
		"/feed.atom": atomPage(
			feedEntry("urn:test:a", "2021-01-05T10:00:00Z", "F-A"),
			feedEntry("urn:test:b", "2021-01-05T10:00:00Z", "F-B"),
			atomLink("page2.atom", "next"),
		),
		"/page2.atom": atomPage(
			feedEntry("urn:test:c", "2021-01-04T10:00:00Z", "F-C"),
			atomLink("page3.atom", "next"),
		),
		"/page3.atom": atomPage(
			feedEntry("urn:test:d", "2021-01-03T10:00:00Z", "F-D"),
		),
	}

	var broken int32 = 1
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/page2.atom" && atomic.LoadInt32(&broken) == 1 {
			http.Error(writer, "down", http.StatusServiceUnavailable)
			return
		}
		page, ok := pages[request.URL.Path]
		if !ok {
			http.NotFound(writer, request)
			return
		}
		_, _ = writer.Write([]byte(page))
	}))
	t.Cleanup(server.Close)

	reader, err := NewFeed(Profiles, server.URL+"/feed.atom")
	if err != nil {
		t.Fatalf("new feed: %v", err)
	}

	from := time.Date(2021, 1, 1, 0, 0, 0, 0, time.UTC)
	var first []string
	var lastPage string
	pagesRead := 0
	err = reader.ReadResumable(context.Background(), app.Window{From: from}, app.Resume{}, func(publication domain.Publication) error {
		first = append(first, publication.Reference.ID)
		return nil
	}, func(page app.Page) error {
		pagesRead++
		lastPage = page.Location
		return nil
	})
	if err == nil {
		t.Fatal("first read succeeded, want the second page to fail")
	}
	if len(first) != 2 || pagesRead != 1 {
		t.Fatalf("first read yielded %v after %d pages, want the whole first page only", first, pagesRead)
	}

	atomic.StoreInt32(&broken, 0)
	var second []string
	var resumedPages int
	if err := reader.ReadResumable(context.Background(), app.Window{From: from}, app.Resume{Page: lastPage}, func(publication domain.Publication) error {
		second = append(second, publication.Reference.ID)
		return nil
	}, func(app.Page) error {
		resumedPages++
		return nil
	}); err != nil {
		t.Fatalf("resumed read: %v", err)
	}

	// The run restarts at the page where it stopped, so the page is read in
	// full again and both entries stamped at the same instant are reprocessed:
	// identity, not page order, resolves the tie.
	want := []string{"urn:test:a", "urn:test:b", "urn:test:c", "urn:test:d"}
	if len(second) != len(want) {
		t.Fatalf("resumed ids = %v, want %v", second, want)
	}
	for index, id := range want {
		if second[index] != id {
			t.Fatalf("resumed ids = %v, want %v", second, want)
		}
	}
	if resumedPages != 3 {
		t.Fatalf("resumed read finished %d pages, want 3", resumedPages)
	}
}

func TestFeedRecoversTheAnnouncementBeforeTheCutOnTheBoundaryPage(t *testing.T) {
	pages := map[string]string{
		"/feed.atom": atomPage(
			feedEntry("urn:test:new", "2021-01-02T10:00:00Z", "F-NEW"),
			feedEntry("urn:test:previous", "2020-12-01T10:00:00Z", "F-OLD"),
		),
	}
	server := serve(t, pages, nil)

	reader, err := NewFeed(Profiles, server.URL+"/feed.atom")
	if err != nil {
		t.Fatalf("new feed: %v", err)
	}
	from := time.Date(2021, 1, 1, 0, 0, 0, 0, time.UTC)

	read := func(recover bool) []string {
		t.Helper()
		var ids []string
		if err := reader.ReadResumable(context.Background(), app.Window{From: from}, app.Resume{RecoverBoundary: recover}, func(publication domain.Publication) error {
			ids = append(ids, publication.Reference.ID)
			return nil
		}, func(app.Page) error { return nil }); err != nil {
			t.Fatalf("read boundary: %v", err)
		}
		return ids
	}

	plain := read(false)
	if len(plain) != 1 || plain[0] != "urn:test:new" {
		t.Fatalf("ids = %v, want only the in-window entry without recovery", plain)
	}
	recovered := read(true)
	if len(recovered) != 2 || recovered[1] != "urn:test:previous" {
		t.Fatalf("ids = %v, want the earlier announcement recovered from the boundary page", recovered)
	}
}

func TestArchiveResumesAtTheLastWholeMember(t *testing.T) {
	members := map[string]string{
		"pkg/20251001.atom": atomPage(feedEntry("urn:test:1", "2025-10-01T10:00:00Z", "F-1")),
		"pkg/20251002.atom": atomPage(feedEntry("urn:test:2", "2025-10-02T10:00:00Z", "F-2")),
		"pkg/20251003.atom": atomPage(feedEntry("urn:test:3", "2025-10-03T10:00:00Z", "F-3")),
	}
	archivePath := writeArchive(t, members)

	reader, err := NewArchive(Profiles, archivePath)
	if err != nil {
		t.Fatalf("new archive: %v", err)
	}

	var ids []string
	if err := reader.ReadResumable(context.Background(), app.Window{}, app.Resume{Page: "pkg/20251002.atom"}, func(publication domain.Publication) error {
		ids = append(ids, publication.Reference.ID)
		return nil
	}, func(app.Page) error { return nil }); err != nil {
		t.Fatalf("resume archive: %v", err)
	}
	if len(ids) != 2 || ids[0] != "urn:test:2" || ids[1] != "urn:test:3" {
		t.Fatalf("ids = %v, want the resumed member and the ones after it", ids)
	}
}

func writeArchive(t *testing.T, members map[string]string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "history.zip")
	file, err := os.Create(path)
	if err != nil {
		t.Fatalf("create archive: %v", err)
	}
	writer := zip.NewWriter(file)
	names := make([]string, 0, len(members))
	for name := range members {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		member, err := writer.Create(name)
		if err != nil {
			t.Fatalf("create member %s: %v", name, err)
		}
		if _, err := member.Write([]byte(members[name])); err != nil {
			t.Fatalf("write member %s: %v", name, err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close archive writer: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("close archive: %v", err)
	}
	return path
}
