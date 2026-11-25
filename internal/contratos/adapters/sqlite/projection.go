package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/hectorruiz23/la-mordida/internal/kernel"
)

type revisionSnapshot struct {
	id          int64
	publishedAt string
	object      string
	status      string
}

// rebuildExpedienteProjection picks the newest source revision and, for each
// projected field, the newest revision that actually states it. An absent
// field falls through to the older revision; two different values at the same
// instant make only that field indeterminate. An explicit annulment marks the
// procedure, never the silence of a snapshot.
func rebuildExpedienteProjection(ctx context.Context, tx *sql.Tx, expedienteID int64) error {
	rows, err := tx.QueryContext(ctx, `
		SELECT id, published_at, object, status
		FROM publication WHERE expediente_id = ?
		ORDER BY julianday(published_at) DESC, id DESC`, expedienteID)
	if err != nil {
		return fmt.Errorf("read revisions for projection: %w", err)
	}
	defer rows.Close()

	var revisions []revisionSnapshot
	for rows.Next() {
		var revision revisionSnapshot
		if err := rows.Scan(&revision.id, &revision.publishedAt, &revision.object, &revision.status); err != nil {
			return fmt.Errorf("scan revision for projection: %w", err)
		}
		revisions = append(revisions, revision)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate revisions for projection: %w", err)
	}
	if len(revisions) == 0 {
		return nil
	}

	object, objectConflict := projectField(revisions, func(revision revisionSnapshot) string { return revision.object })
	status, statusConflict := projectField(revisions, func(revision revisionSnapshot) string { return revision.status })

	state := "known"
	if objectConflict || statusConflict {
		state = "indeterminate"
	}

	var annulments int
	if err := tx.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM fact WHERE expediente_id = ? AND kind = 'annulment'`,
		expedienteID,
	).Scan(&annulments); err != nil {
		return fmt.Errorf("count annulments: %w", err)
	}
	if annulments > 0 {
		state = "annulled"
	}

	if _, err := tx.ExecContext(ctx, `
		UPDATE expediente
		SET object = ?, status = ?, current_publication_id = ?, projection_state = ?
		WHERE id = ?`,
		object, status, revisions[0].id, state, expedienteID,
	); err != nil {
		return fmt.Errorf("update expediente projection: %w", err)
	}
	return nil
}

// projectField walks the revisions newest first. Blank values are unknown and
// are skipped; the newest value wins. Different non-blank values inside the
// same published instant are an irresolvable conflict and leave the field
// empty.
func projectField(revisions []revisionSnapshot, value func(revisionSnapshot) string) (string, bool) {
	for start := 0; start < len(revisions); {
		end := start + 1
		for end < len(revisions) && revisions[end].publishedAt == revisions[start].publishedAt {
			end++
		}

		var chosen string
		for _, revision := range revisions[start:end] {
			candidate := strings.TrimSpace(value(revision))
			if candidate == "" {
				continue
			}
			if chosen == "" {
				chosen = candidate
				continue
			}
			if chosen != candidate {
				return "", true
			}
		}
		if chosen != "" {
			return chosen, false
		}
		start = end
	}
	return "", false
}

type awardSnapshot struct {
	id                int64
	publicationID     int64
	publishedAt       string
	lotID             string
	awardDate         string
	winnerScheme      string
	winnerID          string
	winnerName        string
	contractID        string
	contractIssueDate string
	resultCode        string
	amounts           []kernel.Money
}

// rebuildAwardProjections materialises the current award of every
// (procedure, lot). Awards of an older revision never rewind it, a newer
// snapshot that omits an award never cancels it, and two conflicting awards at
// the same published instant make the projection indeterminate while both
// historical rows stay in award.
func rebuildAwardProjections(ctx context.Context, tx *sql.Tx, expedienteID int64) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM award_unit WHERE expediente_id = ?`, expedienteID); err != nil {
		return fmt.Errorf("clear award units: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM award_projection_amount WHERE expediente_id = ?`, expedienteID); err != nil {
		return fmt.Errorf("clear award projection amounts: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM award_projection WHERE expediente_id = ?`, expedienteID); err != nil {
		return fmt.Errorf("clear award projections: %w", err)
	}

	rows, err := tx.QueryContext(ctx, `
		SELECT a.id, a.publication_id, p.published_at, a.lot_id, a.result_code,
		       COALESCE(a.award_date, ''), a.winner_scheme, a.winner_id, a.winner_name,
		       a.contract_id, COALESCE(a.contract_issue_date, '')
		FROM award a
		JOIN publication p ON p.id = a.publication_id
		WHERE a.expediente_id = ?
		ORDER BY a.lot_id, julianday(p.published_at) DESC, p.id DESC, a.id DESC`, expedienteID)
	if err != nil {
		return fmt.Errorf("read awards for projection: %w", err)
	}
	defer rows.Close()

	var snapshots []awardSnapshot
	for rows.Next() {
		var snapshot awardSnapshot
		if err := rows.Scan(
			&snapshot.id, &snapshot.publicationID, &snapshot.publishedAt, &snapshot.lotID,
			&snapshot.resultCode, &snapshot.awardDate, &snapshot.winnerScheme, &snapshot.winnerID,
			&snapshot.winnerName, &snapshot.contractID, &snapshot.contractIssueDate,
		); err != nil {
			return fmt.Errorf("scan award for projection: %w", err)
		}
		if snapshot.amounts, err = readAwardAmounts(ctx, tx, snapshot.id); err != nil {
			return err
		}
		snapshots = append(snapshots, snapshot)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate awards for projection: %w", err)
	}

	var annulments int
	if err := tx.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM fact WHERE expediente_id = ? AND kind = 'annulment'`,
		expedienteID,
	).Scan(&annulments); err != nil {
		return fmt.Errorf("count annulments: %w", err)
	}

	for start := 0; start < len(snapshots); {
		// Every row of one lot is contiguous because of the ORDER BY.
		lotEnd := start + 1
		for lotEnd < len(snapshots) && snapshots[lotEnd].lotID == snapshots[start].lotID {
			lotEnd++
		}
		group := snapshots[start:lotEnd]

		// The newest published instant that carries this award key. An older
		// revision is history only; a newer one without the key is silence.
		newest := group[0].publishedAt
		candidateEnd := 1
		for candidateEnd < len(group) && group[candidateEnd].publishedAt == newest {
			candidateEnd++
		}
		candidates := group[:candidateEnd]

		state := "known"
		if len(candidates) > 1 && !sameAward(candidates) {
			state = "indeterminate"
		}
		if annulments > 0 {
			state = "cancelled"
		}

		projected := mergeAwards(candidates)
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO award_projection (
				expediente_id, lot_id, state, publication_id, result_code, award_date,
				winner_scheme, winner_id, winner_name, contract_id, contract_issue_date)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			expedienteID, projected.lotID, state, projected.publicationID, projected.resultCode,
			nullableString(projected.awardDate), projected.winnerScheme, projected.winnerID,
			projected.winnerName, projected.contractID, nullableString(projected.contractIssueDate),
		); err != nil {
			return fmt.Errorf("store award projection for lot %q: %w", projected.lotID, err)
		}
		for _, amount := range projected.amounts {
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO award_projection_amount (expediente_id, lot_id, tax_base, currency, amount_cents)
				VALUES (?, ?, ?, ?, ?)`,
				expedienteID, projected.lotID, string(amount.TaxBase), amount.Currency, amount.Cents,
			); err != nil {
				return fmt.Errorf("store award projection amount for lot %q: %w", projected.lotID, err)
			}
		}
		start = lotEnd
	}

	// The current awarded result is also the counting unit of the winner
	// ranking. It is materialised in the same transaction so the ranking never
	// reads a projection that disagrees with the stored unit.
	//
	// Only a unit whose scope was selected belongs in the ranking. The check
	// reads the whole history of the expediente, not just the current
	// publication: a correction that omits the CPV still counts, and the same
	// selected units are counted whether the source stored only them or the
	// whole universe.
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO award_unit (
			unit_key, expediente_id, lot_id, publication_id, award_date, result_code,
			winner_scheme, winner_id, winner_name, contract_id, contract_issue_date,
			buyer_ref, state)
		SELECT printf('%d:%s', ap.expediente_id, ap.lot_id), ap.expediente_id, ap.lot_id,
		       ap.publication_id, ap.award_date, ap.result_code,
		       ap.winner_scheme, ap.winner_id, ap.winner_name,
		       ap.contract_id, ap.contract_issue_date, e.buyer_ref, ap.state
		FROM award_projection ap
		JOIN expediente e ON e.id = ap.expediente_id
		WHERE ap.expediente_id = ? AND ap.result_code = '8'
		  AND EXISTS (
			SELECT 1 FROM cpv c
			JOIN publication cp ON cp.id = c.publication_id
			WHERE cp.expediente_id = ap.expediente_id
			  AND c.included = 1
			  AND ((ap.lot_id = '' AND c.scope = 'expediente' AND c.lot_key = '')
			    OR (ap.lot_id <> '' AND c.scope = 'lote' AND c.lot_key = ap.lot_id)))`,
		expedienteID,
	); err != nil {
		return fmt.Errorf("store award units: %w", err)
	}
	return nil
}

func nullableString(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func readAwardAmounts(ctx context.Context, tx *sql.Tx, awardID int64) ([]kernel.Money, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT tax_base, currency, amount_cents FROM award_amount
		WHERE award_id = ? ORDER BY tax_base`, awardID)
	if err != nil {
		return nil, fmt.Errorf("read award amounts: %w", err)
	}
	defer rows.Close()

	var amounts []kernel.Money
	for rows.Next() {
		var base, currency string
		var cents int64
		if err := rows.Scan(&base, &currency, &cents); err != nil {
			return nil, fmt.Errorf("scan award amount: %w", err)
		}
		amounts = append(amounts, kernel.Money{Cents: cents, Currency: currency, TaxBase: kernel.TaxBase(base)})
	}
	return amounts, rows.Err()
}

func sameAward(candidates []awardSnapshot) bool {
	first := awardSignature(candidates[0])
	for _, candidate := range candidates[1:] {
		if awardSignature(candidate) != first {
			return false
		}
	}
	return true
}

func awardSignature(snapshot awardSnapshot) string {
	var builder strings.Builder
	builder.WriteString(snapshot.resultCode)
	builder.WriteString("\x00")
	builder.WriteString(snapshot.awardDate)
	builder.WriteString("\x00")
	builder.WriteString(snapshot.winnerScheme)
	builder.WriteString("\x00")
	builder.WriteString(snapshot.winnerID)
	builder.WriteString("\x00")
	builder.WriteString(snapshot.winnerName)
	builder.WriteString("\x00")
	builder.WriteString(snapshot.contractID)
	builder.WriteString("\x00")
	builder.WriteString(snapshot.contractIssueDate)

	amounts := append([]kernel.Money(nil), snapshot.amounts...)
	sort.Slice(amounts, func(i, j int) bool { return amounts[i].TaxBase < amounts[j].TaxBase })
	for _, amount := range amounts {
		fmt.Fprintf(&builder, "\x00%s:%s:%d", amount.TaxBase, amount.Currency, amount.Cents)
	}
	return builder.String()
}

// mergeAwards keeps the fields every candidate agrees on. A disputed field is
// left unknown; a disputed amount is not published, because choosing one value
// would invent an outcome the source does not support.
func mergeAwards(candidates []awardSnapshot) awardSnapshot {
	merged := awardSnapshot{
		lotID:         candidates[0].lotID,
		publicationID: candidates[0].publicationID,
		publishedAt:   candidates[0].publishedAt,
	}
	merged.resultCode = agreed(candidates, func(candidate awardSnapshot) string { return candidate.resultCode })
	merged.awardDate = agreed(candidates, func(candidate awardSnapshot) string { return candidate.awardDate })
	merged.winnerScheme = agreed(candidates, func(candidate awardSnapshot) string { return candidate.winnerScheme })
	merged.winnerID = agreed(candidates, func(candidate awardSnapshot) string { return candidate.winnerID })
	merged.winnerName = agreed(candidates, func(candidate awardSnapshot) string { return candidate.winnerName })
	merged.contractID = agreed(candidates, func(candidate awardSnapshot) string { return candidate.contractID })
	merged.contractIssueDate = agreed(candidates, func(candidate awardSnapshot) string { return candidate.contractIssueDate })
	merged.amounts = agreedAmounts(candidates)
	return merged
}

func agreed(candidates []awardSnapshot, field func(awardSnapshot) string) string {
	value := field(candidates[0])
	for _, candidate := range candidates[1:] {
		if field(candidate) != value {
			return ""
		}
	}
	return value
}

func agreedAmounts(candidates []awardSnapshot) []kernel.Money {
	if len(candidates) == 0 {
		return nil
	}
	values := make(map[kernel.TaxBase]string, len(candidates[0].amounts))
	var order []kernel.TaxBase
	for _, amount := range candidates[0].amounts {
		values[amount.TaxBase] = fmt.Sprintf("%s\x00%d", amount.Currency, amount.Cents)
		order = append(order, amount.TaxBase)
	}

	for _, candidate := range candidates[1:] {
		seen := make(map[kernel.TaxBase]string, len(candidate.amounts))
		for _, amount := range candidate.amounts {
			seen[amount.TaxBase] = fmt.Sprintf("%s\x00%d", amount.Currency, amount.Cents)
		}
		for _, base := range order {
			if values[base] != seen[base] {
				values[base] = ""
			}
		}
	}

	var amounts []kernel.Money
	for _, base := range order {
		encoded := values[base]
		if encoded == "" {
			continue
		}
		currency, centsRaw, ok := strings.Cut(encoded, "\x00")
		if !ok {
			continue
		}
		cents, err := strconv.ParseInt(centsRaw, 10, 64)
		if err != nil {
			continue
		}
		amounts = append(amounts, kernel.Money{Cents: cents, Currency: currency, TaxBase: base})
	}
	return amounts
}
