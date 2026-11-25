package placsp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hectorruiz23/la-mordida/internal/ingesta/app"
	"github.com/hectorruiz23/la-mordida/internal/ingesta/domain"
)

func serve(t *testing.T, pages map[string]string, hits map[string]*int32) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if counter, ok := hits[request.URL.Path]; ok {
			atomic.AddInt32(counter, 1)
		}
		page, ok := pages[request.URL.Path]
		if !ok {
			http.NotFound(writer, request)
			return
		}
		writer.Header().Set("Content-Type", "application/atom+xml")
		_, _ = writer.Write([]byte(page))
	}))
	t.Cleanup(server.Close)
	return server
}

func feedEntry(id, updated, folder string) string {
	return atomEntry(id, updated, folder, `<cac:ProcurementProject><cbc:Name>Objeto</cbc:Name></cac:ProcurementProject>`)
}

func TestFeedFollowsRelativeNextAndCutsOutsideTheWindow(t *testing.T) {
	var page2, page3 int32
	pages := map[string]string{
		"/sindicacion/feed.atom": atomPage(
			feedEntry("urn:test:1", "2025-10-20T10:00:00Z", "F-1"),
			feedEntry("urn:test:2", "2025-10-19T10:00:00Z", "F-2"),
			atomLink("page2.atom", "next"),
		),
		"/sindicacion/page2.atom": atomPage(
			feedEntry("urn:test:3", "2025-09-20T10:00:00Z", "F-3"),
			feedEntry("urn:test:4", "2025-09-19T10:00:00Z", "F-4"),
			atomLink("page3.atom", "next"),
		),
		"/sindicacion/page3.atom": atomPage(
			feedEntry("urn:test:5", "2025-08-01T10:00:00Z", "F-5"),
		),
	}
	hits := map[string]*int32{"/sindicacion/page2.atom": &page2, "/sindicacion/page3.atom": &page3}
	server := serve(t, pages, hits)

	reader, err := NewFeed(Profiles, server.URL+"/sindicacion/feed.atom")
	if err != nil {
		t.Fatalf("new feed: %v", err)
	}
	from := time.Date(2025, 10, 1, 0, 0, 0, 0, time.UTC)

	var publications []domain.Publication
	if err := reader.Read(context.Background(), app.Window{From: from}, func(publication domain.Publication) error {
		publications = append(publications, publication)
		return nil
	}); err != nil {
		t.Fatalf("read feed: %v", err)
	}

	if len(publications) != 2 {
		t.Fatalf("publications = %d, want the two in-window entries", len(publications))
	}
	if publications[0].Reference.ID != "urn:test:1" || publications[1].Reference.ID != "urn:test:2" {
		t.Errorf("ids = %q, %q", publications[0].Reference.ID, publications[1].Reference.ID)
	}
	if page2 != 1 {
		t.Errorf("page2 was fetched %d times, want 1 (the relative next was resolved against the feed)", page2)
	}
	if page3 != 0 {
		t.Errorf("page3 was fetched %d times, want 0 (the window was already exhausted)", page3)
	}
}

func TestFeedReorderingInsideAPageDoesNotCut(t *testing.T) {
	var page2 int32
	pages := map[string]string{
		"/feed.atom": atomPage(
			feedEntry("urn:test:old", "2025-09-01T10:00:00Z", "F-OLD"),
			feedEntry("urn:test:new", "2025-10-20T10:00:00Z", "F-NEW"),
			atomLink("page2.atom", "next"),
		),
		"/page2.atom": atomPage(
			feedEntry("urn:test:newer", "2025-10-21T10:00:00Z", "F-NEWER"),
		),
	}
	hits := map[string]*int32{"/page2.atom": &page2}
	server := serve(t, pages, hits)

	reader, err := NewFeed(Profiles, server.URL+"/feed.atom")
	if err != nil {
		t.Fatalf("new feed: %v", err)
	}
	from := time.Date(2025, 10, 1, 0, 0, 0, 0, time.UTC)

	var ids []string
	if err := reader.Read(context.Background(), app.Window{From: from}, func(publication domain.Publication) error {
		ids = append(ids, publication.Reference.ID)
		return nil
	}); err != nil {
		t.Fatalf("read feed: %v", err)
	}

	if len(ids) != 2 || ids[0] != "urn:test:new" || ids[1] != "urn:test:newer" {
		t.Fatalf("ids = %v, want the in-window entries even though one was reordered first", ids)
	}
	if page2 != 1 {
		t.Errorf("page2 fetched %d times, want 1", page2)
	}
}

func TestFeedDetectsPageCycle(t *testing.T) {
	pages := map[string]string{
		"/a.atom": atomPage(feedEntry("urn:test:a", "2025-10-20T10:00:00Z", "F-A"), atomLink("b.atom", "next")),
		"/b.atom": atomPage(feedEntry("urn:test:b", "2025-10-19T10:00:00Z", "F-B"), atomLink("a.atom", "next")),
	}
	server := serve(t, pages, nil)

	reader, err := NewFeed(Profiles, server.URL+"/a.atom")
	if err != nil {
		t.Fatalf("new feed: %v", err)
	}
	err = reader.Read(context.Background(), app.Window{}, func(domain.Publication) error { return nil })
	if err == nil || !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("read cycle: err = %v, want a cycle error", err)
	}
}

func TestArchiveFollowsNextRelativeToTheMemberAndReadsEveryMember(t *testing.T) {
	members := map[string]string{
		"pkg/20251001.atom": atomPage(feedEntry("urn:test:1", "2025-10-01T10:00:00Z", "F-1"), atomLink("20251002.atom", "next")),
		"pkg/20251002.atom": atomPage(feedEntry("urn:test:2", "2025-10-02T10:00:00Z", "F-2")),
		"pkg/20251003.atom": atomPage(feedEntry("urn:test:3", "2025-10-03T10:00:00Z", "F-3")),
	}

	publications, err := readArchiveMembers(t, Profiles, app.Window{}, members)
	if err != nil {
		t.Fatalf("read archive: %v", err)
	}
	if len(publications) != 3 {
		t.Fatalf("publications = %d, want all three members", len(publications))
	}
	for index, want := range []string{"urn:test:1", "urn:test:2", "urn:test:3"} {
		if publications[index].Reference.ID != want {
			t.Errorf("publication %d id = %q, want %q", index, publications[index].Reference.ID, want)
		}
	}
}

func TestArchiveDetectsPageCycle(t *testing.T) {
	members := map[string]string{
		"a.atom": atomPage(feedEntry("urn:test:a", "2025-10-01T10:00:00Z", "F-A"), atomLink("b.atom", "next")),
		"b.atom": atomPage(feedEntry("urn:test:b", "2025-10-02T10:00:00Z", "F-B"), atomLink("a.atom", "next")),
	}
	_, err := readArchiveMembers(t, Profiles, app.Window{}, members)
	if err == nil || !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("read cycle: err = %v, want a cycle error", err)
	}
}

func TestDeletedEntryIsAnEventNotAPublication(t *testing.T) {
	page := atomPage(
		feedEntry("urn:test:kept", "2025-10-20T10:00:00Z", "F-KEPT"),
		`<at:deleted-entry ref="urn:test:gone" when="2025-10-21T12:00:00Z"><at:comment type="ANULADA"/></at:deleted-entry>`,
	)
	pages := map[string]string{"/feed.atom": page}
	server := serve(t, pages, nil)

	var deletions []Deletion
	reader, err := NewFeed(Profiles, server.URL+"/feed.atom", WithDeletion(func(deletion Deletion) error {
		deletions = append(deletions, deletion)
		return nil
	}))
	if err != nil {
		t.Fatalf("new feed: %v", err)
	}

	var publications []domain.Publication
	if err := reader.Read(context.Background(), app.Window{}, func(publication domain.Publication) error {
		publications = append(publications, publication)
		return nil
	}); err != nil {
		t.Fatalf("read feed: %v", err)
	}

	if len(publications) != 1 || publications[0].Reference.ID != "urn:test:kept" {
		t.Fatalf("publications = %+v, want only the entry", publications)
	}
	if len(deletions) != 1 || deletions[0].Ref != "urn:test:gone" || deletions[0].Type != "ANULADA" {
		t.Fatalf("deletions = %+v", deletions)
	}
}

func TestOnlyTenderChannelsAreAccepted(t *testing.T) {
	for _, channel := range []Channel{InHouse, Consultations} {
		if _, err := NewFeed(channel, "https://example.test/feed.atom"); err == nil {
			t.Errorf("channel %s was accepted, want it refused as not a tender channel", channel)
		}
	}
	if _, err := NewFeed("unknown", "https://example.test/feed.atom"); err == nil {
		t.Error("unknown channel was accepted")
	}
	if _, err := NewFeed(Profiles, "file:///tmp/feed.atom"); err == nil {
		t.Error("a non-http feed URL was accepted")
	}
}

func TestABadEntryIsCountedAndDoesNotDiscardThePage(t *testing.T) {
	server := serve(t, map[string]string{
		"/feed.atom": atomPage(
			feedEntry("urn:test:good", "2025-10-20T10:00:00Z", "F-1"),
			feedEntry("urn:test:bad", "2025-10-20T11:00:00Z", ""),
		),
	}, nil)

	reader, err := NewFeed(Profiles, server.URL+"/feed.atom")
	if err != nil {
		t.Fatalf("new feed: %v", err)
	}

	var (
		publications []domain.Publication
		pages        []app.Page
	)
	if err := reader.ReadResumable(context.Background(), app.Window{}, app.Resume{},
		func(publication domain.Publication) error {
			publications = append(publications, publication)
			return nil
		},
		func(page app.Page) error {
			pages = append(pages, page)
			return nil
		},
	); err != nil {
		t.Fatalf("read feed: %v", err)
	}

	if len(publications) != 1 || publications[0].Reference.ID != "urn:test:good" {
		t.Fatalf("publications = %+v, want the one readable entry", publications)
	}
	if len(pages) != 1 || pages[0].Errors != 1 {
		t.Fatalf("pages = %+v, want one page with one counted error", pages)
	}
	if !strings.Contains(pages[0].ErrorDetail, "ContractFolderID") {
		t.Errorf("error detail = %q, want the missing folder reason", pages[0].ErrorDetail)
	}
}
