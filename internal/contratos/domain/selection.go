package domain

// Exclusion reasons are stable keys: the coverage page groups the units left
// out by them, so a reader can tell a procedure outside the tracked families
// from one the source publishes without a classification.
const (
	ExclusionFamilyNotSelected = "family_not_selected"
	ExclusionCPVMissing        = "cpv_missing"
	ExclusionCPVAmbiguous      = "cpv_ambiguous"
)

// Decision says whether one source revision enters the database and, when it
// does not, why. It is a pure reading of the classifications: no database and
// no inference are involved.
type Decision struct {
	Store  bool
	Reason string
}

// Includes says whether the classification selects its scope. The scope flag
// is authoritative; the code flags are accepted too so a caller that only
// filled the codes is read the same way.
func (c Classification) Includes() bool {
	if c.Included {
		return true
	}
	for _, code := range c.Codes {
		if code.Included {
			return true
		}
	}
	return false
}

// Admission decides the whole revision. The unit is the expediente and, when
// it publishes lots, each lot: an expediente with a selected lot enters even
// when its own classification does not, because that lot's spend belongs to a
// tracked family. A revision that selects nothing is excluded with its reason.
func (p Publication) Admission() Decision {
	if p.Expediente.CPV.Includes() {
		return Decision{Store: true}
	}
	for _, lot := range p.Lots {
		if lot.CPV.Includes() {
			return Decision{Store: true}
		}
	}
	return Decision{Reason: exclusionReason(p.Expediente.CPV)}
}

// IncludedLots returns the lots that enter. A lot without a classification of
// its own is never inferred here: the source adapter either published its
// codes or inherited them from an unambiguous expediente.
func (p Publication) IncludedLots() map[string]bool {
	included := make(map[string]bool, len(p.Lots))
	for _, lot := range p.Lots {
		if lot.CPV.Includes() {
			included[lot.ID] = true
		}
	}
	return included
}

func exclusionReason(c Classification) string {
	if len(c.Codes) > 0 {
		return ExclusionFamilyNotSelected
	}
	if c.Reason == ExclusionCPVAmbiguous {
		return ExclusionCPVAmbiguous
	}
	return ExclusionCPVMissing
}
