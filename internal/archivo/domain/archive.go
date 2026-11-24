// Package domain owns the archived bytes of a document, the places they were
// obtained from and the link to the publication and fact they sustain. It
// imports only the standard library: no fetch, no SQLite and no filesystem.
package domain

import (
	"fmt"
	"time"
)

// State tells an announced link from a download that failed and can be
// retried, and from the evidence whose bytes are actually stored.
type State string

const (
	Announced State = "announced"
	Pending   State = "pending"
	Evidence  State = "evidence"
)

func (s State) Valid() bool {
	switch s {
	case Announced, Pending, Evidence:
		return true
	}
	return false
}

// Publication identifies the stored publication version a document belongs to.
type Publication struct {
	Source     string
	ExternalID string
	UpdatedAt  time.Time
}

// Link is one document a publication announces, tied to the fact it sustains.
// An empty TargetKind means the publication itself. SHA256 is the hash the
// source announced, when it publishes one: it lets a retry reuse bytes already
// on disk without downloading again.
type Link struct {
	Publication Publication
	TargetKind  string
	TargetRef   string
	URL         string
	Title       string
	State       State
	SHA256      string
	MIME        string
	ObtainedAt  time.Time
	Attempts    int
	LastError   string
}

func (l Link) Validate() error {
	if l.Publication.Source == "" || l.Publication.ExternalID == "" {
		return fmt.Errorf("link %s: publication source and external id are required", l.URL)
	}
	if l.Publication.UpdatedAt.IsZero() {
		return fmt.Errorf("link %s: publication updated instant is required", l.URL)
	}
	switch l.TargetKind {
	case "publication", "expediente":
	case "lot", "award":
		if l.TargetRef == "" {
			return fmt.Errorf("link %s: %s target needs a reference", l.URL, l.TargetKind)
		}
	default:
		return fmt.Errorf("link %s: unknown target kind %q", l.URL, l.TargetKind)
	}
	if l.URL == "" {
		return fmt.Errorf("link: url is required")
	}
	if !l.State.Valid() {
		return fmt.Errorf("link %s: unknown state %q", l.URL, l.State)
	}
	return nil
}

// Object is immutable content addressed by its SHA-256. MIME is what the
// source announced; empty means unknown, not application/octet-stream.
type Object struct {
	SHA256   string
	MIME     string
	Bytes    int64
	StoredAt time.Time
}

// Origin is one place an object was obtained from, and when. The same bytes
// from two URLs keep two origins; one URL that served two contents keeps one
// origin for each hash.
type Origin struct {
	URL        string
	ObtainedAt time.Time
	Object     Object
}
