// Package domain is the neutral publication a source reader hands over. It is
// not PLACSP and it is not SQLite: a reader translates its own format into
// these facts, and the importer translates these facts into contracts.
package domain

import (
	"time"

	"github.com/hectorruiz23/la-mordida/internal/kernel"
)

// Scope says what a fact is about.
type Scope string

const (
	ScopeExpediente Scope = "expediente"
	ScopeLot        Scope = "lote"
	ScopeAward      Scope = "adjudicacion"
)

// Fact carries the scope and the source identifiers of a fact. The fields
// below each fact are its payload; the identifiers stay explicit so a fact
// can be joined without guessing from a name.
type Fact struct {
	Scope       Scope             `json:"scope"`
	Identifiers map[string]string `json:"identifiers,omitempty"`
}

type Reference struct {
	Source string `json:"source"`
	ID     string `json:"id"`
	URL    string `json:"url,omitempty"`
}

type Party struct {
	Scheme string `json:"scheme"`
	ID     string `json:"id"`
	Name   string `json:"name,omitempty"`
}

type Contract struct {
	ID        string       `json:"id"`
	IssueDate *kernel.Date `json:"issue_date,omitempty"`
}

type Document struct {
	Kind  string `json:"kind,omitempty"`
	Ref   string `json:"ref,omitempty"`
	URL   string `json:"url"`
	Hash  string `json:"sha256,omitempty"`
	Title string `json:"title,omitempty"`
}

// CPV is one published classification code. Code keeps the source spelling;
// Family is the selected family the code fell into and Reason says why. A code
// outside every selected family is kept with Included false, not dropped.
type CPV struct {
	Code     string `json:"code"`
	Family   string `json:"family,omitempty"`
	Included bool   `json:"included"`
	Reason   string `json:"reason"`
}

// Classification is the CPV selection of one scope. Source says whether the
// codes were published for that scope, inherited from the expediente or are
// unknown; Ambiguous marks an inference the source does not support.
type Classification struct {
	Codes     []CPV  `json:"codes,omitempty"`
	Source    string `json:"source"`
	Ambiguous bool   `json:"ambiguous,omitempty"`
	Reason    string `json:"reason,omitempty"`
}

const (
	ClassificationPublished = "published"
	ClassificationInherited = "inherited"
	ClassificationUnknown   = "unknown"
)

// Notice is one legal event as the snapshot names it. IssueDates keeps every
// date the source published under the same notice and medium, including a
// correction's second date: PLACSP does not label corrections.
type Notice struct {
	Type       string        `json:"type"`
	Medium     string        `json:"medium,omitempty"`
	IssueDates []kernel.Date `json:"issue_dates,omitempty"`
}

// Publication is one snapshot of one expediente as the source publishes it.
// The same Reference may arrive again with a newer UpdatedAt: those are
// versions, not separate tenders.
type Publication struct {
	Reference  Reference  `json:"reference"`
	UpdatedAt  time.Time  `json:"updated_at"`
	Expediente Expediente `json:"expediente"`
	Lots       []Lot      `json:"lots,omitempty"`
	Awards     []Award    `json:"awards,omitempty"`
	Notices    []Notice   `json:"notices,omitempty"`
	Documents  []Document `json:"documents,omitempty"`
}

type Expediente struct {
	Fact
	FolderID string         `json:"folder_id"`
	Status   string         `json:"status,omitempty"`
	Object   string         `json:"object,omitempty"`
	Buyer    Party          `json:"buyer"`
	Budget   []kernel.Money `json:"budget,omitempty"`
	CPV      Classification `json:"cpv"`
}

type Lot struct {
	Fact
	ID     string         `json:"id"`
	Object string         `json:"object,omitempty"`
	Budget []kernel.Money `json:"budget,omitempty"`
	CPV    Classification `json:"cpv"`
}

type Award struct {
	Fact
	LotID      string         `json:"lot_id,omitempty"`
	ResultCode string         `json:"result_code"`
	AwardDate  *kernel.Date   `json:"award_date,omitempty"`
	Winner     Party          `json:"winner"`
	Amounts    []kernel.Money `json:"amounts,omitempty"`
	Contract   *Contract      `json:"contract,omitempty"`
}
