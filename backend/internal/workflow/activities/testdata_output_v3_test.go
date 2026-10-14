package activities

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"github.com/Gingoo-TvT/Qraft/backend/internal/llm"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"
)

const testDataFormatFixtureV3 = `{"test_case_count":2,"test_cases":[{"input":"2\n","group_id":0,"is_sample":true,"description":"small","coverage":["minimum"]}],"generator_code":""}`

func testDataFormatRunV3(acts *Activities) (*TestDataResult, error) {
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestActivityEnvironment()
	env.RegisterActivity(acts.GenerateTestDataActivity)
	value, err := env.ExecuteActivity(acts.GenerateTestDataActivity, "Read an integer and print it.", domain.TestDataConfig{
		NumTestCases: 2, NumSamples: 1,
		CustomCases: []domain.CustomTestCase{{Input: "1\n", Description: "custom minimum"}},
	}, domain.ProblemGenParams{})
	if err != nil {
		return nil, err
	}
	var result TestDataResult
	return &result, value.Get(&result)
}

func TestTestDataV3FullObjectAndOneRepairPreserveCasesAndSources(t *testing.T) {
	for _, first := range []string{testDataFormatFixtureV3, `test_case_count":2,"test_cases":[]}`} {
		model := &authoringFormatLLM{responses: []*llm.Response{authoringLLMResponse(first), authoringLLMResponse(testDataFormatFixtureV3)}}
		acts, _, _ := authoringFormatActivities(model)
		result, err := testDataFormatRunV3(acts)
		if err != nil {
			t.Fatal(err)
		}
		wantCalls := 1
		if first != testDataFormatFixtureV3 {
			wantCalls = 2
		}
		if len(model.requests) != wantCalls || len(result.SourceArtifacts) != wantCalls || len(result.TestCases) != 2 || result.TestCases[0].Input != "1\n" || result.TestCases[1].Input != "2\n" {
			t.Fatalf("lost custom/generated data or sources: result=%+v calls=%d", result, len(model.requests))
		}
		for i, request := range model.requests {
			if strings.Contains(request.System, "after the assistant prefill") || !strings.Contains(request.System, "COMPLETE JSON object") || request.PromptVersion != "v3" {
				t.Fatalf("ambiguous full-object request: %+v", request)
			}
			if !strings.Contains(request.System, "indices 2 through 11, not 0 through 9") {
				t.Fatal("inline sample positions are absent from the generator index contract")
			}
			for _, message := range request.Messages {
				if message.Role == "assistant" && message.Content == "{" {
					t.Fatal("provider-specific assistant prefill remains")
				}
			}
			if result.SourceArtifacts[i].LLMCallReceipt == nil || result.SourceArtifacts[i].LLMCallReceipt.PromptID != request.PromptID {
				t.Fatal("repair provenance does not bind the actual request")
			}
		}
		if wantCalls == 2 && (model.requests[1].PromptID != testDataRepairPromptIDV3 || result.SourceArtifacts[0].LLMCallReceipt.RequestSHA256 == result.SourceArtifacts[1].LLMCallReceipt.RequestSHA256) {
			t.Fatal("repair reuses an immutable original request identity")
		}
	}
}

func TestTestDataV3RedeliveryReusesBothEffects(t *testing.T) {
	model := &authoringFormatLLM{responses: []*llm.Response{authoringLLMResponse("not JSON"), authoringLLMResponse(testDataFormatFixtureV3)}}
	acts, _, _ := authoringFormatActivities(model)
	cache := &authoringFormatEffectStore{requests: map[string]string{}, results: map[string]json.RawMessage{}}
	acts.deps.ProviderEffects, acts.deps.ProviderEffectLease = cache, time.Minute
	first, err := testDataFormatRunV3(acts)
	if err != nil {
		t.Fatal(err)
	}
	second, err := testDataFormatRunV3(acts)
	if err != nil || len(model.requests) != 2 || len(second.SourceArtifacts) != len(first.SourceArtifacts) {
		t.Fatalf("redelivery repeated paid effects: err=%v calls=%d", err, len(model.requests))
	}
}

func TestTestDataV3TruncationAndBadCorrectionFailClosed(t *testing.T) {
	for _, stop := range []string{"max_tokens", "length", "max_output_tokens", "token_limit", "stop"} {
		t.Run(stop, func(t *testing.T) {
			response := authoringLLMResponse("not JSON")
			response.StopReason = stop
			model := &authoringFormatLLM{responses: []*llm.Response{response, response}}
			acts, _, _ := authoringFormatActivities(model)
			_, err := testDataFormatRunV3(acts)
			var failure *temporal.ApplicationError
			wantType, wantCalls := "TestDataTruncatedError", 1
			if stop == "stop" {
				wantType, wantCalls = "TestDataFormatError", 2
			}
			if !errors.As(err, &failure) || !failure.NonRetryable() || failure.Type() != wantType || len(model.requests) != wantCalls {
				t.Fatalf("incorrect retry/diagnosis: err=%v calls=%d", err, len(model.requests))
			}
		})
	}
}

func TestTestDataV3FormatCorrectionDoesNotApproveInvalidGenerator(t *testing.T) {
	validJSONInvalidInputs := `{"test_case_count":2,"test_cases":[{"input":"","is_sample":true}],"generator_code":""}`
	model := &authoringFormatLLM{responses: []*llm.Response{authoringLLMResponse("not JSON"), authoringLLMResponse(validJSONInvalidInputs)}}
	acts, _, _ := authoringFormatActivities(model)
	_, err := testDataFormatRunV3(acts)
	var failure *temporal.ApplicationError
	if !errors.As(err, &failure) || failure.Type() != "InvalidGeneratedTestData" || !failure.NonRetryable() || len(model.requests) != 2 {
		t.Fatalf("format correction bypassed generated input check: err=%v", err)
	}
}
