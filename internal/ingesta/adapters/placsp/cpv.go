package placsp

import (
	"strings"

	"github.com/hectorruiz23/la-mordida/internal/ingesta/domain"
)

// selectedFamilies are the CPV families this project follows: 302 (computer
// equipment), 48 (software packages) and 72 (IT services). The prefix is
// matched against the leading digits of the published code; the original code
// is kept beside it. Everything else is kept with Included false: excluded is
// not dropped.
var selectedFamilies = []string{"302", "48", "72"}

// classifyPublished selects the published codes of one scope. The level used
// for the selection is the family prefix, not the whole code, and the code
// itself stays untouched.
func classifyPublished(codes []string) domain.Classification {
	if len(codes) == 0 {
		return domain.Classification{
			Source:    domain.ClassificationUnknown,
			Ambiguous: true,
			Reason:    "cpv_missing",
		}
	}

	classification := domain.Classification{Source: domain.ClassificationPublished}
	for _, code := range codes {
		prefix, ok := familyOf(code)
		reason := "family_not_selected"
		if ok {
			reason = "family_selected"
		}
		classification.Codes = append(classification.Codes, domain.CPV{
			Code:     code,
			Family:   prefix,
			Included: ok,
			Reason:   reason,
		})
	}
	return classification
}

// inheritForLot fills a lot that publishes no classification of its own. It
// only infers when the expediente's published codes all fall in one family:
// with codes from several families, or from a selected and an excluded one,
// the lot's family is unknown and no spend may be attributed to any of them.
func inheritForLot(expediente domain.Classification) domain.Classification {
	if expediente.Source != domain.ClassificationPublished || len(expediente.Codes) == 0 {
		return domain.Classification{
			Source:    domain.ClassificationUnknown,
			Ambiguous: true,
			Reason:    "cpv_missing",
		}
	}

	families := make(map[string]bool, len(expediente.Codes))
	for _, code := range expediente.Codes {
		families[code.Family] = true
	}
	if len(families) != 1 {
		return domain.Classification{
			Source:    domain.ClassificationUnknown,
			Ambiguous: true,
			Reason:    "cpv_ambiguous",
		}
	}

	inherited := domain.Classification{
		Source: domain.ClassificationInherited,
		Reason: "cpv_inherited",
		Codes:  make([]domain.CPV, 0, len(expediente.Codes)),
	}
	for _, code := range expediente.Codes {
		code.Reason = "family_inherited"
		inherited.Codes = append(inherited.Codes, code)
	}
	return inherited
}

// familyOf returns the selected family prefix of a code. It reads the leading
// digits, so a published check digit or an attached "-N" does not affect the
// match.
func familyOf(code string) (string, bool) {
	digits := leadingDigits(code)
	for _, prefix := range selectedFamilies {
		if strings.HasPrefix(digits, prefix) {
			return prefix, true
		}
	}
	return "", false
}

func leadingDigits(code string) string {
	code = strings.TrimSpace(code)
	for index, character := range code {
		if character < '0' || character > '9' {
			return code[:index]
		}
	}
	return code
}
