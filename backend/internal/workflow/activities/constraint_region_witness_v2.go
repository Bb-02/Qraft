package activities

import (
	"fmt"
	"sort"
	"strings"
)

const (
	ConstraintRegionMatchingRuleVersionV2 = "algoforge.constraint-region-matching.rules.v2"
	ConstraintRegionMatchingRuleVersionV3 = "algoforge.constraint-region-matching.rules.v3"
)

// ConstraintRegionCaseWitnessV2 contains only hits already proved by parsing
// the exact CAS bytes against the authoritative SemanticSpec.
type ConstraintRegionCaseWitnessV2 struct {
	TestID       string
	InputSHA256  string
	BoundaryRefs []string
}

type ConstraintRegionWitnessV2 struct {
	ObligationID     string   `json:"obligation_id"`
	ConstraintRegion string   `json:"constraint_region"`
	TestID           string   `json:"test_id"`
	InputSHA256      string   `json:"input_sha256"`
	BoundaryRefs     []string `json:"boundary_refs"`
}

type ConstraintRegionCoverageErrorV2 struct {
	ObligationID     string
	ConstraintRegion string
	BoundaryRefs     []string
}

func (err *ConstraintRegionCoverageErrorV2) Error() string {
	return fmt.Sprintf("derived constraint-region obligation %q has no single matching case (region=%q, boundary_refs=%v)", err.ObligationID, err.ConstraintRegion, err.BoundaryRefs)
}

// DeriveConstraintRegionWitnessesV2 preserves the same-case conjunction while
// allowing one actual input to witness several overlapping test intents. A
// primary ConstraintRegion label is only a scoring group, not an exclusive
// partition of the input domain. The earliest matching test ID is selected so
// witness bytes are stable and do not depend on map iteration.
func DeriveConstraintRegionWitnessesV2(plan IntegerBoundaryCasePlanV1, cases []ConstraintRegionCaseWitnessV2) ([]ConstraintRegionWitnessV2, error) {
	if plan.RuleVersion != IntegerBoundaryCoverageRuleVersion && plan.RuleVersion != TokenStringBoundaryCoverageRuleVersionV2 && plan.RuleVersion != IntegerInputBoundaryCasePlanRuleVersionV3 && plan.RuleVersion != TokenStringInputBoundaryCasePlanRuleVersionV3 {
		return nil, fmt.Errorf("constraint-region witnesses require the authoritative derived-plan rule")
	}
	if _, _, err := CanonicalIntegerBoundaryCasePlanV1(plan, plan.RuleVersion); err != nil {
		return nil, fmt.Errorf("invalid constraint-region witness plan: %w", err)
	}
	allowed := make(map[string]bool)
	for _, obligation := range plan.Obligations {
		if obligation.Kind == IntegerBoundaryObligationBoundary {
			allowed[obligation.BoundaryRefs[0]] = true
		}
	}
	previous := ""
	for _, candidate := range cases {
		if candidate.TestID == "" || candidate.TestID <= previous || !sortedUniqueCanonicalStringsV2([]string{candidate.TestID}) || !isManifestSHA256(candidate.InputSHA256) || candidate.BoundaryRefs == nil || !sortedUniqueCanonicalStringsV2(candidate.BoundaryRefs) {
			return nil, fmt.Errorf("constraint-region witness cases must be canonical, sorted and unique")
		}
		previous = candidate.TestID
		for _, ref := range candidate.BoundaryRefs {
			if !allowed[ref] {
				return nil, fmt.Errorf("constraint-region witness references unknown or forbidden boundary %q", ref)
			}
		}
	}
	witnesses := make([]ConstraintRegionWitnessV2, 0)
	for _, obligation := range plan.Obligations {
		if obligation.Kind != IntegerBoundaryObligationRegion {
			continue
		}
		found := false
		for _, candidate := range cases {
			matched := true
			for _, ref := range obligation.BoundaryRefs {
				index := sort.SearchStrings(candidate.BoundaryRefs, ref)
				if index == len(candidate.BoundaryRefs) || candidate.BoundaryRefs[index] != ref {
					matched = false
					break
				}
			}
			if matched {
				witnesses = append(witnesses, ConstraintRegionWitnessV2{
					ObligationID: obligation.ID, ConstraintRegion: obligation.ConstraintRegion,
					TestID: candidate.TestID, InputSHA256: candidate.InputSHA256,
					BoundaryRefs: copyPresentS3StringsV1(obligation.BoundaryRefs),
				})
				found = true
				break
			}
		}
		if !found {
			return nil, &ConstraintRegionCoverageErrorV2{ObligationID: obligation.ID, ConstraintRegion: obligation.ConstraintRegion, BoundaryRefs: copyPresentS3StringsV1(obligation.BoundaryRefs)}
		}
	}
	if len(witnesses) == 0 {
		return nil, fmt.Errorf("constraint-region witness plan has no region obligations")
	}
	return witnesses, nil
}

func validateConstraintRegionWitnessesV2(witnesses []ConstraintRegionWitnessV2) error {
	if len(witnesses) == 0 {
		return fmt.Errorf("multi-region coverage omits its witnesses")
	}
	previous := ""
	for _, witness := range witnesses {
		if witness.ObligationID <= previous || !strings.HasPrefix(witness.ObligationID, "constraint_region:") || !isManifestSHA256(strings.TrimPrefix(witness.ObligationID, "constraint_region:")) || !sortedUniqueCanonicalStringsV2([]string{witness.ConstraintRegion}) || witness.TestID == "" || !sortedUniqueCanonicalStringsV2([]string{witness.TestID}) || !isManifestSHA256(witness.InputSHA256) || witness.BoundaryRefs == nil || !sortedUniqueCanonicalStringsV2(witness.BoundaryRefs) {
			return fmt.Errorf("multi-region coverage witness is not canonical, sorted and unique")
		}
		previous = witness.ObligationID
	}
	return nil
}

func validateManifestConstraintRegionWitnessesV2(manifest TestManifestV2, final bool) error {
	if manifest.ConstraintRegionRuleVersion == "" {
		if len(manifest.RegionWitnesses) != 0 {
			return fmt.Errorf("legacy TestManifest cannot claim multi-region witnesses")
		}
		return nil
	}
	if !final && len(manifest.RegionWitnesses) == 0 {
		return nil // D has not derived the pre-manifest's proof yet.
	}
	if err := validateConstraintRegionWitnessesV2(manifest.RegionWitnesses); err != nil {
		return err
	}
	byID := make(map[string]TestManifestCaseV2, len(manifest.Cases))
	for _, item := range manifest.Cases {
		byID[item.TestID] = item
	}
	for _, witness := range manifest.RegionWitnesses {
		item, exists := byID[witness.TestID]
		if !exists || item.InputSHA256 != witness.InputSHA256 {
			return fmt.Errorf("constraint-region witness does not bind the same manifest input case")
		}
		for _, ref := range witness.BoundaryRefs {
			index := sort.SearchStrings(item.BoundaryRefs, ref)
			if index == len(item.BoundaryRefs) || item.BoundaryRefs[index] != ref {
				return fmt.Errorf("constraint-region witness combines boundary refs outside one manifest case")
			}
		}
	}
	return nil
}
