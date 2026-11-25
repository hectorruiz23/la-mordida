package placsp

import (
	"fmt"
	"strings"
	"time"

	"github.com/hectorruiz23/la-mordida/internal/ingesta/domain"
	"github.com/hectorruiz23/la-mordida/internal/kernel"
)

// buyerSchemePriority picks the stable legal identifier a buyer publishes. NIF
// is the legal identity; DIR3 is an address book; the platform identifiers are
// last resorts. The pair (scheme, id) — never the folder id alone — is what
// joins an expediente across publications.
var buyerSchemePriority = []string{"NIF", "DIR3", "ID_PLATAFORMA", "ID_OC_PLAT"}

// translate turns one ATOM entry into a neutral publication. Everything the
// source publishes for the expediente is carried over; nothing missing is
// filled with a default.
func (r *Reader) translate(entry *node) (domain.Publication, error) {
	id := entry.child(nsAtom, "id").value()
	if id == "" {
		return domain.Publication{}, fmt.Errorf("placsp: entry has no id")
	}

	updatedRaw := entry.child(nsAtom, "updated").value()
	updated, err := time.Parse(time.RFC3339, updatedRaw)
	if err != nil {
		return domain.Publication{}, fmt.Errorf("placsp: entry %s updated %q: %w", id, updatedRaw, err)
	}

	status := entry.child(nsCACExt, "ContractFolderStatus")
	if status == nil {
		return domain.Publication{}, fmt.Errorf("placsp: entry %s has no ContractFolderStatus", id)
	}

	expediente, err := translateExpediente(status)
	if err != nil {
		return domain.Publication{}, fmt.Errorf("placsp: entry %s: %w", id, err)
	}

	lots, err := translateLots(status, expediente.CPV)
	if err != nil {
		return domain.Publication{}, fmt.Errorf("placsp: entry %s: %w", id, err)
	}

	awards, err := translateAwards(status)
	if err != nil {
		return domain.Publication{}, fmt.Errorf("placsp: entry %s: %w", id, err)
	}

	notices, err := translateNotices(status)
	if err != nil {
		return domain.Publication{}, fmt.Errorf("placsp: entry %s: %w", id, err)
	}

	lots = completeAwardLots(lots, awards, expediente.FolderID)

	return domain.Publication{
		Reference: domain.Reference{
			Source: r.channel.Source(),
			ID:     id,
			URL:    entryLink(entry),
		},
		UpdatedAt:  updated,
		Expediente: expediente,
		Lots:       lots,
		Awards:     awards,
		Notices:    notices,
		Documents:  translateDocuments(status),
	}, nil
}

// completeAwardLots keeps an award whose snapshot omits the lot definition.
// The lot id is published by the award itself, so the lot exists; its object,
// budget and CPV are unknown, and the rebate ranking will exclude it with that
// reason instead of discarding the whole award.
func completeAwardLots(lots []domain.Lot, awards []domain.Award, folderID string) []domain.Lot {
	known := make(map[string]bool, len(lots))
	for _, lot := range lots {
		known[lot.ID] = true
	}
	for _, award := range awards {
		if award.LotID == "" || known[award.LotID] {
			continue
		}
		known[award.LotID] = true
		lots = append(lots, domain.Lot{
			Fact: domain.Fact{
				Scope: domain.ScopeLot,
				Identifiers: map[string]string{
					"contract_folder_id": folderID,
					"lot_id":             award.LotID,
				},
			},
			ID:  award.LotID,
			CPV: domain.Classification{Source: domain.ClassificationUnknown},
		})
	}
	return lots
}

func entryLink(entry *node) string {
	for _, link := range entry.childrenOf(nsAtom, "link") {
		if href := link.attr("href"); href != "" {
			return href
		}
	}
	return ""
}

func translateExpediente(status *node) (domain.Expediente, error) {
	folderID := status.child(nsCBC, "ContractFolderID").value()
	if folderID == "" {
		return domain.Expediente{}, fmt.Errorf("ContractFolderID is missing")
	}

	party := status.path(at(nsCACExt, "LocatedContractingParty"), at(nsCAC, "Party"))
	buyer, err := translateBuyer(party)
	if err != nil {
		return domain.Expediente{}, err
	}

	project := status.child(nsCAC, "ProcurementProject")
	budget, err := translateBudget(project.child(nsCAC, "BudgetAmount"))
	if err != nil {
		return domain.Expediente{}, fmt.Errorf("budget: %w", err)
	}

	return domain.Expediente{
		Fact: domain.Fact{
			Scope: domain.ScopeExpediente,
			Identifiers: map[string]string{
				"contract_folder_id": folderID,
				"buyer_scheme":       buyer.Scheme,
				"buyer_id":           buyer.ID,
			},
		},
		FolderID: folderID,
		Status:   status.child(nsCBCExt, "ContractFolderStatusCode").value(),
		Object:   project.child(nsCBC, "Name").value(),
		Buyer:    buyer,
		Budget:   budget,
		CPV:      classifyPublished(publishedCodes(project)),
	}, nil
}

func translateBuyer(party *node) (domain.Party, error) {
	if party == nil {
		return domain.Party{}, fmt.Errorf("LocatedContractingParty/Party is missing")
	}

	name := party.path(at(nsCAC, "PartyName"), at(nsCBC, "Name")).value()
	identifications := party.childrenOf(nsCAC, "PartyIdentification")

	byScheme := make(map[string]string, len(identifications))
	var first domain.Party
	for _, identification := range identifications {
		id := identification.child(nsCBC, "ID")
		if id == nil || id.value() == "" {
			continue
		}
		candidate := domain.Party{Scheme: id.attr("schemeName"), ID: id.value(), Name: name}
		if candidate.Scheme != "" {
			if _, seen := byScheme[candidate.Scheme]; !seen {
				byScheme[candidate.Scheme] = candidate.ID
			}
		}
		if first.ID == "" {
			first = candidate
		}
	}

	for _, scheme := range buyerSchemePriority {
		if id, ok := byScheme[scheme]; ok {
			return domain.Party{Scheme: scheme, ID: id, Name: name}, nil
		}
	}
	if first.ID != "" {
		return first, nil
	}
	if name != "" {
		// Some minor-contract profiles publish only the buyer's name. It is
		// not a legal identifier, so it is kept under the NAME scheme: the
		// gap stays visible and the procedure is still separated from other
		// buyers that share a folder id.
		return domain.Party{Scheme: "NAME", ID: name, Name: name}, nil
	}
	return domain.Party{}, fmt.Errorf("buyer has no PartyIdentification/ID or PartyName")
}

func translateLots(status *node, expediente domain.Classification) ([]domain.Lot, error) {
	lots := status.childrenOf(nsCAC, "ProcurementProjectLot")
	result := make([]domain.Lot, 0, len(lots))
	for _, lot := range lots {
		id := lot.child(nsCBC, "ID").value()
		if id == "" {
			return nil, fmt.Errorf("ProcurementProjectLot has no ID")
		}

		project := lot.child(nsCAC, "ProcurementProject")
		budget, err := translateBudget(project.child(nsCAC, "BudgetAmount"))
		if err != nil {
			return nil, fmt.Errorf("lot %s budget: %w", id, err)
		}

		classification := classifyPublished(publishedCodes(project))
		if classification.Source == domain.ClassificationUnknown {
			classification = inheritForLot(expediente)
		}

		result = append(result, domain.Lot{
			Fact: domain.Fact{
				Scope: domain.ScopeLot,
				Identifiers: map[string]string{
					"contract_folder_id": status.child(nsCBC, "ContractFolderID").value(),
					"lot_id":             id,
				},
			},
			ID:     id,
			Object: project.child(nsCBC, "Name").value(),
			Budget: budget,
			CPV:    classification,
		})
	}
	return result, nil
}

func translateAwards(status *node) ([]domain.Award, error) {
	results := status.childrenOf(nsCAC, "TenderResult")
	awards := make([]domain.Award, 0, len(results))
	folderID := status.child(nsCBC, "ContractFolderID").value()

	for index, result := range results {
		resultCode := result.child(nsCBC, "ResultCode").value()
		if resultCode == "" {
			return nil, fmt.Errorf("TenderResult %d has no ResultCode", index)
		}

		awarded := result.child(nsCAC, "AwardedTenderedProject")
		lotID := awarded.child(nsCBC, "ProcurementProjectLotID").value()

		amounts, err := translateAwardAmounts(awarded.child(nsCAC, "LegalMonetaryTotal"))
		if err != nil {
			return nil, fmt.Errorf("award %d amounts: %w", index, err)
		}

		awardDate, err := optionalDate(result.child(nsCBC, "AwardDate"))
		if err != nil {
			return nil, fmt.Errorf("award %d date: %w", index, err)
		}

		winner := translateWinner(result.child(nsCAC, "WinningParty"))
		contract, err := translateContract(result.child(nsCAC, "Contract"))
		if err != nil {
			return nil, fmt.Errorf("award %d contract: %w", index, err)
		}

		identifiers := map[string]string{
			"contract_folder_id": folderID,
			"winner_scheme":      winner.Scheme,
			"winner_id":          winner.ID,
		}
		if lotID != "" {
			identifiers["lot_id"] = lotID
		}

		awards = append(awards, domain.Award{
			Fact: domain.Fact{
				Scope:       domain.ScopeAward,
				Identifiers: identifiers,
			},
			LotID:      lotID,
			ResultCode: resultCode,
			AwardDate:  awardDate,
			Winner:     winner,
			Amounts:    amounts,
			Contract:   contract,
		})
	}
	return awards, nil
}

// translateWinner keeps the published scheme and identifier. A UTE may be
// published as OTROS with no NIF: the legal identity is then unknown, but the
// published name is still kept, never invented.
func translateWinner(party *node) domain.Party {
	if party == nil {
		return domain.Party{}
	}

	name := party.path(at(nsCAC, "PartyName"), at(nsCBC, "Name")).value()
	identifications := party.childrenOf(nsCAC, "PartyIdentification")
	for _, preferred := range []string{"NIF", "OTROS"} {
		for _, identification := range identifications {
			id := identification.child(nsCBC, "ID")
			if id != nil && id.attr("schemeName") == preferred {
				return domain.Party{Scheme: preferred, ID: id.value(), Name: name}
			}
		}
	}
	for _, identification := range identifications {
		id := identification.child(nsCBC, "ID")
		if id != nil && id.value() != "" {
			return domain.Party{Scheme: id.attr("schemeName"), ID: id.value(), Name: name}
		}
	}
	return domain.Party{Name: name}
}

func translateContract(contract *node) (*domain.Contract, error) {
	if contract == nil {
		return nil, nil
	}

	id := contract.child(nsCBC, "ID").value()
	issueDate, err := optionalDate(contract.child(nsCBC, "IssueDate"))
	if err != nil {
		return nil, err
	}
	if id == "" && issueDate == nil {
		return nil, nil
	}
	return &domain.Contract{ID: id, IssueDate: issueDate}, nil
}

// translateBudget keeps the published tax base and the tax-inclusive budget.
// EstimatedOverallContractAmount is a different magnitude and is never
// substituted for the budget. A published zero is kept as zero.
func translateBudget(container *node) ([]kernel.Money, error) {
	return translateAmounts(container,
		amountField{local: "TaxExclusiveAmount", base: kernel.TaxExclusive},
		amountField{local: "TotalAmount", base: kernel.TaxInclusive},
	)
}

// translateAwardAmounts keeps the awarded tax base and, when published, the
// payable amount. A missing PayableAmount leaves the tax-inclusive amount and
// the VAT rate unknown; it is not filled with 21 % and not filled with zero.
func translateAwardAmounts(container *node) ([]kernel.Money, error) {
	return translateAmounts(container,
		amountField{local: "TaxExclusiveAmount", base: kernel.TaxExclusive},
		amountField{local: "PayableAmount", base: kernel.TaxInclusive},
	)
}

type amountField struct {
	local string
	base  kernel.TaxBase
}

func translateAmounts(container *node, fields ...amountField) ([]kernel.Money, error) {
	if container == nil {
		return nil, nil
	}

	var amounts []kernel.Money
	for _, field := range fields {
		element := container.child(nsCBC, field.local)
		if element == nil {
			continue
		}
		value := element.value()
		if value == "" {
			return nil, fmt.Errorf("%s is empty", field.local)
		}
		amount, err := kernel.ParseMoney(value, element.attr("currencyID"), field.base)
		if err != nil {
			return nil, err
		}
		amounts = append(amounts, amount)
	}
	return amounts, nil
}

func translateNotices(status *node) ([]domain.Notice, error) {
	var notices []domain.Notice
	for _, info := range status.childrenOf(nsCACExt, "ValidNoticeInfo") {
		noticeType := info.child(nsCBCExt, "NoticeTypeCode").value()
		if noticeType == "" {
			continue
		}

		statuses := info.childrenOf(nsCACExt, "AdditionalPublicationStatus")
		if len(statuses) == 0 {
			notices = append(notices, domain.Notice{Type: noticeType})
			continue
		}

		for _, publicationStatus := range statuses {
			notice := domain.Notice{
				Type:   noticeType,
				Medium: publicationStatus.child(nsCBCExt, "PublicationMediaName").value(),
			}
			for _, reference := range publicationStatus.childrenOf(nsCACExt, "AdditionalPublicationDocumentReference") {
				issueDate, err := optionalDate(reference.child(nsCBC, "IssueDate"))
				if err != nil {
					return nil, fmt.Errorf("notice %s: %w", noticeType, err)
				}
				if issueDate != nil {
					notice.IssueDates = append(notice.IssueDates, *issueDate)
				}
			}
			notices = append(notices, notice)
		}
	}
	return notices, nil
}

func translateDocuments(status *node) []domain.Document {
	var documents []domain.Document
	for _, reference := range status.children {
		if reference.space != nsCAC || !strings.HasSuffix(reference.local, "DocumentReference") {
			continue
		}
		external := reference.path(at(nsCAC, "Attachment"), at(nsCAC, "ExternalReference"))
		url := external.child(nsCBC, "URI").value()
		if url == "" {
			continue
		}
		documents = append(documents, domain.Document{
			URL:   url,
			Hash:  external.child(nsCBC, "DocumentHash").value(),
			Title: reference.child(nsCBC, "ID").value(),
		})
	}
	return documents
}

func publishedCodes(project *node) []string {
	if project == nil {
		return nil
	}
	var codes []string
	for _, classification := range project.childrenOf(nsCAC, "RequiredCommodityClassification") {
		code := classification.child(nsCBC, "ItemClassificationCode").value()
		if code != "" {
			codes = append(codes, code)
		}
	}
	return codes
}

func optionalDate(element *node) (*kernel.Date, error) {
	raw := element.value()
	if raw == "" {
		return nil, nil
	}
	date, err := kernel.ParseDate(raw)
	if err != nil {
		return nil, err
	}
	return &date, nil
}
