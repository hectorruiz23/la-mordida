// Package domain is the read model served over what was imported.
package domain

import "github.com/hectorruiz23/la-mordida/internal/kernel"

type Party struct {
	Scheme string `json:"scheme"`
	ID     string `json:"id"`
	Name   string `json:"name,omitempty"`
}

type Contract struct {
	ID        string `json:"id"`
	IssueDate string `json:"issue_date,omitempty"`
}

// Document is a document announced by the publication version and the fact
// it sustains. State tells an announced link from a pending download and from
// the evidence whose bytes are stored.
type Document struct {
	TargetKind string `json:"target_kind,omitempty"`
	TargetRef  string `json:"target_ref,omitempty"`
	URL        string `json:"url"`
	SHA256     string `json:"sha256,omitempty"`
	Title      string `json:"title,omitempty"`
	State      string `json:"state"`
	MIME       string `json:"mime,omitempty"`
	ObtainedAt string `json:"obtained_at,omitempty"`
}

// Publication is the source revision the current projection points at.
// UpdatedAt is the published instant; EffectiveDate is the civil date the
// source states, when it states one.
type Publication struct {
	Source        string `json:"source"`
	ExternalID    string `json:"external_id"`
	URL           string `json:"url,omitempty"`
	UpdatedAt     string `json:"updated_at"`
	EffectiveDate string `json:"effective_date,omitempty"`
}

// Award is the current award of one lot. State says whether the source is
// known, indeterminate because two same-instant revisions disagree, or
// cancelled by a documented annulment. A disputed amount is absent, never one
// of the conflicting values.
type Award struct {
	LotID      string         `json:"lot_id,omitempty"`
	ResultCode string         `json:"result_code,omitempty"`
	AwardDate  string         `json:"award_date,omitempty"`
	Winner     Party          `json:"winner"`
	Amounts    []kernel.Money `json:"amounts,omitempty"`
	Contract   *Contract      `json:"contract,omitempty"`
	State      string         `json:"state,omitempty"`
}

type Lot struct {
	ID     string         `json:"id"`
	Object string         `json:"object,omitempty"`
	Budget []kernel.Money `json:"budget,omitempty"`
	State  string         `json:"state,omitempty"`
}

// Fact is one lifecycle fact, kept with the source revision that states it.
// Deadline and delivery are separate; unknown is a value, not a zero.
type Fact struct {
	Kind           string         `json:"kind"`
	Reference      string         `json:"reference,omitempty"`
	LotID          string         `json:"lot_id,omitempty"`
	EffectiveDate  string         `json:"effective_date,omitempty"`
	DeadlineStatus string         `json:"deadline_status"`
	DeliveryStatus string         `json:"delivery_status"`
	Detail         string         `json:"detail,omitempty"`
	Amounts        []kernel.Money `json:"amounts,omitempty"`
	Publication    Publication    `json:"publication"`
}

type Expediente struct {
	Buyer       Party          `json:"buyer"`
	FolderID    string         `json:"folder_id"`
	Object      string         `json:"object,omitempty"`
	Status      string         `json:"status,omitempty"`
	State       string         `json:"state,omitempty"`
	Budget      []kernel.Money `json:"budget,omitempty"`
	Lots        []Lot          `json:"lots,omitempty"`
	Awards      []Award        `json:"awards,omitempty"`
	Facts       []Fact         `json:"facts,omitempty"`
	Documents   []Document     `json:"documents,omitempty"`
	Publication Publication    `json:"publication"`
}
