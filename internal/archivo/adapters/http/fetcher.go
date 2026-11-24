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

func (f *Fetcher) Fetch(ctx context.Context, url string) (io.ReadCloser, string, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, "", fmt.Errorf("download %s: %w", url, err)
	}

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
