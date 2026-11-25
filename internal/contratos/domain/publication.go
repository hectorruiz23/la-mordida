// Package domain owns the expediente, its optional lots, its awards and the
// publication versions that sustain them. It imports only the kernel: no
// source format and no database reaches this package.
package domain

import (
	"fmt"
	"time"

	"github.com/hectorruiz23/la-mordida/internal/kernel"
)

type Party struct {
	Scheme string
	ID     string
	Name   string
}

type Contract struct {
	ID        string
	IssueDate *kernel.Date
}

// Document is one document a publication announces. Kind names the fact it
// sustains (publication, expediente, lot or award) and Ref its identifier
// inside the publication; an empty Kind means the publication itself.
type Document struct {
	Kind  string
	Ref   string
	URL   string
	Hash  string
	Title string
}

// CPV is one published classification code with the family it was selected
// into, if any. Included and Reason say whether and why it selected the
// publication, so a code outside the tracked families is kept, not dropped.
type CPV struct {
	Code     string
	Family   string
	Included bool
	Reason   string
}

// Classification is the CPV selection of an expediente or a lot. Source is
// published, inherited or unknown; Ambiguous marks an inference the source
// does not support and that must not be added up. Included is the decision of
// the scope: true when at least one of its codes belongs to a selected family.
type Classification struct {
	Codes     []CPV
	Source    string
	Ambiguous bool
	Reason    string
	Included  bool
}

const (
	ClassificationPublished = "published"
	ClassificationInherited = "inherited"
	ClassificationUnknown   = "unknown"
)

// Notice is one legal event published in a snapshot. The source does not label
// corrections, so every published issue date is kept.
type Notice struct {
	Type       string
	Medium     string
	IssueDates []kernel.Date
}

type Expediente struct {
	Buyer    Party
	FolderID string
	Object   string
	Status   string
	Budget   []kernel.Money
	CPV      Classification
}

type Lot struct {
	ID     string
	Object string
	Budget []kernel.Money
	CPV    Classification
}

// Award is one TenderResult. LotID is empty for a global award, which is a
// single award, not an unknown lot. AwardDate is a civil date and may be
// unknown; the source does not always publish it.
type Award struct {
	LotID      string
	ResultCode string
	AwardDate  *kernel.Date
	Winner     Party
	Amounts    []kernel.Money
	Contract   *Contract
}

// FactKind names a lifecycle fact. An award is kept in Award because it has
// its own typed result; the other facts are kept with the reference the source
// gives them. A numeric difference is never labelled a rectification by the
// importer: the kind is evidence, not an inference.
type FactKind string

const (
	FactRectification      FactKind = "rectification"
	FactExtensionPlanned   FactKind = "extension_planned"
	FactExtensionExercised FactKind = "extension_exercised"
	FactAmplification      FactKind = "amplification"
	FactScopeChange        FactKind = "scope_change"
	FactReception          FactKind = "reception"
	FactFinalPayment       FactKind = "final_payment"
	FactAnnulment          FactKind = "annulment"
)

func (k FactKind) Valid() bool {
	switch k {
	case FactRectification, FactExtensionPlanned, FactExtensionExercised,
		FactAmplification, FactScopeChange, FactReception, FactFinalPayment, FactAnnulment:
		return true
	}
	return false
}

// Compliance says whether a deadline was met. Unknown is the zero value and
// the only value when no evidence exists; it is never derived from the other
// status.
type Compliance string

const (
	ComplianceUnknown Compliance = "unknown"
	ComplianceOnTime  Compliance = "on_time"
	ComplianceLate    Compliance = "late"
)

func (c Compliance) Valid() bool {
	return c == "" || c == ComplianceUnknown || c == ComplianceOnTime || c == ComplianceLate
}

// Delivery says whether the object was received. It is kept apart from the
// deadline: a contract may be delivered late, on time, or with one of the two
// facts unknown.
type Delivery string

const (
	DeliveryUnknown      Delivery = "unknown"
	DeliveryDelivered    Delivery = "delivered"
	DeliveryNotDelivered Delivery = "not_delivered"
)

func (d Delivery) Valid() bool {
	return d == "" || d == DeliveryUnknown || d == DeliveryDelivered || d == DeliveryNotDelivered
}

// Fact is one lifecycle fact published by the source. Ref is the source's
// identifier inside the publication; LotID is empty when the fact concerns the
// whole procedure. EffectiveDate is a civil date and may be unknown.
type Fact struct {
	Kind           FactKind
	Ref            string
	LotID          string
	EffectiveDate  *kernel.Date
	Amounts        []kernel.Money
	DeadlineStatus Compliance
	DeliveryStatus Delivery
	Detail         string
	EvidenceHash   string
}

// Publication is one stored version of a source publication. The same source
// reference with a newer UpdatedAt is another version, kept beside the first.
// UpdatedAt is the source's published instant, EffectiveDate is the civil date
// the revision applies from when the source states one, and ObservedAt is when
// the importer saw it (a zero value means "when stored").
type Publication struct {
	Source        string
	ExternalID    string
	URL           string
	UpdatedAt     time.Time
	EffectiveDate *kernel.Date
	ObservedAt    time.Time
	BodyHash      string
	Expediente    Expediente
	Lots          []Lot
	Awards        []Award
	Notices       []Notice
	Facts         []Fact
	Documents     []Document
}

func (p Publication) Validate() error {
	if p.Source == "" {
		return fmt.Errorf("publication: source is required")
	}
	if p.ExternalID == "" {
		return fmt.Errorf("publication %s: external id is required", p.Source)
	}
	if p.UpdatedAt.IsZero() {
		return fmt.Errorf("publication %s/%s: updated instant is required", p.Source, p.ExternalID)
	}
	if p.BodyHash == "" {
		return fmt.Errorf("publication %s/%s: body hash is required", p.Source, p.ExternalID)
	}
	if p.Expediente.FolderID == "" {
		return fmt.Errorf("publication %s/%s: contract folder id is required", p.Source, p.ExternalID)
	}
	if p.Expediente.Buyer.Scheme == "" || p.Expediente.Buyer.ID == "" {
		return fmt.Errorf("publication %s/%s: buyer scheme and id are required", p.Source, p.ExternalID)
	}
	if err := validateMoney(p.Expediente.Budget); err != nil {
		return fmt.Errorf("publication %s/%s: expediente budget: %w", p.Source, p.ExternalID, err)
	}

	knownLots := make(map[string]bool, len(p.Lots))
	for _, lot := range p.Lots {
		if lot.ID == "" {
			return fmt.Errorf("publication %s/%s: lot id is required", p.Source, p.ExternalID)
		}
		if knownLots[lot.ID] {
			return fmt.Errorf("publication %s/%s: duplicate lot %s", p.Source, p.ExternalID, lot.ID)
		}
		knownLots[lot.ID] = true
		if err := validateMoney(lot.Budget); err != nil {
			return fmt.Errorf("publication %s/%s: lot %s budget: %w", p.Source, p.ExternalID, lot.ID, err)
		}
	}

	for index, document := range p.Documents {
		if document.URL == "" {
			return fmt.Errorf("publication %s/%s: document %d has no url", p.Source, p.ExternalID, index)
		}
		switch document.Kind {
		case "", "publication", "expediente":
		case "lot", "award":
			if document.Ref == "" {
				return fmt.Errorf("publication %s/%s: document %d has a %s target without a reference", p.Source, p.ExternalID, index, document.Kind)
			}
		default:
			return fmt.Errorf("publication %s/%s: document %d has unknown target %q", p.Source, p.ExternalID, index, document.Kind)
		}
	}

	for index, award := range p.Awards {
		if award.ResultCode == "" {
			return fmt.Errorf("publication %s/%s: award %d has no result code", p.Source, p.ExternalID, index)
		}
		if award.LotID != "" && !knownLots[award.LotID] {
			return fmt.Errorf("publication %s/%s: award %d cites unknown lot %s", p.Source, p.ExternalID, index, award.LotID)
		}
		if err := validateMoney(award.Amounts); err != nil {
			return fmt.Errorf("publication %s/%s: award %d amounts: %w", p.Source, p.ExternalID, index, err)
		}
	}

	seenFacts := make(map[string]bool, len(p.Facts))
	for index, fact := range p.Facts {
		if !fact.Kind.Valid() {
			return fmt.Errorf("publication %s/%s: fact %d has unknown kind %q", p.Source, p.ExternalID, index, fact.Kind)
		}
		if fact.LotID != "" && !knownLots[fact.LotID] {
			return fmt.Errorf("publication %s/%s: fact %d cites unknown lot %s", p.Source, p.ExternalID, index, fact.LotID)
		}
		if !fact.DeadlineStatus.Valid() {
			return fmt.Errorf("publication %s/%s: fact %d has unknown deadline status %q", p.Source, p.ExternalID, index, fact.DeadlineStatus)
		}
		if !fact.DeliveryStatus.Valid() {
			return fmt.Errorf("publication %s/%s: fact %d has unknown delivery status %q", p.Source, p.ExternalID, index, fact.DeliveryStatus)
		}
		key := string(fact.Kind) + "\x00" + fact.Ref + "\x00" + fact.LotID
		if seenFacts[key] {
			return fmt.Errorf("publication %s/%s: duplicate fact %s %s", p.Source, p.ExternalID, fact.Kind, fact.Ref)
		}
		seenFacts[key] = true
		if err := validateMoney(fact.Amounts); err != nil {
			return fmt.Errorf("publication %s/%s: fact %d amounts: %w", p.Source, p.ExternalID, index, err)
		}
	}
	return nil
}

func validateMoney(amounts []kernel.Money) error {
	seen := make(map[kernel.TaxBase]bool, len(amounts))
	for _, amount := range amounts {
		if !amount.TaxBase.Valid() {
			return fmt.Errorf("invalid tax base %q", amount.TaxBase)
		}
		if seen[amount.TaxBase] {
			return fmt.Errorf("duplicate %s amount", amount.TaxBase)
		}
		seen[amount.TaxBase] = true
	}
	return nil
}

// Amount returns the published amount for a tax base.
func Amount(amounts []kernel.Money, base kernel.TaxBase) (kernel.Money, bool) {
	for _, amount := range amounts {
		if amount.TaxBase == base {
			return amount, true
		}
	}
	return kernel.Money{}, false
}
