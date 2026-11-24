package placsp

import (
	"archive/zip"
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/hectorruiz23/la-mordida/internal/ingesta/app"
	"github.com/hectorruiz23/la-mordida/internal/ingesta/domain"
)

const testdataRoot = "../../../../testdata/placsp"

// readArchiveMembers writes the given members into a temporary ZIP, reads it
// through the real archive pager and returns every yielded publication.
func readArchiveMembers(t *testing.T, channel Channel, window app.Window, members map[string]string, options ...Option) ([]domain.Publication, error) {
	t.Helper()

	archivePath := filepath.Join(t.TempDir(), "history.zip")
	file, err := os.Create(archivePath)
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

	reader, err := NewArchive(channel, archivePath, options...)
	if err != nil {
		t.Fatalf("new archive: %v", err)
	}

	var publications []domain.Publication
	readErr := reader.Read(context.Background(), window, func(publication domain.Publication) error {
		publications = append(publications, publication)
		return nil
	})
	return publications, readErr
}

func readFixture(t *testing.T, channel Channel, relativePath string) domain.Publication {
	t.Helper()

	contents, err := os.ReadFile(filepath.Join(testdataRoot, relativePath))
	if err != nil {
		t.Fatalf("read fixture %s: %v", relativePath, err)
	}

	publications, err := readArchiveMembers(t, channel, app.Window{}, map[string]string{"publication.atom": string(contents)})
	if err != nil {
		t.Fatalf("read fixture %s: %v", relativePath, err)
	}
	if len(publications) != 1 {
		t.Fatalf("fixture %s yielded %d publications, want 1", relativePath, len(publications))
	}
	return publications[0]
}

// atomPage builds the smallest Atom feed that carries the given entries.
func atomPage(entries ...string) string {
	return `<?xml version="1.0" encoding="UTF-8"?>
<feed xmlns="http://www.w3.org/2005/Atom"
      xmlns:cbc="urn:dgpe:names:draft:codice:schema:xsd:CommonBasicComponents-2"
      xmlns:cac="urn:dgpe:names:draft:codice:schema:xsd:CommonAggregateComponents-2"
      xmlns:cbc-place-ext="urn:dgpe:names:draft:codice-place-ext:schema:xsd:CommonBasicComponents-2"
      xmlns:cac-place-ext="urn:dgpe:names:draft:codice-place-ext:schema:xsd:CommonAggregateComponents-2"
      xmlns:at="http://purl.org/atompub/tombstones/1.0">
  <id>urn:test:feed</id>
  <title>test feed</title>
  <updated>2025-10-24T00:00:00Z</updated>
  <author><name>test</name></author>
` + strings.Join(entries, "") + `
</feed>`
}

// atomEntry builds one entry with a buyer and a folder. extra is appended
// inside ContractFolderStatus so a test can add project, lots or awards.
func atomEntry(id, updated, folder, extra string) string {
	return `<entry>
  <id>` + id + `</id>
  <updated>` + updated + `</updated>
  <cac-place-ext:ContractFolderStatus>
    <cbc:ContractFolderID>` + folder + `</cbc:ContractFolderID>
    <cbc-place-ext:ContractFolderStatusCode>ADJ</cbc-place-ext:ContractFolderStatusCode>
    <cac-place-ext:LocatedContractingParty>
      <cac:Party>
        <cac:PartyIdentification><cbc:ID schemeName="NIF">P0000000A</cbc:ID></cac:PartyIdentification>
        <cac:PartyName><cbc:Name>Organismo de prueba</cbc:Name></cac:PartyName>
      </cac:Party>
    </cac-place-ext:LocatedContractingParty>
    ` + extra + `
  </cac-place-ext:ContractFolderStatus>
</entry>`
}

func atomLink(href, rel string) string {
	return `<link rel="` + rel + `" href="` + href + `"/>`
}
