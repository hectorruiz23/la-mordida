package sqlite

import (
	"context"
	"fmt"
	"strings"

	"github.com/hectorruiz23/la-mordida/internal/consultas/domain"
	"github.com/hectorruiz23/la-mordida/internal/kernel"
)

type rebateUnit struct {
	rebate       domain.Rebate
	expedienteID int64
	lotKey       string
}

type budgetRevision struct {
	publicationID int64
	publication   domain.Publication
	amounts       []kernel.Money
}

type budgetHistory struct {
	revisions []budgetRevision
}

type budgetKey struct {
	expedienteID int64
	lotKey       string
}

// Rebates ranks the comparable award units of one scope by the quotient
// between their initial documented budget and their current awarded price.
// The denominator is the first revision that publishes the tax base of the
// unit, so a later correction is visible as a correction and never rewrites
// the rebate it produced. Every amount keeps the publication that sustains it.
func (s *Store) Rebates(ctx context.Context, query domain.RankingQuery) (domain.RebateRanking, error) {
	where, args, buyer, err := s.scope(ctx, query)
	if err != nil {
		return domain.RebateRanking{}, err
	}

	result := domain.RebateRanking{
		Buyer:    buyer,
		From:     query.From,
		To:       query.To,
		Offset:   query.Offset,
		Limit:    query.Limit,
		Ranking:  []domain.Rebate{},
		Excluded: []domain.RebateExclusion{},
	}

	units, err := s.rebateUnits(ctx, where, args)
	if err != nil {
		return domain.RebateRanking{}, err
	}
	if len(units) == 0 {
		return result, nil
	}

	expedienteIDs := make([]int64, 0, len(units))
	seen := make(map[int64]bool, len(units))
	for _, unit := range units {
		if !seen[unit.expedienteID] {
			seen[unit.expedienteID] = true
			expedienteIDs = append(expedienteIDs, unit.expedienteID)
		}
	}

	budgets, err := s.rebateBudgets(ctx, expedienteIDs)
	if err != nil {
		return domain.RebateRanking{}, err
	}
	amounts, err := s.rebateAwardAmounts(ctx, expedienteIDs)
	if err != nil {
		return domain.RebateRanking{}, err
	}

	comparable := make([]domain.Rebate, 0, len(units))
	for _, unit := range units {
		rebate, excluded := comparableRebate(unit, budgets[budgetKey{unit.expedienteID, unit.lotKey}], amounts[budgetKey{unit.expedienteID, unit.lotKey}])
		if excluded != nil {
			result.Excluded = append(result.Excluded, *excluded)
			continue
		}
		comparable = append(comparable, rebate)
	}

	domain.SortRebates(comparable)
	result.Total = len(comparable)
	result.ExcludedTotal = len(result.Excluded)

	start := query.Offset
	if start > len(comparable) {
		start = len(comparable)
	}
	end := start + query.Limit
	if end > len(comparable) {
		end = len(comparable)
	}
	result.Ranking = append(result.Ranking, comparable[start:end]...)
	return result, nil
}

func (s *Store) rebateUnits(ctx context.Context, where string, args []any) ([]rebateUnit, error) {
	rows, err := s.database.QueryContext(ctx, `
		SELECT u.unit_key, u.expediente_id, e.folder_id, e.object,
		       e.buyer_scheme, e.buyer_id, e.buyer_name,
		       u.lot_id, COALESCE(l.object, ''), COALESCE(u.award_date, ''),
		       u.winner_scheme, u.winner_id, u.winner_name,
		       p.source, p.external_id, p.url, p.published_at, COALESCE(p.effective_date, '')
		FROM award_unit u
		JOIN expediente e ON e.id = u.expediente_id
		JOIN publication p ON p.id = u.publication_id
		LEFT JOIN lot l ON l.expediente_id = u.expediente_id AND l.lot_id = u.lot_id
		WHERE `+where+`
		ORDER BY u.unit_key`, args...)
	if err != nil {
		return nil, fmt.Errorf("read rebate units: %w", err)
	}
	defer rows.Close()

	var units []rebateUnit
	for rows.Next() {
		var unit rebateUnit
		if err := rows.Scan(
			&unit.rebate.UnitKey, &unit.expedienteID, &unit.rebate.FolderID, &unit.rebate.Object,
			&unit.rebate.Buyer.Scheme, &unit.rebate.Buyer.ID, &unit.rebate.Buyer.Name,
			&unit.lotKey, &unit.rebate.LotObject, &unit.rebate.AwardDate,
			&unit.rebate.Winner.Scheme, &unit.rebate.Winner.ID, &unit.rebate.Winner.Name,
			&unit.rebate.AwardPublication.Source, &unit.rebate.AwardPublication.ExternalID,
			&unit.rebate.AwardPublication.URL, &unit.rebate.AwardPublication.UpdatedAt,
			&unit.rebate.AwardPublication.EffectiveDate,
		); err != nil {
			return nil, fmt.Errorf("scan rebate unit: %w", err)
		}
		unit.rebate.LotID = unit.lotKey
		unit.rebate.ExpedienteID = unit.expedienteID
		units = append(units, unit)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate rebate units: %w", err)
	}
	return units, nil
}

// rebateBudgets reads every published budget of the in-scope expedientes,
// oldest first. A revision is one publication; its amounts stay together so a
// later snapshot can be compared with the first one that documented the base.
func (s *Store) rebateBudgets(ctx context.Context, expedienteIDs []int64) (map[budgetKey]*budgetHistory, error) {
	placeholders := questionMarks(len(expedienteIDs))
	args := idArgs(expedienteIDs)
	rows, err := s.database.QueryContext(ctx, `
		SELECT b.lot_key, b.tax_base, b.currency, b.amount_cents,
		       p.expediente_id, p.id, p.source, p.external_id, p.url,
		       p.published_at, COALESCE(p.effective_date, '')
		FROM budget_amount b
		JOIN publication p ON p.id = b.publication_id
		WHERE p.expediente_id IN (`+placeholders+`)
		ORDER BY p.expediente_id, b.lot_key, julianday(p.published_at) ASC, p.id ASC, b.tax_base`, args...)
	if err != nil {
		return nil, fmt.Errorf("read rebate budgets: %w", err)
	}
	defer rows.Close()

	budgets := make(map[budgetKey]*budgetHistory)
	for rows.Next() {
		var key budgetKey
		var amount kernel.Money
		var publicationID int64
		var publication domain.Publication
		if err := rows.Scan(
			&key.lotKey, &amount.TaxBase, &amount.Currency, &amount.Cents,
			&key.expedienteID, &publicationID, &publication.Source, &publication.ExternalID,
			&publication.URL, &publication.UpdatedAt, &publication.EffectiveDate,
		); err != nil {
			return nil, fmt.Errorf("scan rebate budget: %w", err)
		}

		history := budgets[key]
		if history == nil {
			history = &budgetHistory{}
			budgets[key] = history
		}
		last := len(history.revisions) - 1
		if last < 0 || history.revisions[last].publicationID != publicationID {
			history.revisions = append(history.revisions, budgetRevision{
				publicationID: publicationID,
				publication:   publication,
			})
			last++
		}
		history.revisions[last].amounts = append(history.revisions[last].amounts, amount)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate rebate budgets: %w", err)
	}
	return budgets, nil
}

func (s *Store) rebateAwardAmounts(ctx context.Context, expedienteIDs []int64) (map[budgetKey][]kernel.Money, error) {
	placeholders := questionMarks(len(expedienteIDs))
	args := idArgs(expedienteIDs)
	rows, err := s.database.QueryContext(ctx, `
		SELECT expediente_id, lot_id, tax_base, currency, amount_cents
		FROM award_projection_amount
		WHERE expediente_id IN (`+placeholders+`)
		ORDER BY expediente_id, lot_id, tax_base`, args...)
	if err != nil {
		return nil, fmt.Errorf("read rebate award amounts: %w", err)
	}
	defer rows.Close()

	amounts := make(map[budgetKey][]kernel.Money)
	for rows.Next() {
		var key budgetKey
		var amount kernel.Money
		if err := rows.Scan(&key.expedienteID, &key.lotKey, &amount.TaxBase, &amount.Currency, &amount.Cents); err != nil {
			return nil, fmt.Errorf("scan rebate award amount: %w", err)
		}
		amounts[key] = append(amounts[key], amount)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate rebate award amounts: %w", err)
	}
	return amounts, nil
}

// initial returns the first revision that documents the tax base of the unit.
// When no revision publishes a tax-exclusive amount, the first revision with
// any amount is the initial budget and its base is used as published.
func (history *budgetHistory) initial() (budgetRevision, kernel.TaxBase, bool) {
	if history == nil {
		return budgetRevision{}, "", false
	}
	for _, revision := range history.revisions {
		if _, ok := amountFor(revision.amounts, kernel.TaxExclusive); ok {
			return revision, kernel.TaxExclusive, true
		}
	}
	for _, revision := range history.revisions {
		if len(revision.amounts) > 0 {
			return revision, revision.amounts[0].TaxBase, true
		}
	}
	return budgetRevision{}, "", false
}

// latest returns the newest revision that documents the given base. A revision
// that omits the budget is silence, not a correction to zero.
func (history *budgetHistory) latest(base kernel.TaxBase) (kernel.Money, domain.Publication, bool) {
	for index := len(history.revisions) - 1; index >= 0; index-- {
		if amount, ok := amountFor(history.revisions[index].amounts, base); ok {
			return amount, history.revisions[index].publication, true
		}
	}
	return kernel.Money{}, domain.Publication{}, false
}

func amountFor(amounts []kernel.Money, base kernel.TaxBase) (kernel.Money, bool) {
	for _, amount := range amounts {
		if amount.TaxBase == base {
			return amount, true
		}
	}
	return kernel.Money{}, false
}

func comparableRebate(unit rebateUnit, history *budgetHistory, awarded []kernel.Money) (domain.Rebate, *domain.RebateExclusion) {
	initial, _, ok := history.initial()
	if !ok {
		reason, detail := domain.ReasonBudgetMissing, "La unidad no publica presupuesto base de licitación."
		if unit.lotKey != "" {
			reason, detail = domain.ReasonNoLotBudget, "El lote no publica presupuesto propio; el del expediente no se reparte entre lotes."
		}
		exclusion := rebateExclusion(unit, reason, detail)
		return domain.Rebate{}, &exclusion
	}

	if len(awarded) == 0 {
		exclusion := rebateExclusion(unit, domain.ReasonAmountMissing, "La adjudicación vigente no publica importe.")
		return domain.Rebate{}, &exclusion
	}

	// Only a base both sides publish is comparable. The tax-exclusive base is
	// preferred because it is the one the budget is published in; the
	// tax-inclusive total is used only when both sides state it and not the
	// other.
	var budgetAmount, awardedAmount kernel.Money
	base, found := commonBase(initial.amounts, awarded)
	if !found {
		exclusion := rebateExclusion(unit, domain.ReasonTaxBaseMismatch, "El presupuesto y la adjudicación no comparten base fiscal; el IVA no se estima.")
		return domain.Rebate{}, &exclusion
	}
	budgetAmount, _ = amountFor(initial.amounts, base)
	awardedAmount, _ = amountFor(awarded, base)

	if budgetAmount.Cents <= 0 {
		exclusion := rebateExclusion(unit, domain.ReasonBudgetNotPositive, "El presupuesto publicado es cero; no puede ser denominador de una rebaja.")
		return domain.Rebate{}, &exclusion
	}
	if budgetAmount.Currency == "" || awardedAmount.Currency == "" {
		exclusion := rebateExclusion(unit, domain.ReasonCurrencyUnknown, "Falta la moneda en el presupuesto o en la adjudicación; no se supone EUR.")
		return domain.Rebate{}, &exclusion
	}
	if budgetAmount.Currency != awardedAmount.Currency {
		exclusion := rebateExclusion(unit, domain.ReasonCurrencyMismatch, "El presupuesto y la adjudicación están en monedas distintas.")
		return domain.Rebate{}, &exclusion
	}
	if awardedAmount.Cents <= 0 {
		exclusion := rebateExclusion(unit, domain.ReasonAmountNotPositive, "El importe adjudicado vigente es cero; no hay rebaja que calcular.")
		return domain.Rebate{}, &exclusion
	}

	rebate := unit.rebate
	rebate.InitialBudget = budgetAmount
	rebate.InitialBudgetPublication = initial.publication
	rebate.BudgetScope = "expediente"
	if unit.lotKey != "" {
		rebate.BudgetScope = "lot"
	}
	rebate.AwardedAmount = awardedAmount
	rebate.Difference = kernel.Money{
		Cents:    budgetAmount.Cents - awardedAmount.Cents,
		Currency: budgetAmount.Currency,
		TaxBase:  base,
	}
	rebate.Ratio, rebate.Percent = domain.RatioParts(rebate.Difference.Cents, budgetAmount.Cents)

	if latest, publication, ok := history.latest(base); ok && (latest.Cents != budgetAmount.Cents || latest.Currency != budgetAmount.Currency) {
		rebate.BudgetCorrected = true
		rebate.LatestBudget = &latest
		rebate.LatestBudgetPublication = &publication
	}
	return rebate, nil
}

func commonBase(budget, awarded []kernel.Money) (kernel.TaxBase, bool) {
	for _, base := range []kernel.TaxBase{kernel.TaxExclusive, kernel.TaxInclusive} {
		_, budgetHas := amountFor(budget, base)
		_, awardedHas := amountFor(awarded, base)
		if budgetHas && awardedHas {
			return base, true
		}
	}
	return "", false
}

func rebateExclusion(unit rebateUnit, reason, detail string) domain.RebateExclusion {
	return domain.RebateExclusion{
		UnitKey:          unit.rebate.UnitKey,
		ExpedienteID:     unit.rebate.ExpedienteID,
		FolderID:         unit.rebate.FolderID,
		Object:           unit.rebate.Object,
		Buyer:            unit.rebate.Buyer,
		LotID:            unit.rebate.LotID,
		LotObject:        unit.rebate.LotObject,
		AwardDate:        unit.rebate.AwardDate,
		Winner:           unit.rebate.Winner,
		AwardPublication: unit.rebate.AwardPublication,
		Reason:           reason,
		Detail:           detail,
	}
}

func questionMarks(count int) string {
	return strings.TrimSuffix(strings.Repeat("?,", count), ",")
}

func idArgs(ids []int64) []any {
	args := make([]any, len(ids))
	for index, id := range ids {
		args[index] = id
	}
	return args
}
