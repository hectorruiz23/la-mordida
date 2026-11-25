package placsp

import (
	"testing"

	"github.com/hectorruiz23/la-mordida/internal/ingesta/domain"
)

func TestSelectedFamiliesAndExclusions(t *testing.T) {
	cases := []struct {
		code     string
		family   string
		included bool
	}{
		{code: "30237475", family: "302", included: true},
		{code: "30200000-1", family: "302", included: true},
		{code: "48000000", family: "48", included: true},
		{code: "48219700", family: "48", included: true},
		{code: "72000000", family: "72", included: true},
		{code: "72500000", family: "72", included: true},
		{code: "33700000", included: false},
		{code: "39800000", included: false},
		{code: "55511000", included: false},
		{code: "80000000", included: false},
		{code: "not-a-code", included: false},
	}

	for _, test := range cases {
		t.Run(test.code, func(t *testing.T) {
			classification := classifyPublished([]string{test.code})
			if classification.Source != domain.ClassificationPublished {
				t.Fatalf("source = %q", classification.Source)
			}
			if len(classification.Codes) != 1 {
				t.Fatalf("codes = %+v", classification.Codes)
			}
			code := classification.Codes[0]
			if code.Code != test.code {
				t.Errorf("original code = %q, want it preserved", code.Code)
			}
			if code.Family != test.family {
				t.Errorf("family = %q, want %q", code.Family, test.family)
			}
			if code.Included != test.included {
				t.Errorf("included = %v, want %v", code.Included, test.included)
			}
			if classification.Included != test.included {
				t.Errorf("scope included = %v, want %v", classification.Included, test.included)
			}
			wantReason := "family_not_selected"
			if test.included {
				wantReason = "family_selected"
			}
			if code.Reason != wantReason {
				t.Errorf("reason = %q, want %q", code.Reason, wantReason)
			}
		})
	}
}

func TestAScopeEntersWhenAnyOfItsCodesIsSelected(t *testing.T) {
	classification := classifyPublished([]string{"45300000", "72200000"})
	if !classification.Included {
		t.Fatal("a scope with one selected code did not enter")
	}
	if len(classification.Codes) != 2 || classification.Codes[0].Included || !classification.Codes[1].Included {
		t.Fatalf("codes = %+v, want the selected one to mark the scope", classification.Codes)
	}
}

func TestMissingCPVIsUnknownNotAFamily(t *testing.T) {
	classification := classifyPublished(nil)
	if classification.Source != domain.ClassificationUnknown || classification.Reason != "cpv_missing" || !classification.Ambiguous {
		t.Fatalf("classification = %+v", classification)
	}
	if len(classification.Codes) != 0 {
		t.Fatalf("codes = %+v, want none", classification.Codes)
	}
}

func TestLotInheritsOnlyAnUnambiguousExpediente(t *testing.T) {
	tests := []struct {
		name          string
		expediente    domain.Classification
		wantSource    string
		wantReason    string
		wantAmbiguous bool
		wantFamily    string
	}{
		{
			name:       "single family",
			expediente: classifyPublished([]string{"72200000"}),
			wantSource: domain.ClassificationInherited,
			wantReason: "cpv_inherited",
			wantFamily: "72",
		},
		{
			name:       "single excluded family",
			expediente: classifyPublished([]string{"33700000"}),
			wantSource: domain.ClassificationInherited,
			wantReason: "cpv_inherited",
		},
		{
			name:          "selected and excluded",
			expediente:    classifyPublished([]string{"72200000", "33700000"}),
			wantSource:    domain.ClassificationUnknown,
			wantReason:    "cpv_ambiguous",
			wantAmbiguous: true,
		},
		{
			name:          "two selected families",
			expediente:    classifyPublished([]string{"72200000", "48000000"}),
			wantSource:    domain.ClassificationUnknown,
			wantReason:    "cpv_ambiguous",
			wantAmbiguous: true,
		},
		{
			name:          "no expediente classification",
			expediente:    classifyPublished(nil),
			wantSource:    domain.ClassificationUnknown,
			wantReason:    "cpv_missing",
			wantAmbiguous: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			inherited := inheritForLot(test.expediente)
			if inherited.Source != test.wantSource || inherited.Reason != test.wantReason {
				t.Errorf("classification = %+v, want source %q reason %q", inherited, test.wantSource, test.wantReason)
			}
			if inherited.Ambiguous != test.wantAmbiguous {
				t.Errorf("ambiguous = %v, want %v", inherited.Ambiguous, test.wantAmbiguous)
			}
			wantIncluded := test.wantFamily != ""
			if inherited.Included != wantIncluded {
				t.Errorf("included = %v, want %v", inherited.Included, wantIncluded)
			}
			if test.wantFamily != "" {
				if len(inherited.Codes) != 1 || inherited.Codes[0].Family != test.wantFamily {
					t.Errorf("codes = %+v, want family %s", inherited.Codes, test.wantFamily)
				}
				if inherited.Codes[0].Reason != "family_inherited" {
					t.Errorf("code reason = %q", inherited.Codes[0].Reason)
				}
			}
			if test.wantAmbiguous && len(inherited.Codes) != 0 {
				t.Errorf("ambiguous lot carries codes %+v", inherited.Codes)
			}
		})
	}
}
