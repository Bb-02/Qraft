package activities

import (
	"fmt"
	"sort"
	"strings"
)

const (
	ConstraintRegionAssociationRuleVersionV4               = "algoforge.constraint-region-association.rules.v4"
	RegionEvidenceExplicitAssociationV4                    = "explicit_case_association_with_boundary_checks"
	FullCaseIntegerExplicitRegionCoverageRuleVersionV5     = "algoforge.integer-boundary-coverage.fullcase.input-scope.explicit-region.rules.v5"
	FullCaseTokenStringExplicitRegionCoverageRuleVersionV5 = "algoforge.token-string-boundary-coverage.fullcase.input-scope.explicit-region.rules.v5"
)

type ConstraintRegionCaseAssociationV4 struct {
	ConstraintRegionCaseWitnessV2
	RegionObligationIDs []string
}

func s3RegionObligationIDsV6(plan IntegerBoundaryCasePlanV1, indexes []int) ([]string, error) {
	ids := make([]string, 0, len(indexes))
	for i, index := range indexes {
		if index < 0 || index >= len(plan.Obligations) || (i > 0 && indexes[i-1] >= index) {
			return nil, fmt.Errorf("region_obligation_indexes must be sorted unique indexes within the bound derived plan")
		}
		ob := plan.Obligations[index]
		if ob.Kind != IntegerBoundaryObligationRegion {
			return nil, fmt.Errorf("region_obligation_indexes[%d]=%d does not reference a constraint_region obligation", i, index)
		}
		ids = append(ids, ob.ID)
	}
	return ids, nil
}

// This proves association integrity and its boundary prerequisites ONLY.
// A natural-language region is a model-authored claim, not a typed predicate.
// Equal boundary refs never authorize attaching a different region to a case.
func DeriveConstraintRegionAssociationsV4(plan IntegerBoundaryCasePlanV1, cases []ConstraintRegionCaseAssociationV4) ([]ConstraintRegionWitnessV2, error) {
	if plan.RuleVersion != IntegerInputBoundaryCasePlanRuleVersionV3 && plan.RuleVersion != TokenStringInputBoundaryCasePlanRuleVersionV3 {
		return nil, fmt.Errorf("explicit region associations require the authoritative input-scoped derived plan")
	}
	if _, _, err := CanonicalIntegerBoundaryCasePlanV1(plan, plan.RuleVersion); err != nil {
		return nil, err
	}
	regions := map[string]IntegerBoundaryCaseObligationV1{}
	allowed := map[string]bool{}
	for _, ob := range plan.Obligations {
		if ob.Kind == IntegerBoundaryObligationRegion {
			regions[ob.ID] = ob
		}
		if ob.Kind == IntegerBoundaryObligationBoundary {
			allowed[ob.BoundaryRefs[0]] = true
		}
	}
	previous := ""
	for _, c := range cases {
		if c.TestID == "" || c.TestID <= previous || !isManifestSHA256(c.InputSHA256) || c.BoundaryRefs == nil || !sortedUniqueCanonicalStringsV2(c.BoundaryRefs) || !sortedUniqueCanonicalStringsV2(c.RegionObligationIDs) {
			return nil, fmt.Errorf("explicit region association cases must be canonical sorted unique")
		}
		previous = c.TestID
		for _, ref := range c.BoundaryRefs {
			if !allowed[ref] {
				return nil, fmt.Errorf("case %q references unknown or forbidden boundary %q", c.TestID, ref)
			}
		}
		for _, id := range c.RegionObligationIDs {
			ob, ok := regions[id]
			if !ok {
				return nil, fmt.Errorf("case %q associates unknown region obligation %q", c.TestID, id)
			}
			for _, ref := range ob.BoundaryRefs {
				at := sort.SearchStrings(c.BoundaryRefs, ref)
				if at == len(c.BoundaryRefs) || c.BoundaryRefs[at] != ref {
					return nil, fmt.Errorf("case %q explicitly associates region obligation %q but its actual input does not hit boundary %q", c.TestID, id, ref)
				}
			}
		}
	}
	witnesses := make([]ConstraintRegionWitnessV2, 0, len(regions))
	for _, ob := range plan.Obligations {
		if ob.Kind != IntegerBoundaryObligationRegion {
			continue
		}
		found := false
		for _, c := range cases {
			at := sort.SearchStrings(c.RegionObligationIDs, ob.ID)
			if at < len(c.RegionObligationIDs) && c.RegionObligationIDs[at] == ob.ID {
				witnesses = append(witnesses, ConstraintRegionWitnessV2{ObligationID: ob.ID, ConstraintRegion: ob.ConstraintRegion, TestID: c.TestID, InputSHA256: c.InputSHA256, BoundaryRefs: copyPresentS3StringsV1(ob.BoundaryRefs)})
				found = true
				break
			}
		}
		if !found {
			return nil, fmt.Errorf("derived constraint-region obligation %q has no explicitly associated matching case (region=%q, boundary_refs=%v)", ob.ID, ob.ConstraintRegion, ob.BoundaryRefs)
		}
	}
	if len(witnesses) == 0 {
		return nil, fmt.Errorf("explicit region plan has no region obligations")
	}
	return witnesses, nil
}

func validateExplicitManifestRegionAssociationsV4(manifest TestManifestV2, final bool) error {
	if manifest.ConstraintRegionRuleVersion != ConstraintRegionAssociationRuleVersionV4 {
		for _, c := range manifest.Cases {
			if len(c.RegionObligationIDs) > 0 {
				return fmt.Errorf("legacy manifest cannot claim explicit region associations")
			}
		}
		return nil
	}
	byID := map[string]TestManifestCaseV2{}
	knownObligations := map[string]bool{}
	for _, w := range manifest.RegionWitnesses {
		knownObligations[w.ObligationID] = true
	}
	for _, c := range manifest.Cases {
		if !sortedUniqueCanonicalStringsV2(c.RegionObligationIDs) {
			return fmt.Errorf("case region_obligation_ids must be sorted unique")
		}
		for _, id := range c.RegionObligationIDs {
			if !strings.HasPrefix(id, "constraint_region:") || !isManifestSHA256(strings.TrimPrefix(id, "constraint_region:")) {
				return fmt.Errorf("case %q has an invalid explicit region obligation ID", c.TestID)
			}
			// A pre-manifest has not derived its full association receipt yet.
			// Final readers must reject orphan claims, including on nonselected cases.
			if (final || len(manifest.RegionWitnesses) != 0) && !knownObligations[id] {
				return fmt.Errorf("case %q associates unknown region obligation %q absent from complete region witnesses", c.TestID, id)
			}
		}
		byID[c.TestID] = c
	}
	for _, w := range manifest.RegionWitnesses {
		c, ok := byID[w.TestID]
		if !ok {
			return fmt.Errorf("explicit region witness case is absent")
		}
		at := sort.SearchStrings(c.RegionObligationIDs, w.ObligationID)
		if at == len(c.RegionObligationIDs) || c.RegionObligationIDs[at] != w.ObligationID {
			return fmt.Errorf("region witness lacks the case's explicit association")
		}
	}
	return nil
}
