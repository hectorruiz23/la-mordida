package placsp

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/hectorruiz23/la-mordida/internal/ingesta/app"
	"github.com/hectorruiz23/la-mordida/internal/ingesta/domain"
	"github.com/hectorruiz23/la-mordida/internal/kernel"
)

func amountCents(t *testing.T, amounts []kernel.Money, base kernel.TaxBase) (int64, bool) {
	t.Helper()
	for _, amount := range amounts {
		if amount.TaxBase == base {
			return amount.Cents, true
		}
	}
	return 0, false
}

func requireAmount(t *testing.T, amounts []kernel.Money, base kernel.TaxBase, want int64) {
	t.Helper()
	got, ok := amountCents(t, amounts, base)
	if !ok {
		t.Fatalf("%s amount is missing from %+v", base, amounts)
	}
	if got != want {
		t.Errorf("%s amount = %d cents, want %d", base, got, want)
	}
}

func noticeDates(t *testing.T, publication domain.Publication, noticeType string) []string {
	t.Helper()
	var dates []string
	for _, notice := range publication.Notices {
		if notice.Type != noticeType {
			continue
		}
		for _, date := range notice.IssueDates {
			dates = append(dates, date.String())
		}
	}
	return dates
}

func TestOfficialLotsSample(t *testing.T) {
	publication := readFixture(t, Profiles, "official/01-lotes-1787-2025/publication.atom")

	if publication.Reference.Source != "placsp:sindicacion_643" {
		t.Errorf("source = %q", publication.Reference.Source)
	}
	if publication.Reference.ID != "https://contrataciondelestado.es/sindicacion/licitacionesPerfilContratante/17680481" {
		t.Errorf("id = %q", publication.Reference.ID)
	}
	if publication.Reference.URL == "" {
		t.Error("deeplink is empty")
	}
	if publication.Expediente.FolderID != "1787/2025" || publication.Expediente.Status != "ADJ" {
		t.Errorf("expediente = %s / %s", publication.Expediente.FolderID, publication.Expediente.Status)
	}
	if publication.Expediente.Buyer.Scheme != "NIF" || publication.Expediente.Buyer.ID != "P3704600J" {
		t.Errorf("buyer = %+v", publication.Expediente.Buyer)
	}
	requireAmount(t, publication.Expediente.Budget, kernel.TaxExclusive, 244722)
	requireAmount(t, publication.Expediente.Budget, kernel.TaxInclusive, 296114)

	if len(publication.Lots) != 2 {
		t.Fatalf("lots = %d, want 2", len(publication.Lots))
	}
	if publication.Lots[0].ID != "1" || publication.Lots[1].ID != "2" {
		t.Errorf("lot ids = %q, %q", publication.Lots[0].ID, publication.Lots[1].ID)
	}
	for _, lot := range publication.Lots {
		if lot.CPV.Source != domain.ClassificationPublished {
			t.Errorf("lot %s CPV source = %q, want published", lot.ID, lot.CPV.Source)
		}
		for _, code := range lot.CPV.Codes {
			if code.Included {
				t.Errorf("lot %s code %s is included, want the 337/398 family excluded", lot.ID, code.Code)
			}
		}
	}

	if len(publication.Awards) != 2 {
		t.Fatalf("awards = %d, want 2", len(publication.Awards))
	}
	if publication.Awards[0].LotID != "1" || publication.Awards[1].LotID != "2" {
		t.Errorf("award lot ids = %q, %q", publication.Awards[0].LotID, publication.Awards[1].LotID)
	}
	if publication.Awards[0].AwardDate == nil || publication.Awards[0].AwardDate.String() != "2025-10-10" {
		t.Errorf("award date = %v", publication.Awards[0].AwardDate)
	}
	requireAmount(t, publication.Awards[0].Amounts, kernel.TaxExclusive, 78411)
	requireAmount(t, publication.Awards[0].Amounts, kernel.TaxInclusive, 94877)
	requireAmount(t, publication.Awards[1].Amounts, kernel.TaxExclusive, 59409)
	requireAmount(t, publication.Awards[1].Amounts, kernel.TaxInclusive, 71884)

	if len(publication.Notices) != 3 {
		t.Fatalf("notices = %d, want 3", len(publication.Notices))
	}
	if dates := noticeDates(t, publication, "DOC_CAN_ADJ"); len(dates) != 2 || dates[0] != "2025-10-20" || dates[1] != "2025-10-20" {
		t.Errorf("DOC_CAN_ADJ dates = %v, want both published duplicates", dates)
	}
	if len(publication.Documents) == 0 {
		t.Error("documents are empty")
	}
}

func TestOfficialCorrectionKeepsBothVersions(t *testing.T) {
	members := map[string]string{
		"before-2025-10-01.atom": mustRead(t, "official/02-correccion-37988-2025/before-2025-10-01.atom"),
		"after-2025-10-22.atom":  mustRead(t, "official/02-correccion-37988-2025/after-2025-10-22.atom"),
	}
	publications, err := readArchiveMembers(t, Profiles, app.Window{}, members)
	if err != nil {
		t.Fatalf("read correction: %v", err)
	}
	if len(publications) != 2 {
		t.Fatalf("publications = %d, want both snapshots", len(publications))
	}
	if publications[0].Reference.ID != publications[1].Reference.ID {
		t.Fatalf("snapshot ids differ: %q / %q", publications[0].Reference.ID, publications[1].Reference.ID)
	}
	if publications[0].UpdatedAt.Equal(publications[1].UpdatedAt) {
		t.Fatalf("snapshots share an updated instant")
	}
	if publications[0].UpdatedAt.After(publications[1].UpdatedAt) {
		publications[0], publications[1] = publications[1], publications[0]
	}

	before := noticeDates(t, publications[0], "DOC_CN")
	after := noticeDates(t, publications[1], "DOC_CN")
	if len(before) != 1 || before[0] != "2025-10-01" {
		t.Errorf("before DOC_CN dates = %v", before)
	}
	if len(after) != 2 || after[0] != "2025-10-01" || after[1] != "2025-10-17" {
		t.Errorf("after DOC_CN dates = %v, want the second IssueDate kept", after)
	}
	for _, code := range publications[1].Expediente.CPV.Codes {
		if code.Included {
			t.Errorf("code %s is included, want 800/853 excluded", code.Code)
		}
	}
}

func TestOfficialMinorWithoutCPV(t *testing.T) {
	publication := readFixture(t, Minors, "official/03-menor-20250145/publication.atom")

	if publication.Expediente.Status != "RES" {
		t.Errorf("status = %q", publication.Expediente.Status)
	}
	if publication.Expediente.CPV.Source != domain.ClassificationUnknown || publication.Expediente.CPV.Reason != "cpv_missing" {
		t.Errorf("minor CPV = %+v, want unknown, not an invented family", publication.Expediente.CPV)
	}
	if len(publication.Lots) != 0 {
		t.Errorf("lots = %d, want none", len(publication.Lots))
	}
	if len(publication.Awards) != 1 || publication.Awards[0].LotID != "" {
		t.Fatalf("awards = %+v, want one global award", publication.Awards)
	}
	requireAmount(t, publication.Expediente.Budget, kernel.TaxExclusive, 1487000)
	requireAmount(t, publication.Awards[0].Amounts, kernel.TaxExclusive, 1469162)
	requireAmount(t, publication.Awards[0].Amounts, kernel.TaxInclusive, 1777686)
}

func TestOfficialBeforeTheCutKeepsTheInitialNotice(t *testing.T) {
	publication := readFixture(t, Profiles, "official/04-precorte-5502/publication.atom")

	dates := noticeDates(t, publication, "DOC_CN")
	want := []string{"2019-04-10", "2019-04-26", "2019-07-18"}
	if len(dates) != len(want) {
		t.Fatalf("DOC_CN dates = %v, want %v", dates, want)
	}
	for index := range want {
		if dates[index] != want[index] {
			t.Errorf("DOC_CN[%d] = %s, want %s", index, dates[index], want[index])
		}
	}

	if publication.Expediente.CPV.Source != domain.ClassificationPublished || len(publication.Expediente.CPV.Codes) != 1 {
		t.Fatalf("CPV = %+v", publication.Expediente.CPV)
	}
	code := publication.Expediente.CPV.Codes[0]
	if code.Code != "30237475" || code.Family != "302" || !code.Included {
		t.Errorf("CPV = %+v, want 30237475 selected into family 302", code)
	}

	if len(publication.Awards) != 1 || publication.Awards[0].AwardDate == nil || publication.Awards[0].AwardDate.String() != "2020-06-26" {
		t.Fatalf("award date = %+v, want the 2020-06-26 award", publication.Awards)
	}
	contract := publication.Awards[0].Contract
	if contract == nil || contract.ID != "181746" || contract.IssueDate == nil || contract.IssueDate.String() != "2020-10-16" {
		t.Errorf("contract = %+v", contract)
	}
}

func TestOfficialNoComparableBudgetKeepsThePublishedZero(t *testing.T) {
	publication := readFixture(t, Profiles, "official/05-sin-presupuesto-25-07-25C/publication.atom")

	requireAmount(t, publication.Expediente.Budget, kernel.TaxExclusive, 0)
	requireAmount(t, publication.Expediente.Budget, kernel.TaxInclusive, 0)
	if len(publication.Awards) != 1 {
		t.Fatalf("awards = %d", len(publication.Awards))
	}
	requireAmount(t, publication.Awards[0].Amounts, kernel.TaxExclusive, 6900000)
	requireAmount(t, publication.Awards[0].Amounts, kernel.TaxInclusive, 7383000)
	if len(publication.Expediente.CPV.Codes) != 1 || publication.Expediente.CPV.Codes[0].Included {
		t.Errorf("CPV = %+v, want 55511000 excluded", publication.Expediente.CPV)
	}
}

func TestOfficialAggregatedSample(t *testing.T) {
	publication := readFixture(t, Aggregated, "official/06-agregada-b045-2025-00012/publication.atom")

	if publication.Expediente.Buyer.Scheme != "ID_OC_PLAT" || publication.Expediente.Buyer.ID != "18367_18368_18369" {
		t.Errorf("buyer = %+v, want the platform identifier", publication.Expediente.Buyer)
	}
	if len(publication.Lots) != 2 {
		t.Fatalf("lots = %d", len(publication.Lots))
	}
	for _, lot := range publication.Lots {
		if len(lot.Budget) != 0 {
			t.Errorf("lot %s has a budget %+v; the project budget must not be prorated", lot.ID, lot.Budget)
		}
		if lot.CPV.Source != domain.ClassificationInherited || len(lot.CPV.Codes) != 1 || lot.CPV.Codes[0].Family != "72" {
			t.Errorf("lot %s CPV = %+v, want inherited family 72", lot.ID, lot.CPV)
		}
	}
	if len(publication.Awards) != 2 {
		t.Fatalf("awards = %d", len(publication.Awards))
	}
	for _, award := range publication.Awards {
		if award.AwardDate != nil {
			t.Errorf("award date = %v, want unknown", award.AwardDate)
		}
		if _, ok := amountCents(t, award.Amounts, kernel.TaxInclusive); ok {
			t.Errorf("award has a tax-inclusive amount; PayableAmount is absent")
		}
		requireAmount(t, award.Amounts, kernel.TaxExclusive, 0)
		if award.Contract == nil || award.Contract.IssueDate == nil || award.Contract.IssueDate.String() != "2025-09-05" {
			t.Errorf("contract = %+v, want the contract date kept apart from the award date", award.Contract)
		}
	}
}

func TestSyntheticOptionalElements(t *testing.T) {
	publications, err := readArchiveMembers(t, Profiles, app.Window{}, map[string]string{
		"02.atom": mustRead(t, "synthetic/02-award-without-payable/publication.atom"),
		"04.atom": mustRead(t, "synthetic/04-unknown-currency/publication.atom"),
	})
	if err != nil {
		t.Fatalf("read synthetic fixtures: %v", err)
	}
	if len(publications) != 2 {
		t.Fatalf("publications = %d", len(publications))
	}

	withoutPayable := publications[0]
	if withoutPayable.Expediente.FolderID != "SYN-NO-PAYABLE" {
		t.Fatalf("unexpected first fixture %s", withoutPayable.Expediente.FolderID)
	}
	if len(withoutPayable.Awards) != 1 {
		t.Fatalf("awards = %d", len(withoutPayable.Awards))
	}
	requireAmount(t, withoutPayable.Awards[0].Amounts, kernel.TaxExclusive, 900000)
	if _, ok := amountCents(t, withoutPayable.Awards[0].Amounts, kernel.TaxInclusive); ok {
		t.Error("a tax-inclusive amount was invented without PayableAmount")
	}

	withoutCurrency := publications[1]
	if withoutCurrency.Expediente.FolderID != "SYN-NO-CURRENCY" {
		t.Fatalf("unexpected second fixture %s", withoutCurrency.Expediente.FolderID)
	}
	for _, amount := range withoutCurrency.Expediente.Budget {
		if amount.Currency != "" {
			t.Errorf("budget currency = %q, want unknown", amount.Currency)
		}
	}
	for _, amount := range withoutCurrency.Awards[0].Amounts {
		if amount.Currency != "" {
			t.Errorf("award currency = %q, want unknown", amount.Currency)
		}
	}
}

func TestMixedLotsDoNotAttributeForeignSpend(t *testing.T) {
	extra := `
    <cac:ProcurementProject>
      <cbc:Name>Contrato mixto</cbc:Name>
      <cac:BudgetAmount><cbc:TaxExclusiveAmount currencyID="EUR">1000</cbc:TaxExclusiveAmount></cac:BudgetAmount>
      <cac:RequiredCommodityClassification><cbc:ItemClassificationCode>72200000</cbc:ItemClassificationCode></cac:RequiredCommodityClassification>
      <cac:RequiredCommodityClassification><cbc:ItemClassificationCode>33700000</cbc:ItemClassificationCode></cac:RequiredCommodityClassification>
    </cac:ProcurementProject>
    <cac:ProcurementProjectLot>
      <cbc:ID>1</cbc:ID>
      <cac:ProcurementProject>
        <cbc:Name>Lote de servicios</cbc:Name>
        <cac:BudgetAmount><cbc:TaxExclusiveAmount currencyID="EUR">600</cbc:TaxExclusiveAmount></cac:BudgetAmount>
        <cac:RequiredCommodityClassification><cbc:ItemClassificationCode>72200000</cbc:ItemClassificationCode></cac:RequiredCommodityClassification>
      </cac:ProcurementProject>
    </cac:ProcurementProjectLot>
    <cac:ProcurementProjectLot>
      <cbc:ID>2</cbc:ID>
      <cac:ProcurementProject>
        <cbc:Name>Lote de mobiliario</cbc:Name>
        <cac:BudgetAmount><cbc:TaxExclusiveAmount currencyID="EUR">400</cbc:TaxExclusiveAmount></cac:BudgetAmount>
        <cac:RequiredCommodityClassification><cbc:ItemClassificationCode>33700000</cbc:ItemClassificationCode></cac:RequiredCommodityClassification>
      </cac:ProcurementProject>
    </cac:ProcurementProjectLot>
    <cac:ProcurementProjectLot>
      <cbc:ID>3</cbc:ID>
      <cac:ProcurementProject><cbc:Name>Lote sin clasificar</cbc:Name></cac:ProcurementProject>
    </cac:ProcurementProjectLot>`

	page := atomPage(atomEntry("urn:test:mixed", "2025-10-20T10:00:00Z", "MIXED", extra))
	publications, err := readArchiveMembers(t, Profiles, app.Window{}, map[string]string{"mixed.atom": page})
	if err != nil {
		t.Fatalf("read mixed lots: %v", err)
	}
	if len(publications) != 1 {
		t.Fatalf("publications = %d", len(publications))
	}
	publication := publications[0]
	if len(publication.Lots) != 3 {
		t.Fatalf("lots = %d", len(publication.Lots))
	}

	first := publication.Lots[0]
	if first.CPV.Source != domain.ClassificationPublished || len(first.CPV.Codes) != 1 || !first.CPV.Codes[0].Included || first.CPV.Codes[0].Family != "72" {
		t.Errorf("lot 1 CPV = %+v, want its own 72 code", first.CPV)
	}
	requireAmount(t, first.Budget, kernel.TaxExclusive, 60000)

	second := publication.Lots[1]
	if second.CPV.Source != domain.ClassificationPublished || len(second.CPV.Codes) != 1 || second.CPV.Codes[0].Included {
		t.Errorf("lot 2 CPV = %+v, want its own excluded code", second.CPV)
	}
	requireAmount(t, second.Budget, kernel.TaxExclusive, 40000)

	third := publication.Lots[2]
	if third.CPV.Source != domain.ClassificationUnknown || !third.CPV.Ambiguous || third.CPV.Reason != "cpv_ambiguous" {
		t.Errorf("lot 3 CPV = %+v, want unknown because the project mixes families", third.CPV)
	}
	if len(third.CPV.Codes) != 0 {
		t.Errorf("lot 3 codes = %+v, want no inferred family", third.CPV.Codes)
	}
	if len(third.Budget) != 0 {
		t.Errorf("lot 3 budget = %+v; the project budget must not be copied", third.Budget)
	}
}

func TestWindowFiltersOnEntryUpdated(t *testing.T) {
	publication := readFixture(t, Profiles, "official/01-lotes-1787-2025/publication.atom")
	updated := publication.UpdatedAt

	before := atomPage(atomEntry("urn:test:before", updated.Add(-time.Hour).Format(time.RFC3339), "BEFORE", ""))
	inside := atomPage(atomEntry("urn:test:inside", updated.Format(time.RFC3339), "INSIDE", ""))
	after := atomPage(atomEntry("urn:test:after", updated.Add(time.Hour).Format(time.RFC3339), "AFTER", ""))
	members := map[string]string{
		"a.atom": before,
		"b.atom": inside,
		"c.atom": after,
	}

	got, err := readArchiveMembers(t, Profiles, app.Window{From: updated.Add(-time.Minute), To: updated.Add(time.Minute)}, members)
	if err != nil {
		t.Fatalf("inside window: %v", err)
	}
	if len(got) != 1 || got[0].Reference.ID != "urn:test:inside" {
		t.Fatalf("inside window yielded %+v, want only the middle member", got)
	}

	outside, err := readArchiveMembers(t, Profiles, app.Window{From: updated.Add(time.Hour)}, members)
	if err != nil {
		t.Fatalf("outside window: %v", err)
	}
	if len(outside) != 0 {
		t.Fatalf("outside window yielded %d publications", len(outside))
	}
}

func mustRead(t *testing.T, relativePath string) string {
	t.Helper()
	contents, err := os.ReadFile(filepath.Join(testdataRoot, relativePath))
	if err != nil {
		t.Fatalf("read fixture %s: %v", relativePath, err)
	}
	return string(contents)
}
