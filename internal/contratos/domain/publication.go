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

type Expediente struct {
	Buyer    Party
	FolderID string
	Object   string
	Status   string
	Budget   []kernel.Money
}

type Lot struct {
	ID     string
	Object string
	Budget []kernel.Money
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

// Publication is one stored version of a source publication. The same source
// reference with a newer UpdatedAt is another version, kept beside the first.
type Publication struct {
	Source     string
	ExternalID string
	URL        string
	UpdatedAt  time.Time
	BodyHash   string
	Expediente Expediente
	Lots       []Lot
	Awards     []Award
	Documents  []Document
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
