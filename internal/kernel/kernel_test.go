package kernel

import (
	"testing"
	"time"
)

func TestParseMoneyKeepsExactCentsAndCurrency(t *testing.T) {
	tests := []struct {
		raw      string
		currency string
		want     string
		cents    int64
	}{
		{raw: "14870", currency: "EUR", want: "14870.00", cents: 1487000},
		{raw: "17992.7", currency: "EUR", want: "17992.70", cents: 1799270},
		{raw: "0", currency: "EUR", want: "0.00", cents: 0},
		{raw: "900", currency: "", want: "900.00", cents: 90000},
	}
	for _, tt := range tests {
		t.Run(tt.raw, func(t *testing.T) {
			money, err := ParseMoney(tt.raw, tt.currency, TaxExclusive)
			if err != nil {
				t.Fatalf("ParseMoney: %v", err)
			}
			if money.String() != tt.want {
				t.Errorf("String() = %s, want %s", money.String(), tt.want)
			}
			if money.Cents != tt.cents {
				t.Errorf("Cents = %d, want %d", money.Cents, tt.cents)
			}
			if money.Currency != tt.currency {
				t.Errorf("Currency = %q, want %q", money.Currency, tt.currency)
			}
		})
	}
}

func TestParseMoneyRefusesToRound(t *testing.T) {
	if _, err := ParseMoney("10.001", "EUR", TaxExclusive); err == nil {
		t.Fatal("ParseMoney accepted three fractional digits")
	}
}

func TestParseDateIsCivilAndNotAnInstant(t *testing.T) {
	date, err := ParseDate("2020-06-26")
	if err != nil {
		t.Fatalf("ParseDate: %v", err)
	}
	if date.Year != 2020 || date.Month != time.June || date.Day != 26 {
		t.Fatalf("date = %+v", date)
	}
	if date.String() != "2020-06-26" {
		t.Fatalf("String() = %s", date.String())
	}
	if _, err := ParseDate("2020-02-30"); err == nil {
		t.Fatal("ParseDate accepted a day that does not exist")
	}
}
