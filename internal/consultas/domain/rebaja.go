package domain

import (
	"math/big"
	"sort"
	"strings"

	"github.com/hectorruiz23/la-mordida/internal/kernel"
)

// Rebate exclusion reasons. Each one names the fact that stops a comparability
// and is kept beside the excluded unit: a missing amount is never read as zero
// and a different currency is never converted.
const (
	// ReasonBudgetMissing: the unit publishes no budget at all.
	ReasonBudgetMissing = "budget_missing"
	// ReasonNoLotBudget: the awarded lot publishes no budget of its own; the
	// project budget is not split between lots without evidence.
	ReasonNoLotBudget = "no_lot_budget"
	// ReasonBudgetNotPositive: the published budget is zero, so it cannot be
	// the denominator of a quotient.
	ReasonBudgetNotPositive = "budget_not_positive"
	// ReasonAmountMissing: the current award publishes no amount.
	ReasonAmountMissing = "amount_missing"
	// ReasonAmountNotPositive: the current award publishes zero.
	ReasonAmountNotPositive = "amount_not_positive"
	// ReasonCurrencyUnknown: either side omits its currency; EUR is not
	// assumed.
	ReasonCurrencyUnknown = "currency_unknown"
	// ReasonCurrencyMismatch: the two amounts are published in different
	// currencies.
	ReasonCurrencyMismatch = "currency_mismatch"
	// ReasonTaxBaseMismatch: the budget and the award do not share a tax base,
	// so VAT cannot be separated without guessing.
	ReasonTaxBaseMismatch = "tax_base_mismatch"
)

// Rebate is one comparable award unit: the initial documented budget of the
// unit, the current awarded price and the quotient between them. Both amounts
// carry the publication that sustains them. The quotient is ordered by its
// exact rational value; Ratio and Percent are only its rendered form.
//
// LatestBudget is present only when a later publication corrected the budget
// the row is ranked with. The initial budget is never overwritten: it keeps
// its own publication, so a correction is visible instead of silently
// replacing the denominator.
type Rebate struct {
	UnitKey                  string        `json:"unit_key"`
	FolderID                 string        `json:"folder_id"`
	Object                   string        `json:"object,omitempty"`
	Buyer                    Party         `json:"buyer"`
	LotID                    string        `json:"lot_id,omitempty"`
	LotObject                string        `json:"lot_object,omitempty"`
	AwardDate                string        `json:"award_date,omitempty"`
	Winner                   Party         `json:"winner"`
	InitialBudget            kernel.Money  `json:"initial_budget"`
	InitialBudgetPublication Publication   `json:"initial_budget_publication"`
	LatestBudget             *kernel.Money `json:"latest_budget,omitempty"`
	LatestBudgetPublication  *Publication  `json:"latest_budget_publication,omitempty"`
	BudgetCorrected          bool          `json:"budget_corrected"`
	BudgetScope              string        `json:"budget_scope"`
	AwardedAmount            kernel.Money  `json:"awarded_amount"`
	AwardPublication         Publication   `json:"award_publication"`
	Difference               kernel.Money  `json:"difference"`
	Ratio                    string        `json:"ratio"`
	Percent                  string        `json:"percent"`
}

// RebateExclusion is a unit that cannot enter the ranking. Reason is the
// machine code above and Detail explains it to the reader.
type RebateExclusion struct {
	UnitKey          string      `json:"unit_key"`
	FolderID         string      `json:"folder_id"`
	Object           string      `json:"object,omitempty"`
	Buyer            Party       `json:"buyer"`
	LotID            string      `json:"lot_id,omitempty"`
	LotObject        string      `json:"lot_object,omitempty"`
	AwardDate        string      `json:"award_date,omitempty"`
	Winner           Party       `json:"winner"`
	AwardPublication Publication `json:"award_publication"`
	Reason           string      `json:"reason"`
	Detail           string      `json:"detail"`
}

// RebateRanking is the rebate reading of one scope. Total counts the
// comparable units, not the page; Excluded keeps every unit the scope had to
// leave out with its reason.
type RebateRanking struct {
	Buyer         *Party            `json:"buyer,omitempty"`
	From          string            `json:"from,omitempty"`
	To            string            `json:"to,omitempty"`
	Total         int               `json:"total"`
	Offset        int               `json:"offset"`
	Limit         int               `json:"limit"`
	Ranking       []Rebate          `json:"ranking"`
	ExcludedTotal int               `json:"excluded_total"`
	Excluded      []RebateExclusion `json:"excluded"`
}

// RatioParts renders (difference / budget) as a six-place ratio and as a
// four-place percentage. The division is exact and only the presentation is
// rounded half away from zero; a non-positive budget has no quotient.
func RatioParts(differenceCents, budgetCents int64) (string, string) {
	if budgetCents <= 0 {
		return "0.000000", "0.0000"
	}

	scaled := scaledMillionths(differenceCents, budgetCents)
	// A millionth of the ratio is a ten-thousandth of the percentage: the
	// same integer renders both, at different decimal places.
	return formatFixed(scaled, 6), formatFixed(scaled, 4)
}

func scaledMillionths(differenceCents, budgetCents int64) *big.Int {
	numerator := new(big.Int).Mul(big.NewInt(differenceCents), big.NewInt(1_000_000))
	denominator := big.NewInt(budgetCents)
	quotient, remainder := new(big.Int), new(big.Int)
	quotient.QuoRem(numerator, denominator, remainder)

	twiceRemainder := new(big.Int).Lsh(new(big.Int).Abs(remainder), 1)
	if twiceRemainder.Cmp(denominator) >= 0 {
		if numerator.Sign() < 0 {
			quotient.Sub(quotient, big.NewInt(1))
		} else {
			quotient.Add(quotient, big.NewInt(1))
		}
	}
	return quotient
}

func formatFixed(value *big.Int, places int) string {
	sign := ""
	magnitude := value
	if magnitude.Sign() < 0 {
		sign = "-"
		magnitude = new(big.Int).Abs(magnitude)
	}

	digits := magnitude.String()
	if len(digits) <= places {
		digits = strings.Repeat("0", places-len(digits)+1) + digits
	}
	whole := digits[:len(digits)-places]
	fraction := digits[len(digits)-places:]
	return sign + whole + "." + fraction
}

// SortRebates orders by the exact quotient, highest first, and breaks a tie by
// the unit key. Integer cross-multiplication keeps the comparison exact: no
// float64 touches a published amount.
func SortRebates(rebates []Rebate) {
	sort.SliceStable(rebates, func(i, j int) bool {
		left := new(big.Int).Mul(big.NewInt(rebates[i].Difference.Cents), big.NewInt(rebates[j].InitialBudget.Cents))
		right := new(big.Int).Mul(big.NewInt(rebates[j].Difference.Cents), big.NewInt(rebates[i].InitialBudget.Cents))
		if comparison := left.Cmp(right); comparison != 0 {
			return comparison > 0
		}
		return rebates[i].UnitKey < rebates[j].UnitKey
	})
}
