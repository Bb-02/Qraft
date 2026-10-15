package activities

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"github.com/Gingoo-TvT/Qraft/backend/internal/llm"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"
)

func addS3RegionIndexesFixtureV6(t *testing.T, text string, spec domain.SemanticSpecV1, plan domain.AuthoringPlanV1, selectRegion func(int, IntegerBoundaryCaseObligationV1) bool) string {
	t.Helper()
	var value map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(text), &value))
	derived, err := DeriveInputBoundaryCasePlanV3(spec, plan)
	require.NoError(t, err)
	for i, item := range value["test_cases"].([]interface{}) {
		indexes := []int{}
		for index, ob := range derived.Obligations {
			if ob.Kind == IntegerBoundaryObligationRegion && selectRegion(i, ob) {
				indexes = append(indexes, index)
			}
		}
		item.(map[string]interface{})["region_obligation_indexes"] = indexes
	}
	encoded, err := json.Marshal(value)
	require.NoError(t, err)
	return string(encoded)
}

func TestS3TestDataV6StrictCaseContractAndLegacyParserCompatibility(t *testing.T) {
	good := `{"test_cases":[{"input":"1\n0\n","region_obligation_indexes":[]}],"generator_code":""}`
	r, err := parseS3TestDataResponseV6(good)
	require.NoError(t, err)
	require.NotNil(t, r.TestCases[0].RegionObligationIndexes)
	for _, text := range []string{
		strings.Replace(good, `,"region_obligation_indexes":[]`, "", 1),
		strings.Replace(good, `"region_obligation_indexes":[]`, `"region_obligation_indexes":null`, 1),
		strings.Replace(good, `"region_obligation_indexes":[]`, `"region_obligation_indexes":[-1]`, 1),
		strings.Replace(good, `"region_obligation_indexes":[]`, `"region_obligation_indexes":[2,2]`, 1),
		strings.Replace(good, `"region_obligation_indexes":[]`, `"region_obligation_indexes":[2,1]`, 1),
		strings.Replace(good, `"region_obligation_indexes":[]`, `"region_obligation_indexes":[0.5]`, 1),
		strings.Replace(good, `"region_obligation_indexes":[]`, `"region_obligation_indexes":[],"constraint_region":"guessed"`, 1),
		strings.Replace(good, `"generator_code":""`, `"generator_code":"","region":"guessed"`, 1),
	} {
		_, err := parseS3TestDataResponseV6(text)
		require.Error(t, err, text)
	}
	legacy, err := parseTestDataResponse(good)
	require.NoError(t, err)
	require.Nil(t, legacy.S3Coverage)
	require.Nil(t, legacy.TestCases[0].RegionObligationIndexes, "public/import v3 must not adopt S3 claims")
	encoded, err := json.Marshal(legacy)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "s3_coverage")
	require.NotContains(t, string(encoded), "region_obligation_indexes")
}

func TestS3TestDataV6BindsIndexesToExactPlanAndKeepsCustomCaseAssociations(t *testing.T) {
	spec, plan, _, _ := validIntegerCoverageFixtureV1(t)
	params, contract, err := enrichS3TestDataParamsV6(domain.ProblemGenParams{}, spec, plan)
	require.NoError(t, err)
	require.Contains(t, params.CustomPrompt, `"indexed_region_obligations"`)
	require.Contains(t, params.CustomPrompt, "not prove any condition on its sum")
	text := addS3RegionIndexesFixtureV6(t, s3CoverageFixtureJSONV5, spec, plan, func(i int, ob IntegerBoundaryCaseObligationV1) bool {
		return ob.ConstraintRegion == []string{"region-min", "region-mid", "region-max"}[i]
	})
	data, err := parseS3TestDataResponseV6(text)
	require.NoError(t, err)
	data.S3Coverage = contract
	ids, err := s3CoverageAssociationIDsV6(spec, plan, *data)
	require.NoError(t, err)
	require.Len(t, ids[2], 1)
	before := append([]int(nil), data.TestCases[2].RegionObligationIndexes...)
	merged := mergeCustomCasesForConfig(data.TestCases, domain.TestDataConfig{NumTestCases: 4, CustomCases: []domain.CustomTestCase{{Input: "1\n0\n"}}})
	require.Equal(t, before, merged[3].RegionObligationIndexes, "case metadata follows the actual case when custom cases are prepended")
	data.S3Coverage = &S3TestDataCoverageV6{SchemaVersion: S3TestDataCoverageSchemaV6, DerivedCasePlanSHA256: strings.Repeat("a", 64)}
	_, err = s3CoverageAssociationIDsV6(spec, plan, *data)
	require.ErrorContains(t, err, "different derived plan")
	derived, err := DeriveInputBoundaryCasePlanV3(spec, plan)
	require.NoError(t, err)
	_, err = s3RegionObligationIDsV6(*derived, []int{len(derived.Obligations)})
	require.Error(t, err)
	_, err = s3RegionObligationIDsV6(*derived, []int{0})
	require.ErrorContains(t, err, "does not reference a constraint_region")
}

func TestS3TestDataV6EffectRepairAndCoveragePreserveRealReceipts(t *testing.T) {
	spec, plan, _, _ := validIntegerCoverageFixtureV1(t)
	good := addS3RegionIndexesFixtureV6(t, s3CoverageFixtureJSONV5, spec, plan, func(i int, ob IntegerBoundaryCaseObligationV1) bool {
		return ob.ConstraintRegion == []string{"region-min", "region-mid", "region-max"}[i]
	})
	for _, initial := range []string{good, s3CoverageFixtureJSONV5} {
		model := &authoringFormatLLM{responses: []*llm.Response{authoringLLMResponse(initial), authoringLLMResponse(good)}}
		acts, _, _ := authoringFormatActivities(model)
		acts.deps.ProviderEffects = &authoringFormatEffectStore{requests: map[string]string{}, results: map[string]json.RawMessage{}}
		acts.deps.ProviderEffectLease = time.Minute
		fn := func(ctx context.Context) (*TestDataResult, error) {
			params, contract, err := enrichS3TestDataParamsV6(domain.ProblemGenParams{}, spec, plan)
			if err != nil {
				return nil, err
			}
			result, err := acts.generateTestDataActivity(ctx, "synthetic integer sequence", domain.TestDataConfig{NumTestCases: 3, NumSamples: 1}, params, true, contract)
			if err != nil {
				return nil, err
			}
			return result, acts.validateS3GeneratedTestDataCoverageV6(ctx, spec, plan, *result)
		}
		run := func() TestDataResult {
			var suite testsuite.WorkflowTestSuite
			env := suite.NewTestActivityEnvironment()
			env.RegisterActivity(fn)
			value, err := env.ExecuteActivity(fn)
			require.NoError(t, err)
			var result TestDataResult
			require.NoError(t, value.Get(&result))
			return result
		}
		result := run()
		calls := 1
		if initial != good {
			calls = 2
		}
		require.Len(t, model.requests, calls)
		require.Len(t, result.SourceArtifacts, calls)
		require.NotNil(t, result.S3Coverage)
		require.Equal(t, s3TestDataPromptIDV6, model.requests[0].PromptID)
		require.NotEqual(t, s3TestDataPromptIDV5, model.requests[0].PromptID)
		if calls == 2 {
			require.Equal(t, s3TestDataRepairPromptIDV6, model.requests[1].PromptID)
		}
		for _, req := range model.requests {
			require.Equal(t, "v6", req.PromptVersion)
		}
		run()
		require.Len(t, model.requests, calls, "redelivery reuses completed v6 effects")
	}
}

func TestS3TestDataV6MissingAssociationFailsBeforeVerificationAndRetainsSources(t *testing.T) {
	spec, plan, _, _ := validIntegerCoverageFixtureV1(t)
	_, contract, err := enrichS3TestDataParamsV6(domain.ProblemGenParams{}, spec, plan)
	require.NoError(t, err)
	text := addS3RegionIndexesFixtureV6(t, s3CoverageFixtureJSONV5, spec, plan, func(_ int, _ IntegerBoundaryCaseObligationV1) bool { return false })
	result, err := parseS3TestDataResponseV6(text)
	require.NoError(t, err)
	result.S3Coverage = contract
	result.TestCases[2].Description = "all regions covered including maximum"
	result.TestCases[2].Coverage = []string{coverageMaximum}
	ref := manifestV2FixtureArtifact("synthetic provider receipt", "application/json")
	result.SourceArtifacts = []*ArtifactRef{&ref}
	acts := &Activities{}
	err = acts.validateS3GeneratedTestDataCoverageV6(context.Background(), spec, plan, *result)
	var failure *temporal.ApplicationError
	require.ErrorAs(t, err, &failure)
	require.Equal(t, "S3TestDataCoverageError", failure.Type())
	require.True(t, failure.NonRetryable())
	require.Contains(t, failure.Error(), "no explicitly associated matching case")
	var sources []*ArtifactRef
	require.NoError(t, failure.Details(&sources))
	require.Equal(t, result.SourceArtifacts, sources)
}
