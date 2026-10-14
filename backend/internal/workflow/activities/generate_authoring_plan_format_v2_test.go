package activities

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"github.com/Gingoo-TvT/Qraft/backend/internal/llm"
	"github.com/Gingoo-TvT/Qraft/backend/internal/repository"
	"github.com/google/uuid"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"
)

type authoringFormatLLM struct {
	responses []*llm.Response
	requests  []*llm.Request
}

func (l *authoringFormatLLM) CompleteWithRetry(_ context.Context, req *llm.Request, _ int) (*llm.Response, error) {
	copy := *req
	copy.Messages = append([]llm.Message(nil), req.Messages...)
	l.requests = append(l.requests, &copy)
	if len(l.requests) > len(l.responses) {
		return nil, fmt.Errorf("unexpected extra authoring provider invocation")
	}
	return l.responses[len(l.requests)-1], nil
}

func authoringFormatValidJSON(t *testing.T, output authoringPlanModelOutputV1) string {
	t.Helper()
	encoded, err := marshalAuthoringModelOutputV1(output)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}

func authoringFormatActivities(model *authoringFormatLLM) (*Activities, *oracleMemoryArtifactStore, *captureProvenanceRecorder) {
	store := newOracleMemoryArtifactStore()
	provenance := &captureProvenanceRecorder{}
	return &Activities{
		deps:      &Dependencies{LLM: model, LLMProvider: "fixture-provider", LLMModel: "fixture-model", ProvenanceRecorder: provenance},
		artifacts: store,
	}, store, provenance
}

func TestAuthoringPlanV2PromptDescribesActualNestedContract(t *testing.T) {
	system, err := authoringPlanSystemPromptV2()
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(system, "Complete output JSON Schema:\n")
	if len(parts) != 2 {
		t.Fatalf("missing complete schema in prompt: %s", system)
	}
	var schema struct {
		Definitions map[string]struct {
			Type                 string                     `json:"type"`
			AdditionalProperties bool                       `json:"additionalProperties"`
			Properties           map[string]json.RawMessage `json:"properties"`
			Required             []string                   `json:"required"`
		} `json:"$defs"`
	}
	if err := json.Unmarshal([]byte(parts[1]), &schema); err != nil {
		t.Fatal(err)
	}
	if len(schema.Definitions) < 15 {
		t.Fatalf("nested model contract is incomplete: %d definitions", len(schema.Definitions))
	}
	for name, definition := range schema.Definitions {
		if definition.Type != "object" || definition.AdditionalProperties {
			t.Fatalf("%s is not a closed object", name)
		}
	}
	semantic := schema.Definitions[reflect.TypeOf(domain.SemanticSpecV1{}).Name()]
	if string(semantic.Properties["problem_definition"]) != `{"type":"string"}` || semantic.Properties["sections"] == nil || semantic.Properties["input_constraints"] != nil {
		t.Fatalf("problem facts contract is misleading: %+v", semantic.Properties)
	}
	grammarField := schema.Definitions[reflect.TypeOf(domain.SemanticGrammarFieldV1{}).Name()]
	if grammarField.Properties["symbol"] == nil || grammarField.Properties["count"] == nil || grammarField.Properties["name"] != nil {
		t.Fatalf("grammar field contract is wrong: %+v", grammarField.Properties)
	}
	expression := schema.Definitions[reflect.TypeOf(domain.SemanticExpressionV1{}).Name()]
	if !strings.Contains(string(expression.Properties["args"]), `"$ref":"#/$defs/SemanticExpressionV1"`) {
		t.Fatalf("recursive expression shape is missing: %s", expression.Properties["args"])
	}
	for _, name := range []string{"SemanticSpecV1", "AuthoringPlanV1"} {
		for _, field := range []string{"schema_version", "brief_sha256", "semantic_spec_sha256"} {
			if schema.Definitions[name].Properties[field] != nil {
				t.Fatalf("model contract exposes server-owned %s.%s", name, field)
			}
		}
	}
}

func TestAuthoringPlanV2RepairsOneFormatResponseAndPreservesBothSources(t *testing.T) {
	valid := authoringFormatValidJSON(t, validAuthoringModelOutputV1())
	badResponses := map[string]string{
		"problem definition object": `{"decision":"accepted","semantic_spec":{"problem_definition":{"summary":"sum"}},"authoring_plan":{}}`,
		"unknown input constraints": `{"decision":"accepted","semantic_spec":{"input_constraints":[]},"authoring_plan":{}}`,
		"unknown input section":     `{"decision":"accepted","semantic_spec":{"input_section":"read n"},"authoring_plan":{}}`,
		"unknown input":             `{"decision":"accepted","semantic_spec":{"input":"read n"},"authoring_plan":{}}`,
		"grammar guessed name":      `{"decision":"accepted","semantic_spec":{"input_grammar":{"profile":"algoforge.io-grammar.token-lines.v1","lines":[{"id":"size","repeat":{"kind":"integer","value":1},"fields":[{"name":"n","mode":"scalar"}],"meaning":"size"}]}},"authoring_plan":{}}`,
		"non JSON text":             "å synthetic non-JSON answer",
	}
	for name, bad := range badResponses {
		t.Run(name, func(t *testing.T) {
			model := &authoringFormatLLM{responses: []*llm.Response{authoringLLMResponse(bad), authoringLLMResponse(valid)}}
			acts, store, provenance := authoringFormatActivities(model)
			result := executeAuthoringActivity(t, acts, validAuthoringActivityInput())
			if !result.LintPassed || result.Decision != AuthoringPlanDecisionAccepted || len(model.requests) != 2 || len(result.SourceArtifacts) != 3 || len(provenance.records) != 3 {
				t.Fatalf("format repair lost checks/sources: result=%+v calls=%d records=%d", result, len(model.requests), len(provenance.records))
			}
			first, corrected := result.SourceArtifacts[0], result.SourceArtifacts[1]
			if first.LLMCallReceipt.PromptID != AuthoringPlanPromptIDV4 || corrected.LLMCallReceipt.PromptID != AuthoringPlanFormatRepairPromptIDV4 || first.LLMCallReceipt.PromptVersion != "v4" || first.LLMCallReceipt.RequestSHA256 == corrected.LLMCallReceipt.RequestSHA256 {
				t.Fatalf("repair does not have independent immutable request identity: first=%+v corrected=%+v", first, corrected)
			}
			if first.Producer != corrected.Producer || first.Producer != result.BundleArtifact.Producer || !strings.Contains(first.Producer, "GenerateAuthoringPlanActivity") {
				t.Fatalf("activity producer contract changed: first=%+v corrected=%+v bundle=%+v", first, corrected, result.BundleArtifact)
			}
			var original recordedLLMResponse
			if err := json.Unmarshal(store.objects[first.SHA256], &original); err != nil || original.Response.Text() != bad {
				t.Fatalf("original invalid reply was not retained: err=%v original=%+v", err, original)
			}
			var bundle AuthoringPlanBundleV1
			if err := json.Unmarshal(store.objects[result.BundleSHA256], &bundle); err != nil {
				t.Fatal(err)
			}
			if bundle.SourceArtifactSHA256 != corrected.SHA256 || bundle.SourceRequestSHA256 != corrected.LLMCallReceipt.RequestSHA256 {
				t.Fatalf("bundle does not bind final corrected response: %+v", bundle)
			}
			var repairData struct {
				FrozenTask       string `json:"frozen_task"`
				PreviousResponse string `json:"previous_response"`
				DecoderError     string `json:"decoder_error"`
			}
			if err := json.Unmarshal([]byte(model.requests[1].Messages[0].Content), &repairData); err != nil || repairData.PreviousResponse != bad || repairData.DecoderError == "" || repairData.FrozenTask != model.requests[0].Messages[0].Content {
				t.Fatalf("repair payload lost frozen task/format evidence: err=%v data=%+v", err, repairData)
			}
			if model.requests[0].Model != model.requests[1].Model || !strings.Contains(model.requests[1].System, "untrusted") {
				t.Fatal("format correction changed model or failed to delimit untrusted output")
			}
		})
	}
}

func TestAuthoringPlanV2FailsClosedAfterSingleRepair(t *testing.T) {
	bad := `{"decision":"accepted","semantic_spec":{"input_section":"guessed"},"authoring_plan":{}}`
	model := &authoringFormatLLM{responses: []*llm.Response{authoringLLMResponse(bad), authoringLLMResponse(bad)}}
	acts, _, provenance := authoringFormatActivities(model)
	_, err := executeAuthoringActivityRaw(acts, validAuthoringActivityInput())
	var applicationErr *temporal.ApplicationError
	if !errors.As(err, &applicationErr) || applicationErr.Type() != authoringPlanFormatErrorType || !applicationErr.NonRetryable() || len(model.requests) != 2 || len(provenance.records) != 2 {
		t.Fatalf("malformed correction was retriable or escaped parser: err=%v calls=%d records=%d", err, len(model.requests), len(provenance.records))
	}
}

func TestAuthoringPlanV2DoesNotFormatRepairTruncatedReply(t *testing.T) {
	for _, stopReason := range []string{"max_tokens", "length", "max_output_tokens", "token_limit"} {
		t.Run(stopReason, func(t *testing.T) {
			response := authoringLLMResponse(`{"decision":"accepted"`)
			response.StopReason = stopReason
			model := &authoringFormatLLM{responses: []*llm.Response{response}}
			acts, _, provenance := authoringFormatActivities(model)
			_, err := executeAuthoringActivityRaw(acts, validAuthoringActivityInput())
			var applicationErr *temporal.ApplicationError
			if !errors.As(err, &applicationErr) || applicationErr.Type() != authoringPlanTruncatedErrorType || !applicationErr.NonRetryable() || len(model.requests) != 1 || len(provenance.records) != 1 {
				t.Fatalf("truncation was treated as a field error: err=%v calls=%d records=%d", err, len(model.requests), len(provenance.records))
			}
		})
	}
	response := authoringLLMResponse(`{"decision":"accepted"`)
	response.StopReason = "max_tokens"
	model := &authoringFormatLLM{responses: []*llm.Response{authoringLLMResponse("invalid"), response}}
	acts, _, _ := authoringFormatActivities(model)
	_, err := executeAuthoringActivityRaw(acts, validAuthoringActivityInput())
	var applicationErr *temporal.ApplicationError
	if !errors.As(err, &applicationErr) || applicationErr.Type() != authoringPlanTruncatedErrorType || len(model.requests) != 2 {
		t.Fatalf("truncated correction was retried: err=%v calls=%d", err, len(model.requests))
	}
}

func TestAuthoringPlanV2CorrectionStillRequiresSemanticLint(t *testing.T) {
	output := validAuthoringModelOutputV1()
	output.SemanticSpec.Sections.Output = nil
	model := &authoringFormatLLM{responses: []*llm.Response{authoringLLMResponse("not JSON"), authoringLLMResponse(authoringFormatValidJSON(t, output))}}
	acts, _, _ := authoringFormatActivities(model)
	result := executeAuthoringActivity(t, acts, validAuthoringActivityInput())
	if result.LintPassed || result.Decision != AuthoringPlanDecisionRejected || result.GateReasonCode != AuthoringPlanGateReasonSpecLintFailed || result.LintErrorCount == 0 || len(model.requests) != 2 {
		t.Fatalf("format correction weakened semantic lint: %+v calls=%d", result, len(model.requests))
	}
}

type authoringFormatEffectStore struct {
	requests map[string]string
	results  map[string]json.RawMessage
	acquired []string
}

func (s *authoringFormatEffectStore) Acquire(_ context.Context, key, _, request string, _ time.Duration) (repository.ProviderEffectClaim, error) {
	s.acquired = append(s.acquired, key)
	if old, ok := s.requests[key]; ok && old != request {
		return repository.ProviderEffectClaim{}, fmt.Errorf("synthetic request hash collision")
	}
	s.requests[key] = request
	if result, ok := s.results[key]; ok {
		return repository.ProviderEffectClaim{State: repository.ProviderEffectCompleted, ResultJSON: result}, nil
	}
	return repository.ProviderEffectClaim{State: repository.ProviderEffectAcquired, LeaseToken: uuid.New()}, nil
}

func (s *authoringFormatEffectStore) Complete(_ context.Context, key, _, _ string, _ uuid.UUID, result json.RawMessage) error {
	s.results[key] = append(json.RawMessage(nil), result...)
	return nil
}

func (s *authoringFormatEffectStore) Fail(context.Context, string, string, string, uuid.UUID, string) error {
	return nil
}

func TestAuthoringPlanV2RepairEffectsAreIndependentAndReplayWithoutPaidCalls(t *testing.T) {
	valid := authoringFormatValidJSON(t, validAuthoringModelOutputV1())
	model := &authoringFormatLLM{responses: []*llm.Response{authoringLLMResponse("invalid JSON"), authoringLLMResponse(valid)}}
	acts, _, _ := authoringFormatActivities(model)
	cache := &authoringFormatEffectStore{requests: map[string]string{}, results: map[string]json.RawMessage{}}
	acts.deps.ProviderEffects = cache
	acts.deps.ProviderEffectLease = time.Minute
	var legacyKey, legacyV2Key, legacyV3Key string
	run := func() *GenerateAuthoringPlanResult {
		var suite testsuite.WorkflowTestSuite
		env := suite.NewTestActivityEnvironment()
		wrapper := func(ctx context.Context, input GenerateAuthoringPlanInput) (*GenerateAuthoringPlanResult, error) {
			var err error
			legacyKey, err = temporalProviderEffectKey(ctx, "llm:authoring_plan")
			if err != nil {
				return nil, err
			}
			// The old contract's cached malformed reply must remain unused. A
			// changed request under this exact old key would fail acquisition.
			cache.requests[legacyKey] = "old-contract-request-hash"
			cache.results[legacyKey] = json.RawMessage(`{"content":[{"type":"text","text":"old malformed response"}]}`)
			legacyV2Key, err = temporalProviderEffectKey(ctx, "llm:"+AuthoringPlanPromptIDV2)
			if err != nil {
				return nil, err
			}
			cache.requests[legacyV2Key] = "old-v2-contract-request-hash"
			cache.results[legacyV2Key] = cache.results[legacyKey]
			legacyV3Key, err = temporalProviderEffectKey(ctx, "llm:"+AuthoringPlanPromptIDV3)
			if err != nil {
				return nil, err
			}
			cache.requests[legacyV3Key] = "old-v3-contract-request-hash"
			cache.results[legacyV3Key] = cache.results[legacyKey]
			return acts.GenerateAuthoringPlanActivity(ctx, input)
		}
		env.RegisterActivityWithOptions(wrapper, activity.RegisterOptions{Name: "GenerateAuthoringPlanActivity"})
		encoded, err := env.ExecuteActivity("GenerateAuthoringPlanActivity", validAuthoringActivityInput())
		if err != nil {
			t.Fatal(err)
		}
		var result GenerateAuthoringPlanResult
		if err := encoded.Get(&result); err != nil {
			t.Fatal(err)
		}
		return &result
	}
	first := run()
	second := run()
	if len(model.requests) != 2 || len(cache.acquired) != 4 || cache.acquired[0] == cache.acquired[1] || cache.acquired[0] == legacyKey || cache.acquired[1] == legacyKey || cache.acquired[0] == legacyV2Key || cache.acquired[1] == legacyV2Key || cache.acquired[0] == legacyV3Key || cache.acquired[1] == legacyV3Key || cache.acquired[0] != cache.acquired[2] || cache.acquired[1] != cache.acquired[3] || cache.requests[cache.acquired[0]] == cache.requests[cache.acquired[1]] || first.BundleSHA256 != second.BundleSHA256 {
		t.Fatalf("repair replay or effect identity broken: calls=%d keys=%v legacy=%s first=%s second=%s", len(model.requests), cache.acquired, legacyKey, first.BundleSHA256, second.BundleSHA256)
	}
}

func TestAuthoringFormatRepairPayloadIsBoundedAndUTF8(t *testing.T) {
	req := &llm.Request{Messages: []llm.Message{{Role: "user", Content: "frozen task"}}}
	bad := strings.Repeat("中", maxAuthoringFormatRepairBytes)
	repair := authoringPlanFormatRepairRequestV2(req, bad, errors.New(strings.Repeat("中", 2048)))
	var payload struct {
		PreviousResponse string `json:"previous_response"`
		DecoderError     string `json:"decoder_error"`
		Clipped          bool   `json:"response_clipped"`
	}
	if err := json.Unmarshal([]byte(repair.Messages[0].Content), &payload); err != nil {
		t.Fatal(err)
	}
	if !payload.Clipped || len(payload.PreviousResponse) > maxAuthoringFormatRepairBytes || len(payload.DecoderError) > 2048 || !utf8.ValidString(payload.PreviousResponse) || !utf8.ValidString(payload.DecoderError) {
		t.Fatalf("repair payload is unbounded or invalid UTF-8: response=%d error=%d clipped=%v", len(payload.PreviousResponse), len(payload.DecoderError), payload.Clipped)
	}
}
