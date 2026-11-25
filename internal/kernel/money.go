// Package kernel holds the value objects shared by every capability.
package kernel

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// TaxBase says which side of the tax the amount publishes. A money value is
// never a bare number: its base and its currency travel with it.
type TaxBase string

const (
	TaxExclusive TaxBase = "tax_exclusive"
	TaxInclusive TaxBase = "tax_inclusive"
)

func (b TaxBase) Valid() bool {
	return b == TaxExclusive || b == TaxInclusive
}

// Money is an exact decimal amount in minor units, with its currency and the
// tax base it was published in. It is never a float64. Currency is empty when
// the source does not publish one: that is unknown, not EUR.
type Money struct {
	Cents    int64
	Currency string
	TaxBase  TaxBase
}

// ParseMoney reads the decimal string the source publishes. More than two
// fractional digits is an error: rounding money silently is not allowed.
func ParseMoney(raw, currency string, base TaxBase) (Money, error) {
	if !base.Valid() {
		return Money{}, fmt.Errorf("money %q: invalid tax base %q", raw, base)
	}

	text := strings.TrimSpace(raw)
	if text == "" {
		return Money{}, errors.New("money: empty amount")
	}
	if strings.HasPrefix(text, "-") || strings.HasPrefix(text, "+") {
		return Money{}, fmt.Errorf("money %q: a signed amount is not a published magnitude", raw)
	}

	whole, fraction, hasFraction := strings.Cut(text, ".")
	if whole == "" {
		whole = "0"
	}
	if !digits(whole) {
		return Money{}, fmt.Errorf("money %q: invalid amount", raw)
	}
	if hasFraction {
		if fraction == "" || !digits(fraction) {
			return Money{}, fmt.Errorf("money %q: invalid amount", raw)
		}
		if len(fraction) > 2 {
			return Money{}, fmt.Errorf("money %q: more than two fractional digits", raw)
		}
		for len(fraction) < 2 {
			fraction += "0"
		}
	} else {
		fraction = "00"
	}

	units, err := strconv.ParseInt(whole, 10, 64)
	if err != nil {
		return Money{}, fmt.Errorf("money %q: %w", raw, err)
	}
	cents, err := strconv.ParseInt(fraction, 10, 64)
	if err != nil {
		return Money{}, fmt.Errorf("money %q: %w", raw, err)
	}
	if units > (1<<63-1-cents)/100 {
		return Money{}, fmt.Errorf("money %q: amount out of range", raw)
	}

	if currency != "" {
		if len(currency) != 3 || strings.ToUpper(currency) != currency || !letters(currency) {
			return Money{}, fmt.Errorf("money %q: invalid currency %q", raw, currency)
		}
	}

	return Money{Cents: units*100 + cents, Currency: currency, TaxBase: base}, nil
}

func digits(value string) bool {
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return value != ""
}

func letters(value string) bool {
	for _, r := range value {
		if r < 'A' || r > 'Z' {
			return false
		}
	}
	return value != ""
}

// String returns the exact decimal with two fractional digits.
func (m Money) String() string {
	sign := ""
	cents := m.Cents
	if cents < 0 {
		sign = "-"
		cents = -cents
	}
	return fmt.Sprintf("%s%d.%02d", sign, cents/100, cents%100)
}

func (m Money) IsZero() bool { return m.Cents == 0 }

type moneyJSON struct {
	Amount   string  `json:"amount"`
	Currency string  `json:"currency,omitempty"`
	TaxBase  TaxBase `json:"tax_base"`
}

func (m Money) MarshalJSON() ([]byte, error) {
	return json.Marshal(moneyJSON{Amount: m.String(), Currency: m.Currency, TaxBase: m.TaxBase})
}

func (m *Money) UnmarshalJSON(data []byte) error {
	var raw moneyJSON
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	parsed, err := ParseMoney(raw.Amount, raw.Currency, raw.TaxBase)
	if err != nil {
		return err
	}
	*m = parsed
	return nil
}
