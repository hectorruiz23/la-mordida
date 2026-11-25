// Package http downloads the documents a publication announces. It is the only
// package that knows the network.
package http

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Fetcher downloads a document over HTTP and returns its body with the MIME
// type the server announced.
type Fetcher struct {
	client *http.Client
}

func New(client *http.Client) *Fetcher {
	if client == nil {
		client = &http.Client{Timeout: 60 * time.Second}
	}
	return &Fetcher{client: client}
}

// userAgent is what the source accepts for a document. PLACSP sits behind a
// WAF that answers "Request Rejected" with a 244-byte HTML page to anything
// that does not present itself as a browser: a self-identifying agent gets no
// document, not even when it starts with "Mozilla/5.0".
const userAgent = "Mozilla/5.0 (X11; Linux aarch64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0 Safari/537.36"

func (f *Fetcher) Fetch(ctx context.Context, url string) (io.ReadCloser, string, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, "", fmt.Errorf("download %s: %w", url, err)
	}
	request.Header.Set("User-Agent", userAgent)
	request.Header.Set("Accept-Language", "es-ES,es;q=0.9")

	response, err := f.client.Do(request)
	if err != nil {
		return nil, "", fmt.Errorf("download %s: %w", url, err)
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		response.Body.Close()
		return nil, "", fmt.Errorf("download %s: status %d", url, response.StatusCode)
	}

	mime := response.Header.Get("Content-Type")
	if index := strings.IndexByte(mime, ';'); index >= 0 {
		mime = mime[:index]
	}
	return response.Body, strings.TrimSpace(mime), nil
}
