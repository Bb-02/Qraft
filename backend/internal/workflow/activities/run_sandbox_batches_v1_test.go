package activities

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	remotesandbox "github.com/Gingoo-TvT/Qraft/backend/internal/sandbox"
	"github.com/stretchr/testify/require"
)

func batchSandboxResponseV1(inputs []string, limits remotesandbox.RemoteLimits, runID string) *remotesandbox.RemoteExecuteResult {
	audit := remotesandbox.RemoteAuditMetadata{RunID: runID, ManifestDigest: s3TestDigest("1"),
		ImageDigest: s3TestDigest("a"), ToolchainManifestDigest: s3TestDigest("b"), SeccompPolicyDigest: s3TestDigest("c"),
		LimitProfile: fmt.Sprintf("execute-v1:time_ms=%d,memory_mb=%d,output_bytes=%d,pids=%d", limits.TimeLimitMS, limits.MemoryLimitMB, limits.OutputLimitBytes, limits.MaxProcesses), Profile: limits.Profile}
	if limits.Profile != "" {
		audit.LimitProfile += ",profile=" + limits.Profile
	}
	r := &remotesandbox.RemoteExecuteResult{Version: remotesandbox.ProtocolVersion, Compile: *remoteCompileResult("cpp", true, ""), Audit: audit}
	r.Compile.Audit = audit
	for i, input := range inputs {
		r.Results = append(r.Results, remotesandbox.RemoteCaseResult{Index: i, Verdict: remotesandbox.VerdictOK, Stdout: input + " \n", TimeMS: int64(i + 1), MemoryBytes: int64(i + 10)})
	}
	return r
}

func TestFixedSandboxOutputBudgetBatchesEveryCaseWithoutReducingItsLimit(t *testing.T) {
	for _, count := range []int{8, 9, 20} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			var seen []string
			var sizes []int
			fake := &fakeRemoteSandbox{executeFunc: func(_ context.Context, _, _ string, inputs []string, limits remotesandbox.RemoteLimits) (*remotesandbox.RemoteExecuteResult, error) {
				require.EqualValues(t, 8<<20, limits.OutputLimitBytes)
				require.LessOrEqual(t, int64(len(inputs))*limits.OutputLimitBytes, int64(64<<20))
				seen = append(seen, inputs...)
				sizes = append(sizes, len(inputs))
				return batchSandboxResponseV1(inputs, limits, fmt.Sprintf("run-%d", len(sizes))), nil
			}}
			acts := New(&Dependencies{RemoteSandboxFactory: func(time.Duration) (remotesandbox.RemoteExecutor, error) { return fake, nil }})
			cases := make([]TestCaseData, count)
			for i := range cases {
				cases[i].Input = fmt.Sprint(i)
			}
			run, err := acts.RunSandboxActivity(context.Background(), domain.Solution{Language: "cpp"}, cases, ExecutionLimits{TimeLimitMs: 1000, MemoryLimitMB: 64, OutputLimitBytes: 8 << 20, PreserveOutputBytes: true})
			require.NoError(t, err)
			require.Len(t, seen, count)
			require.Len(t, run.Outputs, count)
			for i := range cases {
				require.Equal(t, cases[i].Input, seen[i])
				require.Equal(t, cases[i].Input+" \n", run.Outputs[i])
			}
			identity, err := s3SandboxRunIdentityV1(*run, count)
			require.NoError(t, err)
			if count == 8 {
				require.Equal(t, []int{8}, sizes)
				require.Empty(t, run.Batches)
				require.Equal(t, "run-1", identity.RunID)
			} else {
				require.Equal(t, SandboxAuditMetadata{}, run.Audit)
				require.Empty(t, identity.RunID)
				require.Empty(t, identity.ManifestDigest)
				require.Len(t, run.Batches, (count+7)/8)
				for i, batch := range run.Batches {
					require.Equal(t, i*8, batch.FirstCase)
					require.Equal(t, min(8, count-i*8), batch.CaseCount)
					require.Equal(t, fmt.Sprintf("run-%d", i+1), batch.Audit.RunID)
				}
			}
		})
	}
}

func TestSandboxBatchPlanAlsoHonorsExecutionAndInputBudgets(t *testing.T) {
	for _, tc := range []struct {
		name   string
		inputs []string
		limits ExecutionLimits
		sizes  []int
	}{
		{"time", make([]string, 5), ExecutionLimits{TimeLimitMs: 30000, OutputLimitBytes: 1}, []int{4, 1}},
		{"input", []string{strings.Repeat("x", 17<<20), strings.Repeat("y", 17<<20)}, ExecutionLimits{TimeLimitMs: 1, OutputLimitBytes: 1}, []int{1, 1}},
		{"count", make([]string, 257), ExecutionLimits{TimeLimitMs: 1, OutputLimitBytes: 1}, []int{256, 1}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ranges, err := sandboxInputBatchRangesV1(tc.inputs, tc.limits)
			require.NoError(t, err)
			var sizes []int
			for _, span := range ranges {
				sizes = append(sizes, span.end-span.first)
			}
			require.Equal(t, tc.sizes, sizes)
		})
	}
	_, err := sandboxInputBatchRangesV1([]string{strings.Repeat("x", (32<<20)+1)}, ExecutionLimits{TimeLimitMs: 1, OutputLimitBytes: 1})
	require.ErrorContains(t, err, "input case 1 exceeds")
	_, err = sandboxInputBatchRangesV1([]string{"x"}, ExecutionLimits{TimeLimitMs: 1, OutputLimitBytes: (8 << 20) + 1})
	require.ErrorContains(t, err, "output limit")
}

func TestFixedSandboxBatchesKeepMixedInlineAndCASOutputsInOrder(t *testing.T) {
	store := newOracleMemoryArtifactStore()
	calls := 0
	fake := &fakeRemoteSandbox{executeFunc: func(_ context.Context, _, _ string, inputs []string, limits remotesandbox.RemoteLimits) (*remotesandbox.RemoteExecuteResult, error) {
		calls++
		r := batchSandboxResponseV1(inputs, limits, fmt.Sprintf("run-%d", calls))
		if calls == 1 {
			r.Results[0].Stdout = strings.Repeat("x", 65<<10) + " \n"
		}
		return r, nil
	}}
	acts := New(&Dependencies{ArtifactStore: store, RemoteSandboxFactory: func(time.Duration) (remotesandbox.RemoteExecutor, error) { return fake, nil }})
	cases := make([]TestCaseData, 9)
	for i := range cases {
		cases[i].Input = fmt.Sprint(i)
	}
	run, err := acts.RunSandboxActivity(context.Background(), domain.Solution{Language: "cpp"}, cases, ExecutionLimits{TimeLimitMs: 1000, MemoryLimitMB: 64, OutputLimitBytes: 8 << 20, PreserveOutputBytes: true})
	require.NoError(t, err)
	require.Len(t, run.OutputArtifacts, 9)
	require.Nil(t, run.OutputArtifacts[8])
	outputs, err := acts.resolveS3SandboxOutputsV1(context.Background(), *run)
	require.NoError(t, err)
	require.Equal(t, strings.Repeat("x", 65<<10)+" \n", outputs[0])
	require.Equal(t, "8 \n", outputs[8])
	run.Outputs[0] = "unbound output"
	_, err = acts.resolveS3SandboxOutputsV1(context.Background(), *run)
	require.ErrorContains(t, err, "mixes inline and artifact bytes")
}

func TestFixedSandboxBatchFailureDoesNotSkipOrSoftenTheLastCase(t *testing.T) {
	for _, verdict := range []remotesandbox.Verdict{remotesandbox.VerdictTLE, remotesandbox.Verdict("OLE")} {
		t.Run(string(verdict), func(t *testing.T) {
			calls := 0
			fake := &fakeRemoteSandbox{executeFunc: func(_ context.Context, _, _ string, inputs []string, limits remotesandbox.RemoteLimits) (*remotesandbox.RemoteExecuteResult, error) {
				calls++
				require.EqualValues(t, 8<<20, limits.OutputLimitBytes)
				r := batchSandboxResponseV1(inputs, limits, fmt.Sprintf("run-%d", calls))
				if calls == 2 {
					r.Results[0].Verdict = verdict
				}
				return r, nil
			}}
			acts := New(&Dependencies{RemoteSandboxFactory: func(time.Duration) (remotesandbox.RemoteExecutor, error) { return fake, nil }})
			run, err := acts.RunSandboxActivity(context.Background(), domain.Solution{Language: "cpp"}, make([]TestCaseData, 9), ExecutionLimits{TimeLimitMs: 1000, MemoryLimitMB: 64, OutputLimitBytes: 8 << 20})
			require.Nil(t, run)
			require.ErrorContains(t, err, "sandbox cases 9-9")
			require.ErrorContains(t, err, "verdict "+string(verdict))
			require.Equal(t, 2, calls)
		})
	}
}
