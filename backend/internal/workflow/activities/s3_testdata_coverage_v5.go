package activities

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	speccontract "github.com/Gingoo-TvT/Qraft/backend/internal/spec"
	"go.temporal.io/sdk/temporal"
)

const s3DerivedCoverageInstructionsV5 = `
DERIVED COVERAGE CONTRACT (all obligations are mandatory):
- Read every derived_case_plan.obligations entry before choosing cases or writing generator branches. Its hashes bind the accepted SemanticSpec and AuthoringPlan; do not invent, rename, omit or weaken obligations.
- The derived plan covers INPUT-scoped facts only, bound to actual input_grammar fields. The full semantic_spec intentionally retains OUTPUT and DERIVED symbols and constraints for the problem, programs and later verification. Do not print output symbols into generated input, turn output/derived range limits into input coverage obligations, or delete those facts from the accepted SemanticSpec. Unknown subjects or scope/grammar mismatches are errors, not permission to ignore input constraints.
- allowed_boundary requires an actual input to hit the specified subject, dimension and exact value, including upper bounds. range_minimum/range_maximum require the actual minimum/maximum of that structured constraint, not merely a small/large-looking example.
- constraint_region requires ONE complete input case to hit ALL boundary_refs together. Hitting one ref in one case and another ref in a different case does not cover that obligation. Multiple compatible region obligations may share a single case. Empty refs impose no additional numeric boundary but still require a valid complete case.
- Check the concrete SemanticSpec grammar, constraints and relations when combining boundaries. Every generated input must satisfy them exactly; never alter the problem or target difficulty to obtain coverage. Preserve all original input records, dependent counts and string-token rules.
- Plan a deterministic zero-based generator branch for every returned generated case, including maximum-value and joint-boundary cases. A description, coverage label, comment, random chance, or generator source alone is not evidence: the server reparses the actual generated input bytes.
- Cover all obligations within the requested fixed/adaptive case-count and group limits. Use test_intents and failure_modes for additional distinct adversarial cases. The server checks coverage before program verification and repeats the authoritative manifest checks after verification.
`

// This is an early rejection only, not a quality receipt. Final manifest
// verification still reparses the canonical CAS bytes and binds execution.
// Model-authored descriptions and generic coverage labels are never trusted.
func (a *Activities) validateS3GeneratedTestDataCoverageV5(ctx context.Context, spec domain.SemanticSpecV1, plan domain.AuthoringPlanV1, result TestDataResult) error {
	inputs := make([]string, len(result.TestCases))
	for i, item := range result.TestCases {
		switch {
		case item.InputArtifact == nil && item.Input != "" && item.InputRef == "":
			inputs[i] = item.Input
		case item.InputArtifact != nil && item.Input == "" && item.InputRef == "":
			raw, err := a.readS3ArtifactV1(ctx, *item.InputArtifact, item.InputArtifact.SHA256, item.InputArtifact.ContentType, maxS3ManifestBytesV1)
			if err != nil {
				return fmt.Errorf("read S3 coverage preflight case %q: %w", fmt.Sprintf("case-%06d", i), err)
			}
			inputs[i] = string(raw)
		default:
			return s3ContractErrorV1("case %q must contain exactly one durable or inline input", fmt.Sprintf("case-%06d", i))
		}
	}
	missing, err := missingS3GeneratedCoverageObligationsV5(spec, plan, inputs)
	if err != nil {
		return err
	}
	if len(missing) == 0 {
		return nil
	}
	// Preserve the durable original/corrected provider receipts in error details
	// as well as their provider-effect CAS records. Do not issue a fake PASS or
	// automatically spend another model call for incomplete semantic coverage.
	return temporal.NewNonRetryableApplicationError("S3 test-data coverage preflight failed: "+strings.Join(missing, "; "), "S3TestDataCoverageError", nil, result.SourceArtifacts)
}

func missingS3GeneratedCoverageObligationsV5(spec domain.SemanticSpecV1, plan domain.AuthoringPlanV1, inputs []string) ([]string, error) {
	derived, err := DeriveInputBoundaryCasePlanV3(spec, plan)
	if err != nil {
		return nil, s3QualityErrorV1("derive S3 coverage preflight obligations: %v", err)
	}
	inputScope, err := deriveInputBoundaryCoverageScopeV3(spec)
	if err != nil {
		return nil, s3QualityErrorV1("derive S3 input coverage scope: %v", err)
	}
	covered := make(map[string]bool, len(derived.Obligations))
	witnessCases := make([]ConstraintRegionCaseWitnessV2, 0, len(inputs))
	for index, raw := range inputs {
		id := fmt.Sprintf("case-%06d", index)
		parsed, err := speccontract.ParseTestCaseInputV2(spec, raw)
		if err != nil {
			return nil, s3QualityErrorV1("case %q does not satisfy SemanticSpec input: %v", id, err)
		}
		bindings := integerCoverageBindingMapV1(parsed.Bindings)
		refs := make([]string, 0)
		for _, boundary := range inputScope.Boundaries {
			if boundary.Allowed && integerBoundaryHitV1(boundary, bindings) {
				covered["allowed_boundary:"+boundary.ID] = true
				refs = append(refs, boundary.ID)
			}
		}
		sort.Strings(refs)
		for _, constraint := range inputScope.Constraints {
			for _, edge := range []struct {
				name  string
				value *int64
			}{{"min", constraint.Min}, {"max", constraint.Max}} {
				if edge.value != nil && integerConstraintEdgeHitV1(constraint, *edge.value, bindings) {
					covered[integerRangeObligationIDV1(constraint, edge.name)] = true
				}
			}
		}
		witnessCases = append(witnessCases, ConstraintRegionCaseWitnessV2{TestID: id, InputSHA256: parsed.CanonicalSHA256, BoundaryRefs: refs})
	}
	missing := make([]string, 0)
	for _, obligation := range derived.Obligations {
		if obligation.Kind == IntegerBoundaryObligationRegion || covered[obligation.ID] {
			continue
		}
		missing = append(missing, fmt.Sprintf("missing obligation %q (kind=%s, subject=%q, dimension=%q, value=%d, boundary_refs=%q)", obligation.ID, obligation.Kind, obligation.Subject, obligation.Dimension, *obligation.Value, obligation.BoundaryRefs))
	}
	if _, err := DeriveConstraintRegionWitnessesV2(*derived, witnessCases); err != nil {
		missing = append(missing, err.Error())
	}
	return missing, nil
}
