package domain

import (
	"testing"

	"github.com/hectorruiz23/la-mordida/internal/kernel"
)

func TestRatioPartsRoundsOnlyPresentation(t *testing.T) {
	cases := []struct {
		name       string
		difference int64
		budget     int64
		ratio      string
		percent    string
	}{
		{"twenty percent", 2_000_000, 10_000_000, "0.200000", "20.0000"},
		{"zero percent", 0, 10_000, "0.000000", "0.0000"},
		{"one point two percent", 17_838, 1_487_000, "0.011996", "1.1996"},
		{"half rounds away from zero", 5, 10, "0.500000", "50.0000"},
		{"negative rebate", -100, 1_000, "-0.100000", "-10.0000"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			ratio, percent := RatioParts(test.difference, test.budget)
			if ratio != test.ratio || percent != test.percent {
				t.Fatalf("RatioParts(%d, %d) = %q, %q, want %q, %q",
					test.difference, test.budget, ratio, percent, test.ratio, test.percent)
			}
		})
	}
}

func TestSortRebatesUsesTheExactQuotientBeforeTheKey(t *testing.T) {
	money := func(cents int64) kernel.Money {
		return kernel.Money{Cents: cents, Currency: "EUR", TaxBase: kernel.TaxExclusive}
	}

	// One third and 0.333333 both render 0.333333, but one third is larger.
	rebates := []Rebate{
		{UnitKey: "b", InitialBudget: money(1_000_000), Difference: money(333_333)},
		{UnitKey: "a", InitialBudget: money(300), Difference: money(100)},
	}
	SortRebates(rebates)

	if rebates[0].UnitKey != "a" {
		t.Fatalf("first unit = %q, want the exact one third first", rebates[0].UnitKey)
	}
	if ratio, _ := RatioParts(rebates[0].Difference.Cents, rebates[0].InitialBudget.Cents); ratio != "0.333333" {
		t.Fatalf("exact ratio renders %q", ratio)
	}
	if ratio, _ := RatioParts(rebates[1].Difference.Cents, rebates[1].InitialBudget.Cents); ratio != "0.333333" {
		t.Fatalf("rounded ratio renders %q", ratio)
	}

	// Same quotient: the unit key breaks the tie.
	tie := []Rebate{
		{UnitKey: "2:1", InitialBudget: money(100), Difference: money(20)},
		{UnitKey: "1:2", InitialBudget: money(200), Difference: money(40)},
	}
	SortRebates(tie)
	if tie[0].UnitKey != "1:2" || tie[1].UnitKey != "2:1" {
		t.Fatalf("tie order = %q, %q, want the key ascending", tie[0].UnitKey, tie[1].UnitKey)
	}
}
