package activities

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"github.com/Gingoo-TvT/Qraft/backend/internal/llm"
	remotesandbox "github.com/Gingoo-TvT/Qraft/backend/internal/sandbox"
	speccontract "github.com/Gingoo-TvT/Qraft/backend/internal/spec"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"
)

func TestS3CoverageV5ChecksActualInputEdgesAndSameCaseCombinations(t *testing.T) {
	spec, plan, _, _ := validIntegerCoverageFixtureV1(t)
	plan.TestIntents = append(plan.TestIntents, domain.AuthoringTestIntentV1{Purpose: "maximum with zero", ConstraintRegion: "combined-max-zero", BoundaryRefs: []string{"a-element-max", "a-element-zero", "n-max"}})
	for _, test := range []struct {
		name   string
		inputs []string
		want   string
	}{
		{"complete", []string{"0\n\n", "1\n0\n", "2\n0 9\n"}, ""},
		{"canonicalizable is still real", []string{"0\r\n\r\n", "1\n00\n", "02\n0     +9"}, ""},
		{"upper boundary missing", []string{"0\n\n", "1\n0\n", "2\n0 8\n"}, `missing obligation "allowed_boundary:a-element-max" (kind=allowed_boundary, subject="a", dimension="element_value", value=9`},
		{"separate cases do not cover conjunction", []string{"0\n\n", "1\n0\n", "2\n9 9\n"}, "combined-max-zero"},
	} {
		t.Run(test.name, func(t *testing.T) {
			missing, err := missingS3GeneratedCoverageObligationsV5(spec, plan, test.inputs)
			require.NoError(t, err)
			if test.want == "" {
				require.Empty(t, missing)
			} else {
				require.Contains(t, strings.Join(missing, "; "), test.want)
			}
		})
	}
	_, err := missingS3GeneratedCoverageObligationsV5(spec, plan, []string{"2\n0 10\n"})
	require.ErrorContains(t, err, "does not satisfy SemanticSpec input")
}

func TestS3CoverageV5ReadsRealCASAndNeverTrustsDescriptionOrCoverage(t *testing.T) {
	spec, plan, _, _ := validIntegerCoverageFixtureV1(t)
	store := newOracleMemoryArtifactStore()
	acts := &Activities{artifacts: store}
	result := TestDataResult{PayloadVersion: ActivityPayloadVersion}
	for _, raw := range []string{"0\n\n", "1\n0\n", "2\n0 8\n"} {
		ref := s3BatchSeedArtifactV1(t, store, []byte(raw), "text/plain", "GenerateS3TestDataActivityV1")
		result.TestCases = append(result.TestCases, TestCaseData{InputArtifact: &ref, Description: "maximum 9 and all boundaries", Coverage: []string{coverageMaximum}})
	}
	err := acts.validateS3GeneratedTestDataCoverageV5(context.Background(), spec, plan, result)
	var failure *temporal.ApplicationError
	require.ErrorAs(t, err, &failure)
	require.Equal(t, "S3TestDataCoverageError", failure.Type())
	require.True(t, failure.NonRetryable())
	require.Contains(t, failure.Error(), "allowed_boundary:a-element-max")
}

func runS3CoverageOutputV5(t *testing.T, acts *Activities) (*TestDataResult, error) {
	t.Helper()
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestActivityEnvironment()
	fn := func(ctx context.Context) (*TestDataResult, error) {
		spec, plan, _, _ := validIntegerCoverageFixtureV1(t)
		params, err := enrichS3TestDataParamsV1(domain.ProblemGenParams{}, spec, plan)
		if err != nil {
			return nil, err
		}
		result, err := acts.generateTestDataActivity(ctx, "synthetic integer sequence", domain.TestDataConfig{NumTestCases: 3, NumSamples: 1}, params, true)
		if err != nil {
			return nil, err
		}
		return result, acts.validateS3GeneratedTestDataCoverageV5(ctx, spec, plan, *result)
	}
	env.RegisterActivity(fn)
	value, err := env.ExecuteActivity(fn)
	if err != nil {
		return nil, err
	}
	var result TestDataResult
	return &result, value.Get(&result)
}

const s3CoverageFixtureJSONV5 = `{"test_case_count":3,"test_cases":[{"input":"0\n\n","is_sample":true},{"input":"1\n0\n"},{"input":"2\n0 9\n"}],"generator_code":""}`

func TestS3CoverageV5PromptEffectVersionAndOneFormatRepairKeepProvenance(t *testing.T) {
	for _, first := range []string{s3CoverageFixtureJSONV5, "not JSON"} {
		model := &authoringFormatLLM{responses: []*llm.Response{authoringLLMResponse(first), authoringLLMResponse(s3CoverageFixtureJSONV5)}}
		acts, _, _ := authoringFormatActivities(model)
		cache := &authoringFormatEffectStore{requests: map[string]string{}, results: map[string]json.RawMessage{}}
		acts.deps.ProviderEffects, acts.deps.ProviderEffectLease = cache, time.Minute
		result, err := runS3CoverageOutputV5(t, acts)
		require.NoError(t, err)
		wantCalls := 1
		if first == "not JSON" {
			wantCalls = 2
		}
		require.Len(t, model.requests, wantCalls)
		require.Len(t, result.SourceArtifacts, wantCalls)
		for i, request := range model.requests {
			require.Equal(t, "v5", request.PromptVersion)
			require.Contains(t, request.Messages[0].Content, `"derived_case_plan"`)
			require.Contains(t, request.Messages[0].Content, "ONE complete input case to hit ALL boundary_refs together")
			require.Equal(t, request.PromptID, result.SourceArtifacts[i].LLMCallReceipt.PromptID)
		}
		require.Equal(t, s3TestDataPromptIDV5, model.requests[0].PromptID)
		if wantCalls == 2 {
			require.Equal(t, s3TestDataRepairPromptIDV5, model.requests[1].PromptID)
		}
		_, err = runS3CoverageOutputV5(t, acts)
		require.NoError(t, err)
		require.Len(t, model.requests, wantCalls, "redelivery reuses all cached effects")
	}
}

func TestS3CoverageV5MissingCoverageDoesNotSpendRepairCalls(t *testing.T) {
	model := &authoringFormatLLM{responses: []*llm.Response{authoringLLMResponse(strings.ReplaceAll(s3CoverageFixtureJSONV5, "0 9", "0 8"))}}
	acts, store, _ := authoringFormatActivities(model)
	_, err := runS3CoverageOutputV5(t, acts)
	var failure *temporal.ApplicationError
	require.True(t, errors.As(err, &failure))
	require.Equal(t, "S3TestDataCoverageError", failure.Type())
	require.Len(t, model.requests, 1, "semantic coverage is not a format-repair retry")
	var sources []*ArtifactRef
	require.NoError(t, failure.Details(&sources))
	require.Len(t, sources, 1)
	_, err = store.Get(context.Background(), *sources[0])
	require.NoError(t, err, "failed preflight retains its real provider artifact")
}

func TestGenerateS3TestDataV5BoundAdapterChecksExecutedGeneratorInputs(t *testing.T) {
	for _, complete := range []bool{true, false} {
		t.Run(fmt.Sprintf("complete=%t", complete), func(t *testing.T) {
			samples := []string{"0\n\n", "1\na\n", "2\nbb c\n", "3\nd ee fff\n"}
			fixture := tokenStringAuthoringFixtureV2(t, samples)
			draftRef := seedSampleClosureStatementDraft(t, fixture)
			output := `{"test_case_count":4,"test_cases":[{"input":"0\n\n"},{"input":""},{"input":""},{"input":""}],"generator_code":"int main(){}"}`
			model := &authoringFormatLLM{responses: []*llm.Response{authoringLLMResponse(output)}}
			acts := presentationActivitiesV2(model, fixture.statementDraftFixture)
			generatorCalls := 0
			acts.deps.RemoteSandboxFactory = func(time.Duration) (remotesandbox.RemoteExecutor, error) {
				return &fakeRemoteSandbox{executeFunc: func(_ context.Context, language, source string, inputs []string, limits remotesandbox.RemoteLimits) (*remotesandbox.RemoteExecuteResult, error) {
					generatorCalls++
					require.Equal(t, "cpp", language)
					require.Equal(t, "int main(){}", source)
					result := batchSandboxResponseV1(inputs, limits, fmt.Sprintf("synthetic-generator-%d", generatorCalls))
					for i, stdin := range inputs {
						var index, group int
						var seed int64
						_, err := fmt.Sscan(stdin, &index, &group, &seed)
						require.NoError(t, err)
						require.GreaterOrEqual(t, index, 1, "inline position zero is retained")
						result.Results[i].Stdout = samples[index]
						if !complete && index == 3 {
							result.Results[i].Stdout = samples[2]
						}
					}
					return result, nil
				}}, nil
			}
			var suite testsuite.WorkflowTestSuite
			env := suite.NewTestActivityEnvironment()
			env.RegisterActivity(acts.GenerateS3TestDataActivityV1)
			value, err := env.ExecuteActivity(acts.GenerateS3TestDataActivityV1, GenerateS3TestDataInputV1{
				PayloadVersion: S3QualityPayloadVersionV1, AuthoringBundleArtifact: fixture.statementDraftFixture.input.BundleArtifact,
				ExpectedAuthoringBundleSHA: fixture.statementDraftFixture.input.ExpectedBundleSHA256,
				StatementDraftArtifact:     draftRef, ExpectedStatementDraftSHA: draftRef.SHA256,
				ExpectedSemanticSpecSHA: fixture.input.ExpectedSemanticSpecSHA256,
				Config:                  domain.TestDataConfig{NumTestCases: 4, NumSamples: 1},
			})
			require.Equal(t, 3, generatorCalls, "only actual test generators run; no program verification or model coverage repair")
			require.Len(t, model.requests, 1)
			require.Equal(t, s3TestDataPromptIDV5, model.requests[0].PromptID)
			if !complete {
				var failure *temporal.ApplicationError
				require.ErrorAs(t, err, &failure)
				require.Equal(t, "S3TestDataCoverageError", failure.Type())
				require.Contains(t, failure.Error(), "allowed_boundary:n-3")
				return
			}
			require.NoError(t, err)
			var result TestDataResult
			require.NoError(t, value.Get(&result))
			require.Len(t, result.GeneratorBatches, 3)
			require.Len(t, result.TestCases, 4)
			require.Equal(t, samples[3], result.TestCases[3].Input)
			require.Len(t, result.SourceArtifacts, 1)
		})
	}
}

func s3OutputConstraintFixtureV5(t *testing.T) (domain.SemanticSpecV1, domain.AuthoringPlanV1, []string) {
	t.Helper()
	spec, plan, _, cases := validIntegerCoverageFixtureV1(t)
	two, three, zero, eighteen := int64(2), int64(3), int64(0), int64(18)
	spec.Symbols = append(spec.Symbols,
		domain.SemanticSymbolV1{Name: "answer", Type: domain.SemanticSymbolString, Scope: domain.SemanticSymbolScopeOutput, BoundaryPolicy: domain.SemanticBoundaryPolicyNotApplicable},
		domain.SemanticSymbolV1{Name: "total", Type: domain.SemanticSymbolInteger, Scope: domain.SemanticSymbolScopeDerived, BoundaryPolicy: domain.SemanticBoundaryPolicyNotApplicable},
	)
	spec.Constraints = append(spec.Constraints,
		domain.SemanticConstraintV1{Subject: "answer", Kind: domain.SemanticConstraintLengthRange, Min: &two, Max: &three, Meaning: "the answer is YES or NO"},
		domain.SemanticConstraintV1{Subject: "total", Kind: domain.SemanticConstraintIntegerRange, Min: &zero, Max: &eighteen, Meaning: "derived total"},
	)
	spec.Boundaries = append(spec.Boundaries,
		domain.SemanticBoundaryV1{ID: "answer-length-two", Symbol: "answer", Measure: domain.SemanticBoundaryMeasureLength, Value: 2, Allowed: true, Meaning: "NO output length"},
		domain.SemanticBoundaryV1{ID: "total-max", Symbol: "total", Measure: domain.SemanticBoundaryMeasureValue, Value: 18, Allowed: true, Meaning: "derived maximum total"},
	)
	one := int64(1)
	spec.OutputGrammar = domain.SemanticGrammarV1{Profile: domain.SemanticGrammarTokenLinesV1, Lines: []domain.SemanticGrammarLineV1{{ID: "answer-line", Repeat: domain.SemanticExpressionV1{Kind: domain.SemanticExpressionInteger, Value: &one}, Fields: []domain.SemanticGrammarFieldV1{{Symbol: "answer", Mode: domain.SemanticGrammarFieldScalar}}}}}
	plan.SemanticSpecSHA256 = speccontract.SemanticSpecSHA256V1(spec)
	inputs := make([]string, len(cases))
	for i, item := range cases {
		inputs[i] = item.Input
	}
	return spec, plan, inputs
}

func TestS3CoverageV5PreservesFullSpecButOnlyRequiresRealInputObligations(t *testing.T) {
	spec, plan, inputs := s3OutputConstraintFixtureV5(t)
	before, err := json.Marshal(spec)
	require.NoError(t, err)
	oldPlan, err := DeriveIntegerBoundaryCasePlanV1(spec, plan)
	require.NoError(t, err)
	var oldOutputRange bool
	for _, obligation := range oldPlan.Obligations {
		oldOutputRange = oldOutputRange || obligation.Subject == "answer"
	}
	require.True(t, oldOutputRange, "synthetic regression reproduces the old impossible input obligation")
	missing, err := missingS3GeneratedCoverageObligationsV5(spec, plan, inputs)
	require.NoError(t, err)
	require.Empty(t, missing)
	params, err := enrichS3TestDataParamsV1(domain.ProblemGenParams{}, spec, plan)
	require.NoError(t, err)
	_, payload, ok := strings.Cut(params.CustomPrompt, "SERVER-AUTHORED TYPED TEST-DATA CONTRACT (authoritative; do not override with prose):\n")
	require.True(t, ok)
	var context struct {
		SemanticSpec    domain.SemanticSpecV1     `json:"semantic_spec"`
		DerivedCasePlan IntegerBoundaryCasePlanV1 `json:"derived_case_plan"`
		DerivedSHA      string                    `json:"derived_case_plan_sha256"`
	}
	require.NoError(t, json.NewDecoder(strings.NewReader(payload)).Decode(&context))
	require.Equal(t, spec, context.SemanticSpec, "output/derived constraints must remain available to G/V and review")
	require.Equal(t, IntegerInputBoundaryCasePlanRuleVersionV3, context.DerivedCasePlan.RuleVersion, "output strings must not change the integer input coverage profile")
	require.Equal(t, speccontract.SemanticSpecSHA256V1(spec), context.DerivedCasePlan.SemanticSpecSHA256)
	_, hash, err := CanonicalIntegerBoundaryCasePlanV1(context.DerivedCasePlan, inputBoundaryCasePlanRuleForSpecV3(spec))
	require.NoError(t, err)
	require.Equal(t, hash, context.DerivedSHA)
	for _, obligation := range context.DerivedCasePlan.Obligations {
		require.NotEqual(t, "answer", obligation.Subject)
		require.NotEqual(t, "total", obligation.Subject)
		require.NotContains(t, obligation.BoundaryRefs, "answer-length-two")
		require.NotContains(t, obligation.BoundaryRefs, "total-max")
	}
	after, err := json.Marshal(spec)
	require.NoError(t, err)
	require.Equal(t, before, after, "scope projection must never mutate the accepted full SemanticSpec")
	require.Contains(t, params.CustomPrompt, "Unknown subjects or scope/grammar mismatches are errors")
}

func TestS3CoverageV5FailsClosedBeforeIgnoringUnknownOrMisboundFacts(t *testing.T) {
	for name, mutate := range map[string]func(*domain.SemanticSpecV1, *domain.AuthoringPlanV1){
		"undefined constraint": func(spec *domain.SemanticSpecV1, _ *domain.AuthoringPlanV1) {
			spec.Constraints[3].Subject = "missing-output"
		},
		"undefined boundary": func(spec *domain.SemanticSpecV1, _ *domain.AuthoringPlanV1) {
			spec.Boundaries[7].Symbol = "missing-output"
		},
		"empty scope":   func(spec *domain.SemanticSpecV1, _ *domain.AuthoringPlanV1) { spec.Symbols[2].Scope = "" },
		"unknown scope": func(spec *domain.SemanticSpecV1, _ *domain.AuthoringPlanV1) { spec.Symbols[2].Scope = "outputs" },
		"output in input grammar": func(spec *domain.SemanticSpecV1, _ *domain.AuthoringPlanV1) {
			spec.InputGrammar.Lines[0].Fields[0].Symbol = "answer"
		},
		"input missing from grammar": func(spec *domain.SemanticSpecV1, _ *domain.AuthoringPlanV1) {
			spec.InputGrammar.Lines = spec.InputGrammar.Lines[:1]
		},
		"output boundary in input intent": func(_ *domain.SemanticSpecV1, plan *domain.AuthoringPlanV1) {
			plan.TestIntents[0].BoundaryRefs = []string{"answer-length-two"}
		},
	} {
		t.Run(name, func(t *testing.T) {
			spec, plan, inputs := s3OutputConstraintFixtureV5(t)
			mutate(&spec, &plan)
			plan.SemanticSpecSHA256 = speccontract.SemanticSpecSHA256V1(spec)
			_, err := enrichS3TestDataParamsV1(domain.ProblemGenParams{}, spec, plan)
			require.Error(t, err)
			_, err = missingS3GeneratedCoverageObligationsV5(spec, plan, inputs)
			require.Error(t, err)
		})
	}
	// Input limits and cross-variable relations are still checked against real
	// values, even though unrelated output facts no longer create obligations.
	spec, plan, _ := s3OutputConstraintFixtureV5(t)
	_, err := missingS3GeneratedCoverageObligationsV5(spec, plan, []string{"2\n0 10\n"})
	require.ErrorContains(t, err, "does not satisfy SemanticSpec input")
	_, err = missingS3GeneratedCoverageObligationsV5(spec, plan, []string{"2\n0\n"})
	require.ErrorContains(t, err, "does not satisfy SemanticSpec input")
}

func TestS3CoverageV5DoesNotReuseCompletedV4ProviderEffects(t *testing.T) {
	model := &authoringFormatLLM{responses: []*llm.Response{authoringLLMResponse("not JSON"), authoringLLMResponse(s3CoverageFixtureJSONV5)}}
	acts, _, _ := authoringFormatActivities(model)
	cache := &authoringFormatEffectStore{requests: map[string]string{}, results: map[string]json.RawMessage{}}
	acts.deps.ProviderEffects, acts.deps.ProviderEffectLease = cache, time.Minute
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestActivityEnvironment()
	fn := func(ctx context.Context) error {
		for _, prompt := range []string{s3TestDataPromptIDV4, s3TestDataRepairPromptIDV4} {
			key, err := temporalProviderEffectKey(ctx, "llm:"+prompt)
			if err != nil {
				return err
			}
			cache.requests[key] = "historical-v4-contract"
			cache.results[key] = json.RawMessage(`{"synthetic":"historical-v4-result"}`)
		}
		spec, plan, _ := s3OutputConstraintFixtureV5(t)
		params, err := enrichS3TestDataParamsV1(domain.ProblemGenParams{}, spec, plan)
		if err != nil {
			return err
		}
		activity.RecordHeartbeat(ctx, "synthetic V5 scope-aware generation")
		_, err = acts.generateTestDataActivity(ctx, "synthetic bounded sequence", domain.TestDataConfig{NumTestCases: 3, NumSamples: 1}, params, true)
		return err
	}
	env.RegisterActivity(fn)
	_, err := env.ExecuteActivity(fn)
	require.NoError(t, err)
	require.Len(t, model.requests, 2)
	require.Equal(t, s3TestDataPromptIDV5, model.requests[0].PromptID)
	require.Equal(t, s3TestDataRepairPromptIDV5, model.requests[1].PromptID)
	for _, request := range model.requests {
		require.Equal(t, "v5", request.PromptVersion)
	}
}
