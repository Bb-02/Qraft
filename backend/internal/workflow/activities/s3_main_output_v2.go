package activities

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Gingoo-TvT/Qraft/backend/internal/llm"
	"go.temporal.io/sdk/temporal"
)

const (
	s3MainSolutionPromptIDV2             = "s3_main_solution_v2"
	s3MainSolutionFormatRepairPromptIDV2 = "s3_main_solution_format_repair_v2"
	maxS3MainFormatRepairBytesV2         = 128 << 10
	mainSolutionEmptyResponseErrorType   = "MainSolutionEmptyResponseError"
	mainSolutionFormatErrorType          = "MainSolutionFormatError"
	mainSolutionTruncatedErrorType       = "MainSolutionTruncatedError"
)

const s3MainSolutionSystemPromptV2 = s3MainSolutionSystemPromptV1 + `
source_code is a JSON string: escape quotes, control characters, and every source-code backslash for the outer JSON. In particular, C++ '\n' in the decoded source requires '\\n' in the outer JSON; never put an actual line break inside a C++ character or string literal. Real source line separators are JSON \n escapes. Using char(10) for output newlines avoids this extra escaping. Preserve the requested language exactly. Return nonempty complete source code, not reasoning or an empty final answer.`

// completeS3MainOutputV2 corrects transport shape at most once, under a separate
// cached effect. It does not repair a program, reinterpret source escapes, or
// replace valid source fields. The original and corrected replies remain CAS.
func (a *Activities) completeS3MainOutputV2(ctx context.Context, original *llm.Request, language string) (*oracleCandidateModelOutputV1, *ArtifactRef, []*ArtifactRef, error) {
	if original == nil {
		return nil, nil, nil, s3ContractErrorV1("missing main-solution request")
	}
	req := *original
	req.Messages = append([]llm.Message(nil), original.Messages...)
	req.PromptID, req.PromptVersion = s3MainSolutionPromptIDV2, "v2"
	var sources []*ArtifactRef
	var firstText string
	for attempt := 0; attempt < 2; attempt++ {
		response, ref, err := a.completeLLMWithProvenance(ctx, req.PromptID, &req, 2)
		if err != nil {
			return nil, nil, sources, wrapRequiredProviderEffectError("S3 main solution", err)
		}
		if response == nil || ref == nil || ref.LLMCallReceipt == nil {
			return nil, nil, sources, s3ContractErrorV1("main-solution provider response is missing identity")
		}
		sources = append(sources, ref)
		if modelResponseTruncatedV2(response) {
			return nil, nil, sources, temporal.NewNonRetryableApplicationError("main-solution response was truncated before complete source was available", mainSolutionTruncatedErrorType, nil)
		}
		text := response.Text()
		parsed, parseErr := parseOracleCandidateResponseV1(text)
		if parseErr == nil {
			if parsed.Language != language {
				return nil, nil, sources, temporal.NewNonRetryableApplicationError("main-solution language does not match the requested language", mainSolutionFormatErrorType, nil)
			}
			if attempt == 1 && !preservesKnownMainFieldsV2(firstText, parsed) {
				return nil, nil, sources, temporal.NewNonRetryableApplicationError("main-solution format correction changed known source code or language", mainSolutionFormatErrorType, nil)
			}
			return parsed, ref, sources, nil
		}
		if attempt == 1 || len(text) > maxS3MainFormatRepairBytesV2 {
			errType := mainSolutionFormatErrorType
			if strings.TrimSpace(text) == "" {
				errType = mainSolutionEmptyResponseErrorType
			}
			return nil, nil, sources, temporal.NewNonRetryableApplicationError("strict main-solution response: "+parseErr.Error(), errType, parseErr)
		}
		firstText = text
		// Keep the complete frozen input, runtime/model, budget and sampling.
		// Never truncate malformed program text and call it a format repair.
		req.Messages = append(req.Messages, llm.Message{Role: "assistant", Content: text}, llm.Message{Role: "user", Content: fmt.Sprintf("The final response was empty or was not the required JSON object (%s). Return exactly {\"source_code\":\"complete source\",\"language\":\"%s\"}. Correct only JSON/output shape. Preserve every existing valid source_code and language string exactly, including all decoded backslashes and newlines. Do not change the frozen task, algorithm, constraints, difficulty, or language. If no source was provided, implement the original frozen input. Do not emit Markdown or commentary.", boundedMainParseErrorV2(parseErr), language)})
		req.PromptID = s3MainSolutionFormatRepairPromptIDV2
	}
	panic("bounded main-solution output loop exhausted")
}

func boundedMainParseErrorV2(err error) string {
	message := err.Error()
	if len(message) > 512 {
		message = message[:512]
	}
	return message
}

// Only successfully decoded, nonempty known fields can be preserved reliably.
// Malformed JSON is not heuristically unescaped or treated as trustworthy code.
func preservesKnownMainFieldsV2(raw string, corrected *oracleCandidateModelOutputV1) bool {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &fields); err != nil {
		return true
	}
	for key, actual := range map[string]string{"source_code": corrected.SourceCode, "language": corrected.Language} {
		var known string
		if value, ok := fields[key]; ok && json.Unmarshal(value, &known) == nil && known != "" && known != actual {
			return false
		}
	}
	return true
}
