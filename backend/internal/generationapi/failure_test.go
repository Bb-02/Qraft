package generationapi

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	commonpb "go.temporal.io/api/common/v1"
	enumspb "go.temporal.io/api/enums/v1"
	failurepb "go.temporal.io/api/failure/v1"
	"go.temporal.io/sdk/temporal"
)

func syntheticChildFailure(t *testing.T, cause error) error {
	t.Helper()
	failureConverter := temporal.GetDefaultFailureConverter()
	err := failureConverter.FailureToError(&failurepb.Failure{
		Message: "synthetic child workflow failed",
		Cause:   failureConverter.ErrorToFailure(cause),
		FailureInfo: &failurepb.Failure_ChildWorkflowExecutionFailureInfo{
			ChildWorkflowExecutionFailureInfo: &failurepb.ChildWorkflowExecutionFailureInfo{
				Namespace:         "synthetic-namespace",
				WorkflowExecution: &commonpb.WorkflowExecution{WorkflowId: "synthetic-child", RunId: "synthetic-run"},
				WorkflowType:      &commonpb.WorkflowType{Name: "SyntheticWorkflow"},
				InitiatedEventId:  10,
				StartedEventId:    11,
				RetryState:        enumspb.RETRY_STATE_NON_RETRYABLE_FAILURE,
			},
		},
	})
	var child *temporal.ChildWorkflowExecutionError
	if !errors.As(err, &child) {
		t.Fatalf("fixture did not produce the SDK child workflow error: %T", err)
	}
	return err
}

func TestDescribeFailurePreservesStructuredChildWorkflowDetails(t *testing.T) {
	want := domain.GenerationFailure{
		Code:             "quality_review_required",
		Message:          "候选题需要人工审核",
		Suggestion:       "核对候选题和未通过项后继续。",
		TechnicalDetails: "synthetic diagnostic trace",
		BlockedGate:      "reviewer_schema_verdict",
		BlockingIssues:   []string{"题面描述存在歧义", "样例解释需要补充"},
	}
	application := temporal.NewNonRetryableApplicationError("candidate did not satisfy quality gates", "QualityNotMet", nil, want)
	err := syntheticChildFailure(t, application)
	if got := DescribeFailure(err); !reflect.DeepEqual(got, &want) {
		t.Fatalf("structured child details lost during classification:\ngot: %#v\nwant: %#v", got, &want)
	}
}

func TestDescribeFailureChineseClassification(t *testing.T) {
	cases := []struct {
		name    string
		message string
		code    string
	}{
		{"authentication", "openai HTTP 401 unauthorized", "model_authentication"},
		{"invalid api key", "provider error: invalid_api_key", "model_authentication"},
		{"forbidden", "provider HTTP 403 forbidden", "model_authentication"},
		{"rate limit", "provider returned HTTP 429: rate limit exceeded", "model_rate_limit"},
		{"quota", "provider quota exhausted", "model_rate_limit"},
		{"timeout", "activity StartToClose timeout", "service_timeout"},
		{"deadline", "request deadline exceeded", "service_timeout"},
		{"compile", "reference program compilation failed", "compile_failed"},
		{"duplicate", "dedup detected a duplicate candidate", "duplicate_candidate"},
		{"quality", "candidate did not satisfy all nine quality gates", "quality_review_required"},
		{"sandbox unavailable", "sandbox connection refused", "verification_unavailable"},
		{"samples", "sample output mismatch", "verification_failed"},
		{"unknown", "synthetic unknown provider failure", "generation_failed"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			application := temporal.NewApplicationError(tc.message, "SyntheticFailure", nil)
			got := DescribeFailure(syntheticChildFailure(t, application))
			if got == nil || got.Code != tc.code {
				t.Fatalf("classification = %#v, want code %q", got, tc.code)
			}
			if !strings.ContainsFunc(got.Message, func(r rune) bool { return unicode.Is(unicode.Han, r) }) || strings.TrimSpace(got.Suggestion) == "" {
				t.Fatalf("failure has no Chinese explanation or next action: %#v", got)
			}
			if !strings.Contains(got.TechnicalDetails, tc.message) {
				t.Fatalf("diagnostic details lost the provider reason: %q", got.TechnicalDetails)
			}
		})
	}
}

func TestDescribeFailurePrioritizesProviderCauseInsideHiddenStage(t *testing.T) {
	for _, tc := range []struct {
		message string
		code    string
	}{
		{"run hidden verification: HTTP 401 unauthorized", "model_authentication"},
		{"run hidden verification: HTTP 429 rate limit", "model_rate_limit"},
		{"run hidden verification: deadline exceeded", "service_timeout"},
	} {
		t.Run(tc.code, func(t *testing.T) {
			got := DescribeFailure(errors.New(tc.message))
			if got.Code != tc.code {
				t.Fatalf("stage name hid the actionable underlying cause: %#v", got)
			}
		})
	}
	got := DescribeFailure(errors.New("hidden-suite resolver is not configured"))
	if got.Code != "verification_unavailable" {
		t.Fatalf("missing hidden suite no longer has a service explanation: %#v", got)
	}
}

func TestDescribeFailureNilAndMalformedDetails(t *testing.T) {
	if got := DescribeFailure(nil); got != nil {
		t.Fatalf("nil error invented a failure: %#v", got)
	}
	for _, details := range []interface{}{"synthetic malformed detail", domain.GenerationFailure{Code: "", Message: ""}} {
		got := DescribeFailure(syntheticChildFailure(t, temporal.NewApplicationError("401 unauthorized", "SyntheticFailure", nil, details)))
		if got.Code != "model_authentication" || got.Message == "" {
			t.Fatalf("malformed structured details prevented safe classification: %#v", got)
		}
	}
}

func TestDescribeFailureBoundsChineseTechnicalDetails(t *testing.T) {
	longText := strings.Repeat("合成错误说明🚩", 1200)
	for _, tc := range []struct {
		name string
		err  error
	}{
		{"plain", errors.New(" \n" + longText + "\n ")},
		{"structured", temporal.NewNonRetryableApplicationError("quality candidate failed", "QualityNotMet", nil, domain.GenerationFailure{Code: "quality_review_required", Message: "候选需要审核", Suggestion: "检查候选", TechnicalDetails: " \n" + longText + "\n "})},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := DescribeFailure(tc.err)
			if !utf8.ValidString(got.TechnicalDetails) || utf8.RuneCountInString(got.TechnicalDetails) > 6000 {
				t.Fatalf("diagnostics were not bounded by complete Unicode characters: bytes=%d runes=%d", len(got.TechnicalDetails), utf8.RuneCountInString(got.TechnicalDetails))
			}
			want := string([]rune(longText)[:6000])
			if got.TechnicalDetails != want {
				t.Fatal("diagnostic truncation changed the readable prefix or left surrounding whitespace")
			}
		})
	}
}
