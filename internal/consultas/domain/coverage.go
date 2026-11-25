package domain

import "errors"

// ErrExpedienteNotFound says the requested procedure was never imported. It
// is not an empty detail: there is no row to read.
var ErrExpedienteNotFound = errors.New("expediente not found")

// Coverage is the load history the reader is shown beside a ranking: which
// channels were read, which intervals they covered and whether a load was
// complete, partial or a gap. A complete load with zero publications is a
// measured zero; a partial load says the period was not walked to its end.
type Coverage struct {
	Sources []CoverageSource `json:"sources"`
}

// CoverageSource is one channel and the spans it covered.
type CoverageSource struct {
	Source string         `json:"source"`
	Spans  []CoverageSpan `json:"spans"`
}

// CoverageSpan is one recorded load: its kind, its half-open period, the
// state it reached and its counters.
type CoverageSpan struct {
	Kind           string `json:"kind"`
	PeriodFrom     string `json:"period_from,omitempty"`
	PeriodUntil    string `json:"period_until,omitempty"`
	UntilExclusive bool   `json:"until_exclusive"`
	State          string `json:"state"`
	Publications   int    `json:"publications"`
	Pages          int    `json:"pages"`
	LastCompleteAt string `json:"last_complete_at,omitempty"`
	LastPage       string `json:"last_page,omitempty"`
	Detail         string `json:"detail,omitempty"`
}
