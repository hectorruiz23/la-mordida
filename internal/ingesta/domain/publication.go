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
	URL   string `json:"url"`
	Hash  string `json:"sha256,omitempty"`
	Title string `json:"title,omitempty"`
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
	Documents  []Document `json:"documents,omitempty"`
}

type Expediente struct {
	Fact
	FolderID string         `json:"folder_id"`
	Status   string         `json:"status,omitempty"`
	Object   string         `json:"object,omitempty"`
	Buyer    Party          `json:"buyer"`
	Budget   []kernel.Money `json:"budget,omitempty"`
}

type Lot struct {
	Fact
	ID     string         `json:"id"`
	Object string         `json:"object,omitempty"`
	Budget []kernel.Money `json:"budget,omitempty"`
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
