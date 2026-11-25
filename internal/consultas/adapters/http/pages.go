package http

import (
	"embed"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"log"
	"net/http"
	"strconv"
	"strings"

	"github.com/hectorruiz23/la-mordida/internal/consultas/app"
	"github.com/hectorruiz23/la-mordida/internal/consultas/domain"
	"github.com/hectorruiz23/la-mordida/internal/kernel"
)

//go:embed templates/*.html assets/*
var files embed.FS

var pages = template.Must(template.New("pages").Funcs(template.FuncMap{
	"money":     moneyText,
	"moneyAt":   moneyAt,
	"baseLabel": taxBaseLabel,
	"decimal":   decimal,
	"inc":       func(index int) int { return index + 1 },
}).ParseFS(files, "templates/*.html"))

// Assets serves the stylesheet embedded in the binary.
func Assets() http.Handler {
	sub, err := fs.Sub(files, "assets")
	if err != nil {
		panic(err)
	}
	return http.StripPrefix("/assets/", http.FileServer(http.FS(sub)))
}

// Adjudicatarios serves the ranking of winners as HTML. It shares the scope,
// the denominator and the pagination with the JSON reading.
func (h *Handler) Adjudicatarios(w http.ResponseWriter, r *http.Request) {
	query, err := rankingQuery(r)
	if err != nil {
		h.renderError(w, r, http.StatusBadRequest, "La consulta no es válida. Revisa el organismo, las fechas y la página.")
		return
	}
	ranking, err := h.service.Ranking(r.Context(), query)
	if err != nil {
		h.renderReadingError(w, r, "ranking", err)
		return
	}
	coverage, err := h.service.Coverage(r.Context())
	if err != nil {
		h.renderReadingError(w, r, "coverage", err)
		return
	}

	view := adjudicatariosView{
		Title:      "Adjudicatarios",
		Coverage:   viewCoverage(coverage),
		Filters:    viewFilters(query, ranking.Limit),
		Ranking:    ranking,
		Empty:      len(ranking.Ranking) == 0,
		EntityURLs: map[string]string{},
	}
	view.PrevURL, view.NextURL = pageURLs(r, ranking.Total, ranking.Offset, ranking.Limit, "offset")

	for _, row := range ranking.Ranking {
		view.EntityURLs[row.Entity.Key] = entityURL(r, row.Entity.Key)
	}

	if entityKey := strings.TrimSpace(r.URL.Query().Get("entity")); entityKey != "" {
		awardsQuery := query
		awardsQuery.Offset = 0
		if raw := r.URL.Query().Get("awards_offset"); raw != "" {
			offset, err := strconv.Atoi(raw)
			if err != nil || offset < 0 {
				h.renderError(w, r, http.StatusBadRequest, "La página de adjudicaciones no es válida.")
				return
			}
			awardsQuery.Offset = offset
		}
		page, err := h.service.Awards(r.Context(), awardsQuery, entityKey)
		if err != nil {
			h.renderReadingError(w, r, "awards", err)
			return
		}
		awards := &awardsView{
			Entity: page.Entity,
			Total:  page.Total,
			Offset: page.Offset,
			Limit:  page.Limit,
		}
		for _, line := range page.Awards {
			awards.Rows = append(awards.Rows, awardRowView{
				Line:      line,
				DetailURL: detailURL(line.ExpedienteID),
				Date:      line.AwardDate,
				Docs:      viewDocuments(line.Documents),
			})
		}
		awards.PrevURL, awards.NextURL = pageURLs(r, page.Total, page.Offset, page.Limit, "awards_offset")
		view.EntityKey = entityKey
		view.Awards = awards
	}

	h.render(w, http.StatusOK, "adjudicatarios.html", view)
}

// Rebajas serves the ranking of comparable rebates as HTML.
func (h *Handler) Rebajas(w http.ResponseWriter, r *http.Request) {
	query, err := rankingQuery(r)
	if err != nil {
		h.renderError(w, r, http.StatusBadRequest, "La consulta no es válida. Revisa el organismo, las fechas y la página.")
		return
	}
	ranking, err := h.service.Rebates(r.Context(), query)
	if err != nil {
		h.renderReadingError(w, r, "rebates", err)
		return
	}
	coverage, err := h.service.Coverage(r.Context())
	if err != nil {
		h.renderReadingError(w, r, "coverage", err)
		return
	}

	view := rebajasView{
		Title:    "Rebajas",
		Coverage: viewCoverage(coverage),
		Filters:  viewFilters(query, ranking.Limit),
		Ranking:  ranking,
		Empty:    len(ranking.Ranking) == 0,
	}
	view.PrevURL, view.NextURL = pageURLs(r, ranking.Total, ranking.Offset, ranking.Limit, "offset")

	for _, rebate := range ranking.Ranking {
		row := rebateRowView{
			Rebate:     rebate,
			DetailURL:  detailURL(rebate.ExpedienteID),
			Initial:    viewMoney(rebate.InitialBudget),
			Awarded:    viewMoney(rebate.AwardedAmount),
			Difference: viewMoney(rebate.Difference),
		}
		if rebate.LatestBudget != nil {
			row.Latest = viewMoney(*rebate.LatestBudget)
		}
		view.Rows = append(view.Rows, row)
	}
	for _, exclusion := range ranking.Excluded {
		view.Excluded = append(view.Excluded, excludedRowView{
			Exclusion: exclusion,
			Reason:    reasonLabel(exclusion.Reason),
		})
	}

	h.render(w, http.StatusOK, "rebajas.html", view)
}

// Adjudicacion serves the detail behind a ranking row: the original budget,
// the current award and the final accredited amount, with the history and the
// documents that sustain them.
func (h *Handler) Adjudicacion(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		h.renderError(w, r, http.StatusBadRequest, "El identificador de la adjudicación no es válido.")
		return
	}
	expediente, err := h.service.Expediente(r.Context(), id)
	if err != nil {
		h.renderReadingError(w, r, "expediente", err)
		return
	}
	coverage, err := h.service.Coverage(r.Context())
	if err != nil {
		h.renderReadingError(w, r, "coverage", err)
		return
	}

	view := adjudicacionView{
		Title:      "Adjudicación " + expediente.FolderID,
		Coverage:   viewCoverage(coverage),
		Expediente: expediente,
		Facts:      viewFacts(expediente.Facts),
		Documents:  viewDocuments(expediente.Documents),
	}
	view.Rows = detailRows(expediente)
	h.render(w, http.StatusOK, "adjudicacion.html", view)
}

// detailRows keeps the original budget, the current award and the final
// accredited fact as separate rows. A lot without its own budget does not
// borrow the project budget, and a missing fact is unknown, never zero.
func detailRows(expediente domain.Expediente) []detailRowView {
	awards := make(map[string]*domain.Award, len(expediente.Awards))
	for index := range expediente.Awards {
		award := &expediente.Awards[index]
		awards[award.LotID] = award
	}

	var rows []detailRowView
	if len(expediente.Lots) == 0 {
		rows = append(rows, detailRow(expediente, "", "Procedimiento", "", expediente.OriginalBudget, expediente.Budget, awards[""]))
	} else {
		for _, lot := range expediente.Lots {
			original := lot.OriginalBudget
			label := "Lote " + lot.ID
			rows = append(rows, detailRow(expediente, lot.ID, label, lot.Object, original, lot.Budget, awards[lot.ID]))
		}
		if award, ok := awards[""]; ok {
			rows = append(rows, detailRow(expediente, "", "Adjudicación global", "", expediente.OriginalBudget, expediente.Budget, award))
		}
	}
	return rows
}

func detailRow(expediente domain.Expediente, lotID, label, object string, original, current []kernel.Money, award *domain.Award) detailRowView {
	row := detailRowView{
		Label:             label,
		Object:            object,
		LotID:             lotID,
		OriginalExclusive: moneyAt(original, string(kernel.TaxExclusive)),
		OriginalInclusive: moneyAt(original, string(kernel.TaxInclusive)),
		CurrentExclusive:  moneyAt(current, string(kernel.TaxExclusive)),
		CurrentInclusive:  moneyAt(current, string(kernel.TaxInclusive)),
	}
	if award != nil {
		row.Award = award
		row.AwardDate = award.AwardDate
		row.AwardedExclusive = moneyAt(award.Amounts, string(kernel.TaxExclusive))
		row.AwardedInclusive = moneyAt(award.Amounts, string(kernel.TaxInclusive))
		if award.Contract != nil {
			row.Contract = award.Contract.ID
		}
	}
	for _, fact := range expediente.Facts {
		if fact.Kind != "final_payment" || fact.LotID != lotID || len(fact.Amounts) == 0 {
			continue
		}
		row.FinalExclusive = moneyAt(fact.Amounts, string(kernel.TaxExclusive))
		row.FinalInclusive = moneyAt(fact.Amounts, string(kernel.TaxInclusive))
		row.Contract = firstNonEmpty(row.Contract, fact.Reference)
		break
	}
	return row
}

func viewFilters(query domain.RankingQuery, limit int) filterView {
	return filterView{
		BuyerScheme: query.BuyerScheme,
		BuyerID:     query.BuyerID,
		From:        query.From,
		To:          query.To,
		Limit:       limit,
	}
}

func detailURL(id int64) string {
	return "/adjudicaciones/" + strconv.FormatInt(id, 10)
}

func entityURL(r *http.Request, key string) string {
	values := r.URL.Query()
	values.Set("entity", key)
	values.Del("offset")
	values.Del("awards_offset")
	return r.URL.Path + "?" + values.Encode()
}

// pageURLs builds the previous and next links without dropping the filters.
// Offset is the only parameter changed; the ordering behind it is stable, so
// a page boundary does not move between two requests over the same data.
func pageURLs(r *http.Request, total, offset, limit int, parameter string) (string, string) {
	if limit <= 0 {
		return "", ""
	}
	build := func(next int) string {
		values := r.URL.Query()
		values.Set(parameter, strconv.Itoa(next))
		return r.URL.Path + "?" + values.Encode()
	}
	previous := ""
	if offset > 0 {
		next := offset - limit
		if next < 0 {
			next = 0
		}
		previous = build(next)
	}
	next := ""
	if offset+limit < total {
		next = build(offset + limit)
	}
	return previous, next
}

func (h *Handler) render(w http.ResponseWriter, status int, name string, view any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	if err := pages.ExecuteTemplate(w, name, view); err != nil {
		log.Printf("consultas: render %s: %v", name, err)
	}
}

type errorView struct {
	Title    string
	Coverage coverageView
	Status   string
	Message  string
}

func (h *Handler) renderError(w http.ResponseWriter, r *http.Request, status int, message string) {
	h.render(w, status, "error.html", errorView{
		Title:   "No se pudo leer la consulta",
		Status:  fmt.Sprintf("%d %s", status, http.StatusText(status)),
		Message: message,
	})
}

func (h *Handler) renderReadingError(w http.ResponseWriter, r *http.Request, reading string, err error) {
	switch {
	case errors.Is(err, domain.ErrExpedienteNotFound):
		h.renderError(w, r, http.StatusNotFound, "No hay ninguna adjudicación importada con ese identificador.")
	case errors.Is(err, domain.ErrUnknownBuyer):
		h.renderError(w, r, http.StatusNotFound, "Ese organismo no figura entre los datos importados.")
	case errors.Is(err, app.ErrInvalidQuery), errors.Is(err, domain.ErrInvalidEntityKey):
		h.renderError(w, r, http.StatusBadRequest, "La consulta no es válida. Revisa el organismo, las fechas y la página.")
	default:
		log.Printf("consultas: %s: %v", reading, err)
		h.renderError(w, r, http.StatusInternalServerError, "No se pudo leer la consulta. Inténtalo de nuevo.")
	}
}
