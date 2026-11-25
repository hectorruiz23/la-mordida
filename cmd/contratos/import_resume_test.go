package main

import (
	"database/sql"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func testAtomPage(entries ...string) string {
	return `<?xml version="1.0" encoding="UTF-8"?>
<feed xmlns="http://www.w3.org/2005/Atom"
      xmlns:cbc="urn:dgpe:names:draft:codice:schema:xsd:CommonBasicComponents-2"
      xmlns:cac="urn:dgpe:names:draft:codice:schema:xsd:CommonAggregateComponents-2"
      xmlns:cac-place-ext="urn:dgpe:names:draft:codice-place-ext:schema:xsd:CommonAggregateComponents-2">
  <id>urn:test:feed</id>
  <title>test feed</title>
  <updated>2025-10-24T00:00:00Z</updated>
` + strings.Join(entries, "") + `
</feed>`
}

func testAtomEntry(id, updated, folder string) string {
	return `<entry>
  <id>` + id + `</id>
  <updated>` + updated + `</updated>
  <cac-place-ext:ContractFolderStatus>
    <cbc:ContractFolderID>` + folder + `</cbc:ContractFolderID>
    <cbc:ContractFolderStatusCode>ADJ</cbc:ContractFolderStatusCode>
    <cac-place-ext:LocatedContractingParty>
      <cac:Party>
        <cac:PartyIdentification><cbc:ID schemeName="NIF">P0000000A</cbc:ID></cac:PartyIdentification>
        <cac:PartyName><cbc:Name>Organismo de prueba</cbc:Name></cac:PartyName>
      </cac:Party>
    </cac-place-ext:LocatedContractingParty>
  </cac-place-ext:ContractFolderStatus>
</entry>`
}

func testAtomLink(href, rel string) string {
	return `<link rel="` + rel + `" href="` + href + `"/>`
}

func publicationKeys(t *testing.T, path string) []string {
	t.Helper()
	database, err := sql.Open("sqlite", "file:"+path+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer database.Close()

	rows, err := database.Query(`
		SELECT source || '|' || external_id || '|' || published_at || '|' || body_sha256
		FROM publication ORDER BY 1`)
	if err != nil {
		t.Fatalf("list publications: %v", err)
	}
	defer rows.Close()
	var keys []string
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			t.Fatalf("scan publication: %v", err)
		}
		keys = append(keys, key)
	}
	return keys
}

func spanStates(t *testing.T, path string) []string {
	t.Helper()
	database, err := sql.Open("sqlite", "file:"+path+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer database.Close()

	rows, err := database.Query(`SELECT state FROM import_span ORDER BY id`)
	if err != nil {
		t.Fatalf("list spans: %v", err)
	}
	defer rows.Close()
	var states []string
	for rows.Next() {
		var state string
		if err := rows.Scan(&state); err != nil {
			t.Fatalf("scan span: %v", err)
		}
		states = append(states, state)
	}
	return states
}

func TestCLIResumesAFeedAfterTheSecondPageFails(t *testing.T) {
	var broken int32 = 1
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/page2.atom" && atomic.LoadInt32(&broken) == 1 {
			http.Error(writer, "down", http.StatusServiceUnavailable)
			return
		}
		pages := map[string]string{
			"/feed.atom": testAtomPage(
				testAtomEntry("urn:test:a", "2021-01-05T10:00:00Z", "F-A"),
				testAtomEntry("urn:test:b", "2021-01-05T10:00:00Z", "F-B"),
				testAtomLink("page2.atom", "next"),
			),
			"/page2.atom": testAtomPage(
				testAtomEntry("urn:test:c", "2021-01-04T10:00:00Z", "F-C"),
				testAtomLink("page3.atom", "next"),
			),
			"/page3.atom": testAtomPage(
				testAtomEntry("urn:test:d", "2021-01-03T10:00:00Z", "F-D"),
			),
		}
		page, ok := pages[request.URL.Path]
		if !ok {
			http.NotFound(writer, request)
			return
		}
		_, _ = writer.Write([]byte(page))
	}))
	t.Cleanup(server.Close)

	dirty := filepath.Join(t.TempDir(), "dirty.db")
	first := []string{"-db", dirty, "-feed", server.URL + "/feed.atom", "-from", "2021-01-01", "-until", "2022-01-01"}
	firstErr := runImport(first)
	if firstErr == nil {
		t.Fatal("first import succeeded, want the second page to fail")
	}
	_ = firstErr

	atomic.StoreInt32(&broken, 0)
	retry := []string{"-db", dirty, "-feed", server.URL + "/feed.atom", "-since-last", "-until", "2022-01-01"}
	if err := runImport(retry); err != nil {
		t.Fatalf("retry: %v", err)
	}

	clean := filepath.Join(t.TempDir(), "clean.db")
	cleanImport := []string{"-db", clean, "-feed", server.URL + "/feed.atom", "-from", "2021-01-01", "-until", "2022-01-01"}
	if err := runImport(cleanImport); err != nil {
		t.Fatalf("clean load: %v", err)
	}

	dirtyKeys := publicationKeys(t, dirty)
	cleanKeys := publicationKeys(t, clean)
	if strings.Join(dirtyKeys, "\n") != strings.Join(cleanKeys, "\n") {
		t.Fatalf("retry result differs from a clean load:\n retry: %v\n clean: %v", dirtyKeys, cleanKeys)
	}
	if len(dirtyKeys) != 4 {
		t.Fatalf("publications = %d, want the four entries", len(dirtyKeys))
	}
	if states := spanStates(t, dirty); fmt.Sprint(states) != "[partial complete]" {
		t.Fatalf("spans = %v, want the failed run partial and the retry complete", states)
	}
}
