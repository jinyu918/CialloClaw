package rpc

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/cialloclaw/cialloclaw/services/local-service/internal/model"
	"github.com/cialloclaw/cialloclaw/services/local-service/internal/orchestrator"
	"github.com/cialloclaw/cialloclaw/services/local-service/internal/storage"
	"github.com/cialloclaw/cialloclaw/services/local-service/internal/tools"
)

func TestDispatchMapsArtifactNotFoundErrors(t *testing.T) {
	_, rpcErr := wrapOrchestratorResult(nil, orchestrator.ErrArtifactNotFound)
	if rpcErr == nil {
		t.Fatal("expected rpc error")
	}
	if rpcErr.Code != 1005002 || rpcErr.Message != "ARTIFACT_NOT_FOUND" {
		t.Fatalf("expected ARTIFACT_NOT_FOUND mapping, got code=%d message=%s", rpcErr.Code, rpcErr.Message)
	}
}

func TestDispatchMapsSecurityAuditListStorageErrors(t *testing.T) {
	_, rpcErr := wrapOrchestratorResult(nil, orchestrator.ErrStorageQueryFailed)
	if rpcErr == nil {
		t.Fatal("expected rpc error")
	}
	if rpcErr.Code != 1005001 || rpcErr.Message != "SQLITE_WRITE_FAILED" {
		t.Fatalf("expected SQLITE_WRITE_FAILED mapping, got code=%d message=%s", rpcErr.Code, rpcErr.Message)
	}
}

func TestDispatchMapsWrappedStructuredStoreErrors(t *testing.T) {
	_, rpcErr := wrapOrchestratorResult(nil, fmt.Errorf("settings snapshot write failed: %w", storage.ErrStructuredStoreUnavailable))
	if rpcErr == nil {
		t.Fatal("expected rpc error")
	}
	if rpcErr.Code != 1005001 || rpcErr.Message != "SQLITE_WRITE_FAILED" {
		t.Fatalf("expected wrapped structured store error to map to SQLITE_WRITE_FAILED, got code=%d message=%s", rpcErr.Code, rpcErr.Message)
	}
}

func TestDispatchMapsRecoveryPointNotFoundErrors(t *testing.T) {
	_, rpcErr := wrapOrchestratorResult(nil, orchestrator.ErrRecoveryPointNotFound)
	if rpcErr == nil {
		t.Fatal("expected rpc error")
	}
	if rpcErr.Code != 1005006 || rpcErr.Message != "RECOVERY_POINT_NOT_FOUND" {
		t.Fatalf("expected RECOVERY_POINT_NOT_FOUND mapping, got code=%d message=%s", rpcErr.Code, rpcErr.Message)
	}
}

func TestDispatchMapsStrongholdErrors(t *testing.T) {
	_, rpcErr := wrapOrchestratorResult(nil, orchestrator.ErrStrongholdAccessFailed)
	if rpcErr == nil {
		t.Fatal("expected rpc error")
	}
	if rpcErr.Code != 1005004 || rpcErr.Message != "STRONGHOLD_ACCESS_FAILED" {
		t.Fatalf("expected STRONGHOLD_ACCESS_FAILED mapping, got code=%d message=%s", rpcErr.Code, rpcErr.Message)
	}
}

func TestDispatchMapsModelSecretErrors(t *testing.T) {
	_, rpcErr := wrapOrchestratorResult(nil, errors.Join(model.ErrSecretSourceFailed, model.ErrSecretNotFound))
	if rpcErr == nil {
		t.Fatal("expected rpc error")
	}
	if rpcErr.Code != 1005004 || rpcErr.Message != "STRONGHOLD_ACCESS_FAILED" {
		t.Fatalf("expected STRONGHOLD_ACCESS_FAILED mapping, got code=%d message=%s", rpcErr.Code, rpcErr.Message)
	}
}

func TestDispatchMapsModelClientConfigurationErrorsToStronghold(t *testing.T) {
	_, rpcErr := wrapOrchestratorResult(nil, model.ErrClientNotConfigured)
	if rpcErr == nil {
		t.Fatal("expected rpc error")
	}
	if rpcErr.Code != 1005004 || rpcErr.Message != "STRONGHOLD_ACCESS_FAILED" {
		t.Fatalf("expected STRONGHOLD_ACCESS_FAILED mapping, got code=%d message=%s", rpcErr.Code, rpcErr.Message)
	}
}

func TestDispatchMapsAPIKeyConfigurationErrorsToStronghold(t *testing.T) {
	_, rpcErr := wrapOrchestratorResult(nil, model.ErrOpenAIAPIKeyRequired)
	if rpcErr == nil {
		t.Fatal("expected rpc error")
	}
	if rpcErr.Code != 1005004 || rpcErr.Message != "STRONGHOLD_ACCESS_FAILED" {
		t.Fatalf("expected STRONGHOLD_ACCESS_FAILED mapping, got code=%d message=%s", rpcErr.Code, rpcErr.Message)
	}
}

func TestDispatchMapsModelProviderErrors(t *testing.T) {
	_, rpcErr := wrapOrchestratorResult(nil, model.ErrModelProviderUnsupported)
	if rpcErr == nil {
		t.Fatal("expected rpc error")
	}
	if rpcErr.Code != 1008001 || rpcErr.Message != "MODEL_PROVIDER_NOT_FOUND" {
		t.Fatalf("expected MODEL_PROVIDER_NOT_FOUND mapping, got code=%d message=%s", rpcErr.Code, rpcErr.Message)
	}
}

func TestDispatchMapsModelCapabilityErrors(t *testing.T) {
	_, rpcErr := wrapOrchestratorResult(nil, model.ErrToolCallingNotSupported)
	if rpcErr == nil {
		t.Fatal("expected rpc error")
	}
	if rpcErr.Code != 1008002 || rpcErr.Message != "MODEL_NOT_ALLOWED" {
		t.Fatalf("expected MODEL_NOT_ALLOWED mapping, got code=%d message=%s", rpcErr.Code, rpcErr.Message)
	}
}

func TestDispatchMapsModelConfigurationErrors(t *testing.T) {
	tests := []struct {
		name string
		err  error
	}{
		{name: "endpoint missing", err: model.ErrOpenAIEndpointRequired},
		{name: "provider rejected request", err: &model.OpenAIHTTPStatusError{StatusCode: 400, Message: "bad request"}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, rpcErr := wrapOrchestratorResult(nil, test.err)
			if rpcErr == nil {
				t.Fatal("expected rpc error")
			}
			if rpcErr.Code != 1008002 || rpcErr.Message != "MODEL_NOT_ALLOWED" {
				t.Fatalf("expected MODEL_NOT_ALLOWED mapping, got code=%d message=%s", rpcErr.Code, rpcErr.Message)
			}
		})
	}
}

func TestDispatchMapsModelRuntimeErrors(t *testing.T) {
	tests := []struct {
		name string
		err  error
	}{
		{name: "timeout", err: model.ErrOpenAIRequestTimeout},
		{name: "provider unavailable", err: &model.OpenAIHTTPStatusError{StatusCode: 503, Message: "service unavailable"}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, rpcErr := wrapOrchestratorResult(nil, test.err)
			if rpcErr == nil {
				t.Fatal("expected rpc error")
			}
			if rpcErr.Code != 1008007 || rpcErr.Message != "MODEL_RUNTIME_UNAVAILABLE" {
				t.Fatalf("expected MODEL_RUNTIME_UNAVAILABLE mapping, got code=%d message=%s", rpcErr.Code, rpcErr.Message)
			}
		})
	}
}

func TestDispatchMapsModelOutputInvalidErrors(t *testing.T) {
	_, rpcErr := wrapOrchestratorResult(nil, tools.ErrToolOutputInvalid)
	if rpcErr == nil {
		t.Fatal("expected rpc error")
	}
	if rpcErr.Code != 1003004 || rpcErr.Message != "TOOL_OUTPUT_INVALID" {
		t.Fatalf("expected TOOL_OUTPUT_INVALID mapping, got code=%d message=%s", rpcErr.Code, rpcErr.Message)
	}
}

func TestHandlerWrappersCoverRecommendationInspectorDashboardAndSecurityMethods(t *testing.T) {
	server := newTestServer()
	handlerCalls := []struct {
		name   string
		invoke func() (any, *rpcError)
	}{
		{name: "recommendation.get", invoke: func() (any, *rpcError) { return server.handleAgentRecommendationGet(map[string]any{}) }},
		{name: "recommendation.feedback.submit", invoke: func() (any, *rpcError) { return server.handleAgentRecommendationFeedbackSubmit(map[string]any{}) }},
		{name: "task_inspector.config.get", invoke: func() (any, *rpcError) { return server.handleAgentTaskInspectorConfigGet(nil) }},
		{name: "task_inspector.config.update", invoke: func() (any, *rpcError) {
			return server.handleAgentTaskInspectorConfigUpdate(map[string]any{"task_sources": []any{"D:/workspace/todos"}, "inspection_interval": map[string]any{"unit": "minute", "value": 10}})
		}},
		{name: "task_inspector.run", invoke: func() (any, *rpcError) { return server.handleAgentTaskInspectorRun(map[string]any{}) }},
		{name: "notepad.list", invoke: func() (any, *rpcError) { return server.handleAgentNotepadList(map[string]any{}) }},
		{name: "notepad.convert_to_task", invoke: func() (any, *rpcError) {
			return server.handleAgentNotepadConvertToTask(map[string]any{"item_id": "missing"})
		}},
		{name: "dashboard.overview.get", invoke: func() (any, *rpcError) { return server.handleAgentDashboardOverviewGet(map[string]any{}) }},
		{name: "dashboard.module.get", invoke: func() (any, *rpcError) { return server.handleAgentDashboardModuleGet(map[string]any{"module": "task"}) }},
		{name: "mirror.overview.get", invoke: func() (any, *rpcError) { return server.handleAgentMirrorOverviewGet(map[string]any{}) }},
		{name: "security.summary.get", invoke: func() (any, *rpcError) { return server.handleAgentSecuritySummaryGet(nil) }},
		{name: "security.pending.list", invoke: func() (any, *rpcError) { return server.handleAgentSecurityPendingList(map[string]any{}) }},
		{name: "security.respond", invoke: func() (any, *rpcError) {
			return server.handleAgentSecurityRespond(map[string]any{"task_id": "missing", "decision": "approve"})
		}},
	}
	for _, call := range handlerCalls {
		data, rpcErr := call.invoke()
		if data == nil && rpcErr == nil {
			t.Fatalf("expected %s handler to return either data or rpc error", call.name)
		}
		if rpcErr != nil && rpcErr.TraceID == "" {
			t.Fatalf("expected %s handler rpc error to include trace id, got %+v", call.name, rpcErr)
		}
	}
}

func TestJSONRPCHandlerWrappersCoverPrimitiveDecodersAndTracingHelpers(t *testing.T) {
	req := requestEnvelope{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`1`),
		Method:  "agent.settings.update",
		Params:  mustMarshal(t, map[string]any{"request_meta": map[string]any{"trace_id": "trace_rpc_helpers"}, "enabled": true, "count": 3, "labels": []string{"a", "b"}}),
	}
	decoded, err := decodeRequest(strings.NewReader(string(mustMarshal(t, req))))
	if err != nil {
		t.Fatalf("decodeRequest returned error: %v", err)
	}
	params, err := decodeParams(decoded.Params)
	if err != nil {
		t.Fatalf("decodeParams returned error: %v", err)
	}
	if requestTraceID(params) != "trace_rpc_helpers" || traceIDFromRequest(decoded.Params) != "trace_rpc_helpers" {
		t.Fatalf("expected trace helpers to extract request trace ids, params=%+v", params)
	}
	if !boolValue(params, "enabled", false) || intValue(params, "count", 0) != 3 {
		t.Fatalf("expected primitive decoders to round-trip values, params=%+v", params)
	}
	labels := stringSliceValue(params["labels"])
	if len(labels) != 2 || labels[1] != "b" {
		t.Fatalf("expected stringSliceValue to decode labels, got %+v", labels)
	}
	if boolValue(nil, "enabled", false) || intValue(nil, "count", 0) != 0 || len(stringSliceValue(map[string]any{"labels": 7}["labels"])) != 0 {
		t.Fatal("expected primitive decoders to handle nil and invalid inputs")
	}
	if _, err := decodeRequest(strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"agent.settings.get","params":`)); err == nil {
		t.Fatal("expected malformed request to fail decodeRequest")
	}
	if _, err := decodeParams(json.RawMessage(`{"broken":`)); err == nil {
		t.Fatal("expected malformed params to fail decodeParams")
	}
}
