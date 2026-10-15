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
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"
)

func TestS3CoverageV4ChecksActualInputEdgesAndSameCaseCombinations(t *testing.T) {
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
			missing, err := missingS3GeneratedCoverageObligationsV4(spec, plan, test.inputs)
			require.NoError(t, err)
			if test.want == "" {
				require.Empty(t, missing)
			} else {
				require.Contains(t, strings.Join(missing, "; "), test.want)
			}
		})
	}
	_, err := missingS3GeneratedCoverageObligationsV4(spec, plan, []string{"2\n0 10\n"})
	require.ErrorContains(t, err, "does not satisfy SemanticSpec input")
}

func TestS3CoverageV4ReadsRealCASAndNeverTrustsDescriptionOrCoverage(t *testing.T) {
	spec, plan, _, _ := validIntegerCoverageFixtureV1(t)
	store := newOracleMemoryArtifactStore()
	acts := &Activities{artifacts: store}
	result := TestDataResult{PayloadVersion: ActivityPayloadVersion}
	for _, raw := range []string{"0\n\n", "1\n0\n", "2\n0 8\n"} {
		ref := s3BatchSeedArtifactV1(t, store, []byte(raw), "text/plain", "GenerateS3TestDataActivityV1")
		result.TestCases = append(result.TestCases, TestCaseData{InputArtifact: &ref, Description: "maximum 9 and all boundaries", Coverage: []string{coverageMaximum}})
	}
	err := acts.validateS3GeneratedTestDataCoverageV4(context.Background(), spec, plan, result)
	var failure *temporal.ApplicationError
	require.ErrorAs(t, err, &failure)
	require.Equal(t, "S3TestDataCoverageError", failure.Type())
	require.True(t, failure.NonRetryable())
	require.Contains(t, failure.Error(), "allowed_boundary:a-element-max")
}

func runS3CoverageOutputV4(t *testing.T, acts *Activities) (*TestDataResult, error) {
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
		return result, acts.validateS3GeneratedTestDataCoverageV4(ctx, spec, plan, *result)
	}
	env.RegisterActivity(fn)
	value, err := env.ExecuteActivity(fn)
	if err != nil {
		return nil, err
	}
	var result TestDataResult
	return &result, value.Get(&result)
}

const s3CoverageFixtureJSONV4 = `{"test_case_count":3,"test_cases":[{"input":"0\n\n","is_sample":true},{"input":"1\n0\n"},{"input":"2\n0 9\n"}],"generator_code":""}`

func TestS3CoverageV4PromptEffectVersionAndOneFormatRepairKeepProvenance(t *testing.T) {
	for _, first := range []string{s3CoverageFixtureJSONV4, "not JSON"} {
		model := &authoringFormatLLM{responses: []*llm.Response{authoringLLMResponse(first), authoringLLMResponse(s3CoverageFixtureJSONV4)}}
		acts, _, _ := authoringFormatActivities(model)
		cache := &authoringFormatEffectStore{requests: map[string]string{}, results: map[string]json.RawMessage{}}
		acts.deps.ProviderEffects, acts.deps.ProviderEffectLease = cache, time.Minute
		result, err := runS3CoverageOutputV4(t, acts)
		require.NoError(t, err)
		wantCalls := 1
		if first == "not JSON" {
			wantCalls = 2
		}
		require.Len(t, model.requests, wantCalls)
		require.Len(t, result.SourceArtifacts, wantCalls)
		for i, request := range model.requests {
			require.Equal(t, "v4", request.PromptVersion)
			require.Contains(t, request.Messages[0].Content, `"derived_case_plan"`)
			require.Contains(t, request.Messages[0].Content, "ONE complete input case to hit ALL boundary_refs together")
			require.Equal(t, request.PromptID, result.SourceArtifacts[i].LLMCallReceipt.PromptID)
		}
		require.Equal(t, s3TestDataPromptIDV4, model.requests[0].PromptID)
		if wantCalls == 2 {
			require.Equal(t, s3TestDataRepairPromptIDV4, model.requests[1].PromptID)
		}
		_, err = runS3CoverageOutputV4(t, acts)
		require.NoError(t, err)
		require.Len(t, model.requests, wantCalls, "redelivery reuses all cached effects")
	}
}

func TestS3CoverageV4MissingCoverageDoesNotSpendRepairCalls(t *testing.T) {
	model := &authoringFormatLLM{responses: []*llm.Response{authoringLLMResponse(strings.ReplaceAll(s3CoverageFixtureJSONV4, "0 9", "0 8"))}}
	acts, store, _ := authoringFormatActivities(model)
	_, err := runS3CoverageOutputV4(t, acts)
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

func TestGenerateS3TestDataV4BoundAdapterChecksExecutedGeneratorInputs(t *testing.T) {
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
			require.Equal(t, s3TestDataPromptIDV4, model.requests[0].PromptID)
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
