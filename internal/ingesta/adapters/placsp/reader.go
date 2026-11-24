package placsp

import (
	"archive/zip"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/hectorruiz23/la-mordida/internal/ingesta/app"
	"github.com/hectorruiz23/la-mordida/internal/ingesta/domain"
)

// Deletion is a feed-level at:deleted-entry: the publication disappeared from
// the platform and there is no snapshot of the annulled state. It is an event
// on top of the last photo, never a publication of its own.
type Deletion struct {
	Ref  string
	When time.Time
	Type string
}

// Reader reads one PLACSP channel: its paginated ATOM feed of novedades or a
// downloaded ZIP archive of history. Both implement the same neutral Reader
// contract as the fixture publisher.
type Reader struct {
	channel     Channel
	feedURL     string
	archivePath string
	startMember string
	client      *http.Client
	onDeletion  func(Deletion) error
}

// Option configures a Reader.
type Option func(*Reader)

// WithHTTPClient replaces the HTTP client used for feed pages.
func WithHTTPClient(client *http.Client) Option {
	return func(reader *Reader) {
		if client != nil {
			reader.client = client
		}
	}
}

// WithDeletion receives the feed-level deletions. Without it a deletion is
// recognised and never turned into a publication, but it is not stored.
func WithDeletion(handler func(Deletion) error) Option {
	return func(reader *Reader) { reader.onDeletion = handler }
}

// WithStartMember starts an archive at a named member instead of the channel's
// base member. It is useful to resume a package at a known file.
func WithStartMember(name string) Option {
	return func(reader *Reader) { reader.startMember = name }
}

// NewFeed reads the novedades ATOM feed of a tenders channel. The reader
// follows rel="next" links, resolves them against the page that carries them
// and stops when the window is exhausted.
func NewFeed(channel Channel, feedURL string, options ...Option) (*Reader, error) {
	if err := channel.validateTender(); err != nil {
		return nil, err
	}
	parsed, err := url.Parse(feedURL)
	if err != nil {
		return nil, fmt.Errorf("placsp: feed %q: %w", feedURL, err)
	}
	if (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return nil, fmt.Errorf("placsp: feed %q is not an absolute http(s) URL", feedURL)
	}

	reader := &Reader{channel: channel, feedURL: feedURL, client: http.DefaultClient}
	for _, option := range options {
		option(reader)
	}
	return reader, nil
}

// NewArchive reads a ZIP archive already on disk. Members are streamed one at
// a time; the archive is never decompressed into memory as a whole.
func NewArchive(channel Channel, archivePath string, options ...Option) (*Reader, error) {
	if err := channel.validateTender(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(archivePath) == "" {
		return nil, errors.New("placsp: archive path is required")
	}

	reader := &Reader{channel: channel, archivePath: archivePath, client: http.DefaultClient}
	for _, option := range options {
		option(reader)
	}
	return reader, nil
}

func (r *Reader) Read(ctx context.Context, window app.Window, yield func(domain.Publication) error) error {
	if yield == nil {
		return errors.New("placsp: yield is required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	switch {
	case r.feedURL != "":
		return r.readFeed(ctx, window, yield)
	case r.archivePath != "":
		return r.readArchive(ctx, window, yield)
	default:
		return errors.New("placsp: reader has no source")
	}
}

// pageScan reports what one ATOM document contributed. allBeforeFrom lets the
// feed pager cut at the window boundary only when a whole page lies outside
// it, so entries reordered inside a page are not lost.
type pageScan struct {
	nextHref      string
	entries       int
	allBeforeFrom bool
}

func (r *Reader) scanPage(ctx context.Context, body io.Reader, window app.Window, yield func(domain.Publication) error) (pageScan, error) {
	decoder := xml.NewDecoder(body)
	scan := pageScan{allBeforeFrom: true}
	foundRoot := false

	for {
		if err := ctx.Err(); err != nil {
			return scan, err
		}

		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return scan, fmt.Errorf("decode page: %w", err)
		}

		start, ok := token.(xml.StartElement)
		if !ok {
			continue
		}

		if !foundRoot {
			if start.Name.Space != nsAtom || start.Name.Local != "feed" {
				return scan, fmt.Errorf("page root is <%s> in %q, want the Atom feed", start.Name.Local, start.Name.Space)
			}
			foundRoot = true
			continue
		}

		if start.Name.Space == nsAtom && start.Name.Local == "link" {
			link, err := decodeNode(decoder, start, 0)
			if err != nil {
				return scan, err
			}
			if link.attr("rel") == "next" && scan.nextHref == "" {
				scan.nextHref = link.attr("href")
			}
			continue
		}

		if start.Name.Space == nsAtom && start.Name.Local == "entry" {
			entry, err := decodeNode(decoder, start, 0)
			if err != nil {
				return scan, err
			}
			publication, err := r.translate(entry)
			if err != nil {
				return scan, err
			}
			scan.entries++
			if !window.From.IsZero() && !publication.UpdatedAt.Before(window.From) {
				scan.allBeforeFrom = false
			}
			if inWindow(publication.UpdatedAt, window) {
				if err := yield(publication); err != nil {
					return scan, err
				}
			}
			continue
		}

		if start.Name.Space == nsTombstone && start.Name.Local == "deleted-entry" {
			deletion, err := r.translateDeletion(decoder, start)
			if err != nil {
				return scan, err
			}
			if r.onDeletion != nil && inWindow(deletion.When, window) {
				if err := r.onDeletion(deletion); err != nil {
					return scan, err
				}
			}
			continue
		}

		if err := decoder.Skip(); err != nil {
			return scan, fmt.Errorf("skip <%s>: %w", start.Name.Local, err)
		}
	}

	if !foundRoot {
		return scan, errors.New("page carries no Atom feed")
	}
	return scan, nil
}

func (r *Reader) translateDeletion(decoder *xml.Decoder, start xml.StartElement) (Deletion, error) {
	deleted, err := decodeNode(decoder, start, 0)
	if err != nil {
		return Deletion{}, err
	}

	deletion := Deletion{
		Ref:  deleted.attr("ref"),
		Type: deleted.child(nsTombstone, "comment").attr("type"),
	}
	when := deleted.attr("when")
	if when == "" {
		return deletion, nil
	}
	deletion.When, err = time.Parse(time.RFC3339, when)
	if err != nil {
		return Deletion{}, fmt.Errorf("deleted-entry %s when %q: %w", deletion.Ref, when, err)
	}
	return deletion, nil
}

func (r *Reader) readFeed(ctx context.Context, window app.Window, yield func(domain.Publication) error) error {
	visited := make(map[string]bool)
	current := r.feedURL

	for current != "" {
		if err := ctx.Err(); err != nil {
			return err
		}
		if visited[current] {
			return fmt.Errorf("placsp: feed page cycle at %s", current)
		}
		visited[current] = true

		body, err := r.fetch(ctx, current)
		if err != nil {
			return err
		}
		scan, scanErr := r.scanPage(ctx, body, window, yield)
		body.Close()
		if scanErr != nil {
			return fmt.Errorf("placsp: feed page %s: %w", current, scanErr)
		}

		// The novedades feed is served newest first: a whole page older than
		// the window is the boundary, and the older pages are not read.
		if !window.From.IsZero() && scan.entries > 0 && scan.allBeforeFrom {
			return nil
		}
		if scan.nextHref == "" {
			return nil
		}

		next, err := resolveFeedNext(current, scan.nextHref)
		if err != nil {
			return err
		}
		current = next
	}
	return nil
}

func (r *Reader) readArchive(ctx context.Context, window app.Window, yield func(domain.Publication) error) error {
	archive, err := zip.OpenReader(r.archivePath)
	if err != nil {
		return fmt.Errorf("placsp: open archive %s: %w", r.archivePath, err)
	}
	defer archive.Close()

	members, names := archiveMembers(archive.File)
	if len(names) == 0 {
		return fmt.Errorf("placsp: archive %s carries no .atom members", r.archivePath)
	}

	start := r.startMember
	if start == "" {
		start = r.channel.baseMember()
	}
	if _, ok := members[start]; !ok {
		if r.startMember != "" {
			return fmt.Errorf("placsp: archive %s has no member %s", r.archivePath, r.startMember)
		}
		start = names[0]
	}

	visited := make(map[string]bool)
	for start != "" {
		// One generated chain: each member names the page that follows it.
		// A repeated member inside the same chain is a cycle; a member read
		// by an earlier chain is simply the end of this one.
		chain := make(map[string]bool)
		current := start
		for current != "" {
			if chain[current] {
				return fmt.Errorf("placsp: archive page cycle at %s", current)
			}
			chain[current] = true
			visited[current] = true

			if err := ctx.Err(); err != nil {
				return err
			}

			member := members[current]
			stream, err := member.Open()
			if err != nil {
				return fmt.Errorf("placsp: open archive member %s: %w", current, err)
			}
			scan, scanErr := r.scanPage(ctx, stream, window, yield)
			stream.Close()
			if scanErr != nil {
				return fmt.Errorf("placsp: archive member %s: %w", current, scanErr)
			}

			// An archive is finite and its members are not guaranteed to be
			// in update order, so every member is read and the window filters
			// entries. Cutting here could drop a late correction that the
			// package stores out of order.
			next, ok := resolveMember(current, scan.nextHref, members)
			switch {
			case ok && !visited[next]:
				current = next
			case ok && chain[next]:
				return fmt.Errorf("placsp: archive page cycle at %s", next)
			default:
				current = ""
			}
		}

		start = firstUnvisited(names, visited)
	}
	return nil
}

func (r *Reader) fetch(ctx context.Context, location string) (io.ReadCloser, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, location, nil)
	if err != nil {
		return nil, fmt.Errorf("placsp: build request for %s: %w", location, err)
	}
	request.Header.Set("Accept", "application/atom+xml, application/xml;q=0.9")
	request.Header.Set("User-Agent", "la-mordida/1 placsp-reader")

	response, err := r.client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("placsp: fetch %s: %w", location, err)
	}
	if response.StatusCode < 200 || response.StatusCode > 299 {
		detail, _ := io.ReadAll(io.LimitReader(response.Body, 512))
		response.Body.Close()
		return nil, fmt.Errorf("placsp: fetch %s: %s: %s", location, response.Status, strings.TrimSpace(string(detail)))
	}
	return response.Body, nil
}

func inWindow(instant time.Time, window app.Window) bool {
	if !window.From.IsZero() && instant.Before(window.From) {
		return false
	}
	if !window.To.IsZero() && instant.After(window.To) {
		return false
	}
	return true
}

func resolveFeedNext(current, href string) (string, error) {
	base, err := url.Parse(current)
	if err != nil {
		return "", fmt.Errorf("placsp: parse page URL %s: %w", current, err)
	}
	reference, err := url.Parse(href)
	if err != nil {
		return "", fmt.Errorf("placsp: parse next link %q: %w", href, err)
	}
	next := base.ResolveReference(reference)
	if next.Scheme != "http" && next.Scheme != "https" {
		return "", fmt.Errorf("placsp: next link %q resolves outside http(s)", href)
	}
	next.Fragment = ""
	return next.String(), nil
}

// archiveMembers indexes the .atom members. Member names embed the generation
// instant, so lexical order is the package's chronological order.
func archiveMembers(files []*zip.File) (map[string]*zip.File, []string) {
	members := make(map[string]*zip.File, len(files))
	names := make([]string, 0, len(files))
	for _, file := range files {
		if !strings.EqualFold(path.Ext(file.Name), ".atom") {
			continue
		}
		members[file.Name] = file
		names = append(names, file.Name)
	}
	sort.Strings(names)
	return members, names
}

// resolveMember resolves a next link against the member that carries it, not
// against the archive path: inside a package, links are sibling members.
func resolveMember(current, href string, members map[string]*zip.File) (string, bool) {
	if href == "" {
		return "", false
	}
	reference, err := url.Parse(href)
	if err != nil {
		return "", false
	}

	name := reference.Path
	if reference.IsAbs() || name == "" {
		name = path.Base(reference.Path)
	} else {
		name = path.Join(path.Dir(current), name)
	}
	name = path.Clean(name)
	_, ok := members[name]
	return name, ok
}

func firstUnvisited(names []string, visited map[string]bool) string {
	for _, name := range names {
		if !visited[name] {
			return name
		}
	}
	return ""
}
