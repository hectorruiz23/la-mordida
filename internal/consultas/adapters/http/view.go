package http

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/hectorruiz23/la-mordida/internal/consultas/domain"
	"github.com/hectorruiz23/la-mordida/internal/kernel"
)

type moneyView struct {
	Amount   string
	Currency string
	Base     string
	BaseKey  string
	Raw      string
}

func viewMoney(m kernel.Money) *moneyView {
	return &moneyView{
		Amount:   decimal(m.Cents),
		Currency: m.Currency,
		Base:     taxBaseLabel(string(m.TaxBase)),
		BaseKey:  string(m.TaxBase),
		Raw:      m.String(),
	}
}

// moneyAt returns the amount published for one tax base, nil when the source
// does not publish it. A missing amount is unknown, never zero.
func moneyAt(amounts []kernel.Money, base string) *moneyView {
	for _, amount := range amounts {
		if string(amount.TaxBase) == base {
			return viewMoney(amount)
		}
	}
	return nil
}

// decimal renders minor units with Spanish thousands and decimal separators.
func decimal(cents int64) string {
	sign := ""
	if cents < 0 {
		sign = "-"
		cents = -cents
	}
	digits := strconv.FormatInt(cents/100, 10)
	var grouped strings.Builder
	for i, digit := range digits {
		if i > 0 && (len(digits)-i)%3 == 0 {
			grouped.WriteByte('.')
		}
		grouped.WriteRune(digit)
	}
	return fmt.Sprintf("%s%s,%02d", sign, grouped.String(), cents%100)
}

func moneyText(m kernel.Money) string {
	value := decimal(m.Cents)
	switch m.Currency {
	case "":
		return value + " (moneda desconocida)"
	case "EUR":
		return value + " €"
	default:
		return value + " " + m.Currency
	}
}

func taxBaseLabel(base string) string {
	switch base {
	case string(kernel.TaxExclusive):
		return "IVA excluido"
	case string(kernel.TaxInclusive):
		return "IVA incluido"
	default:
		return "Base desconocida"
	}
}

func stateLabel(state string) string {
	switch state {
	case "announced":
		return "Anunciado"
	case "pending":
		return "Pendiente"
	case "evidence":
		return "Custodiado"
	default:
		return "Desconocido"
	}
}

func factLabel(kind string) string {
	switch kind {
	case "rectification":
		return "Rectificación"
	case "extension_planned":
		return "Prórroga prevista"
	case "extension_exercised":
		return "Prórroga ejecutada"
	case "amplification":
		return "Ampliación"
	case "scope_change":
		return "Cambio de alcance"
	case "reception":
		return "Recepción"
	case "final_payment":
		return "Pago final"
	case "annulment":
		return "Anulación"
	default:
		return "Hecho desconocido"
	}
}

func deadlineLabel(status string) string {
	switch status {
	case "on_time":
		return "En plazo"
	case "late":
		return "Fuera de plazo"
	case "", "unknown":
		return "Desconocido"
	default:
		return status
	}
}

func deliveryLabel(status string) string {
	switch status {
	case "delivered":
		return "Entregado"
	case "not_delivered":
		return "No entregado"
	case "", "unknown":
		return "Desconocido"
	default:
		return status
	}
}

func reasonLabel(reason string) string {
	switch reason {
	case domain.ReasonBudgetMissing:
		return "Sin presupuesto"
	case domain.ReasonNoLotBudget:
		return "El lote no publica presupuesto"
	case domain.ReasonBudgetNotPositive:
		return "Presupuesto no positivo"
	case domain.ReasonAmountMissing:
		return "Sin importe adjudicado"
	case domain.ReasonAmountNotPositive:
		return "Importe adjudicado no positivo"
	case domain.ReasonCurrencyUnknown:
		return "Moneda desconocida"
	case domain.ReasonCurrencyMismatch:
		return "Monedas distintas"
	case domain.ReasonTaxBaseMismatch:
		return "Bases fiscales distintas"
	default:
		return "Motivo desconocido"
	}
}

type coverageView struct {
	Sources []coverageSourceView
}

type coverageSourceView struct {
	Source string
	Spans  []coverageSpanView
}

type coverageSpanView struct {
	Kind       string
	Period     string
	State      string
	StateClass string
	Seen       int
	Accepted   int
	Excluded   int
	Errors     int
	Exclusions []coverageExclusionView
	Pages      int
	LastPoint  string
	Detail     string
}

type coverageExclusionView struct {
	Reason string
	Label  string
	Units  int
}

func viewCoverage(coverage domain.Coverage) coverageView {
	view := coverageView{Sources: make([]coverageSourceView, 0, len(coverage.Sources))}
	for _, source := range coverage.Sources {
		item := coverageSourceView{Source: source.Source, Spans: make([]coverageSpanView, 0, len(source.Spans))}
		for _, span := range source.Spans {
			row := coverageSpanView{
				Kind:       loadKindLabel(span.Kind),
				Period:     periodLabel(span.PeriodFrom, span.PeriodUntil, span.UntilExclusive),
				State:      spanStateLabel(span.State),
				StateClass: span.State,
				Seen:       span.Seen,
				Accepted:   span.Publications,
				Excluded:   span.Excluded,
				Errors:     span.Errors,
				Pages:      span.Pages,
				LastPoint:  firstNonEmpty(span.LastCompleteAt, span.LastPage),
				Detail:     span.Detail,
			}
			for _, exclusion := range span.Exclusions {
				row.Exclusions = append(row.Exclusions, coverageExclusionView{
					Reason: exclusion.Reason,
					Label:  exclusionReasonLabel(exclusion.Reason),
					Units:  exclusion.Units,
				})
			}
			item.Spans = append(item.Spans, row)
		}
		view.Sources = append(view.Sources, item)
	}
	return view
}

func exclusionReasonLabel(reason string) string {
	switch reason {
	case "family_not_selected":
		return "Familia no seleccionada"
	case "cpv_missing":
		return "Sin CPV"
	case "cpv_ambiguous":
		return "CPV ambiguo"
	default:
		return "Motivo desconocido"
	}
}

func loadKindLabel(kind string) string {
	switch kind {
	case "historical":
		return "Histórico"
	case "incremental":
		return "Actualización"
	default:
		return "Carga desconocida"
	}
}

func spanStateLabel(state string) string {
	switch state {
	case "complete":
		return "Completo"
	case "partial":
		return "Parcial"
	case "gap":
		return "Hueco"
	default:
		return "Desconocido"
	}
}

func periodLabel(from, until string, exclusive bool) string {
	if from == "" && until == "" {
		return "Inicio y fin abiertos"
	}
	start := firstNonEmpty(from, "inicio abierto")
	end := firstNonEmpty(until, "fin abierto")
	if exclusive && until != "" {
		end += " (sin incluir)"
	}
	return start + " – " + end
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

type filterView struct {
	BuyerScheme string
	BuyerID     string
	From        string
	To          string
	Limit       int
}

type adjudicatariosView struct {
	Title       string
	Coverage    coverageView
	Filters     filterView
	Ranking     domain.Ranking
	EntityKey   string
	Awards      *awardsView
	PrevURL     string
	NextURL     string
	EntityURLs  map[string]string
	Empty       bool
	FilterError string
}

type awardsView struct {
	Entity  domain.Entity
	Total   int
	Offset  int
	Limit   int
	Rows    []awardRowView
	PrevURL string
	NextURL string
}

type awardRowView struct {
	Line      domain.AwardLine
	DetailURL string
	Date      string
	Docs      []documentView
}

type documentView struct {
	URL         string
	Title       string
	SHA256      string
	State       string
	Publication string
}

func viewDocuments(documents []domain.Document) []documentView {
	views := make([]documentView, 0, len(documents))
	for _, document := range documents {
		source := document.Publication.Source
		published := document.Publication.UpdatedAt
		views = append(views, documentView{
			URL:         document.URL,
			Title:       firstNonEmpty(document.Title, document.URL),
			SHA256:      document.SHA256,
			State:       stateLabel(document.State),
			Publication: firstNonEmpty(strings.TrimSpace(source+" "+published), "Procedencia desconocida"),
		})
	}
	return views
}

type rebajasView struct {
	Title    string
	Coverage coverageView
	Filters  filterView
	Ranking  domain.RebateRanking
	Rows     []rebateRowView
	Excluded []excludedRowView
	PrevURL  string
	NextURL  string
	Empty    bool
}

type rebateRowView struct {
	Rebate     domain.Rebate
	DetailURL  string
	Initial    *moneyView
	Awarded    *moneyView
	Latest     *moneyView
	Difference *moneyView
}

type excludedRowView struct {
	Exclusion domain.RebateExclusion
	Reason    string
}

type adjudicacionView struct {
	Title      string
	Coverage   coverageView
	Expediente domain.Expediente
	Rows       []detailRowView
	Facts      []factView
	Documents  []documentView
}

type detailRowView struct {
	Label             string
	Object            string
	LotID             string
	OriginalExclusive *moneyView
	OriginalInclusive *moneyView
	CurrentExclusive  *moneyView
	CurrentInclusive  *moneyView
	AwardedExclusive  *moneyView
	AwardedInclusive  *moneyView
	FinalExclusive    *moneyView
	FinalInclusive    *moneyView
	Award             *domain.Award
	AwardDate         string
	Contract          string
	Documents         []documentView
}

type factView struct {
	Kind        string
	Reference   string
	LotID       string
	Date        string
	Deadline    string
	Delivery    string
	Detail      string
	Amounts     []*moneyView
	Publication string
	URL         string
}

func viewFacts(facts []domain.Fact) []factView {
	views := make([]factView, 0, len(facts))
	for _, fact := range facts {
		item := factView{
			Kind:        factLabel(fact.Kind),
			Reference:   fact.Reference,
			LotID:       fact.LotID,
			Date:        fact.EffectiveDate,
			Deadline:    deadlineLabel(fact.DeadlineStatus),
			Delivery:    deliveryLabel(fact.DeliveryStatus),
			Detail:      fact.Detail,
			Publication: firstNonEmpty(fact.Publication.Source+" "+fact.Publication.UpdatedAt, "Procedencia desconocida"),
			URL:         fact.Publication.URL,
		}
		for _, amount := range fact.Amounts {
			item.Amounts = append(item.Amounts, viewMoney(amount))
		}
		views = append(views, item)
	}
	return views
}
