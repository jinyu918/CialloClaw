package rpc

import (
	"errors"

	"github.com/cialloclaw/cialloclaw/services/local-service/internal/model"
	"github.com/cialloclaw/cialloclaw/services/local-service/internal/orchestrator"
	"github.com/cialloclaw/cialloclaw/services/local-service/internal/storage"
	"github.com/cialloclaw/cialloclaw/services/local-service/internal/tools"
)

// wrapOrchestratorResult maps orchestrator return values into the shared RPC
// success/error envelope.
// It does not correct business logic; it only freezes protocol-facing error
// codes, messages, and trace metadata.
func wrapOrchestratorResult(data any, err error) (any, *rpcError) {
	if err == nil {
		return data, nil
	}

	if errors.Is(err, orchestrator.ErrTaskNotFound) {
		return nil, &rpcError{
			Code:    1001001,
			Message: "TASK_NOT_FOUND",
			Detail:  err.Error(),
			TraceID: "trace_task_not_found",
		}
	}
	if errors.Is(err, orchestrator.ErrArtifactNotFound) {
		return nil, &rpcError{
			Code:    1005002,
			Message: "ARTIFACT_NOT_FOUND",
			Detail:  err.Error(),
			TraceID: "trace_artifact_not_found",
		}
	}
	if errors.Is(err, orchestrator.ErrTaskStatusInvalid) {
		return nil, &rpcError{
			Code:    1001004,
			Message: "TASK_STATUS_INVALID",
			Detail:  err.Error(),
			TraceID: "trace_task_status_invalid",
		}
	}
	if errors.Is(err, orchestrator.ErrTaskAlreadyFinished) {
		return nil, &rpcError{
			Code:    1001005,
			Message: "TASK_ALREADY_FINISHED",
			Detail:  err.Error(),
			TraceID: "trace_task_already_finished",
		}
	}
	if errors.Is(err, orchestrator.ErrStorageQueryFailed) {
		return nil, &rpcError{
			Code:    1005001,
			Message: "SQLITE_WRITE_FAILED",
			Detail:  err.Error(),
			TraceID: "trace_storage_query_failed",
		}
	}
	if errors.Is(err, storage.ErrStructuredStoreUnavailable) || errors.Is(err, storage.ErrDatabasePathRequired) {
		return nil, &rpcError{
			Code:    1005001,
			Message: "SQLITE_WRITE_FAILED",
			Detail:  err.Error(),
			TraceID: "trace_sqlite_write_failed",
		}
	}
	if errors.Is(err, orchestrator.ErrStrongholdAccessFailed) {
		return nil, &rpcError{
			Code:    1005004,
			Message: "STRONGHOLD_ACCESS_FAILED",
			Detail:  err.Error(),
			TraceID: "trace_stronghold_access_failed",
		}
	}
	if errors.Is(err, storage.ErrStrongholdAccessFailed) || errors.Is(err, storage.ErrStrongholdUnavailable) || errors.Is(err, storage.ErrSecretStoreAccessFailed) || errors.Is(err, storage.ErrSecretNotFound) || errors.Is(err, model.ErrClientNotConfigured) || errors.Is(err, model.ErrOpenAIAPIKeyRequired) || errors.Is(err, model.ErrSecretSourceFailed) || errors.Is(err, model.ErrSecretNotFound) {
		return nil, &rpcError{
			Code:    1005004,
			Message: "STRONGHOLD_ACCESS_FAILED",
			Detail:  err.Error(),
			TraceID: "trace_stronghold_access_failed",
		}
	}
	if errors.Is(err, orchestrator.ErrRecoveryPointNotFound) {
		return nil, &rpcError{
			Code:    1005006,
			Message: "RECOVERY_POINT_NOT_FOUND",
			Detail:  err.Error(),
			TraceID: "trace_recovery_point_not_found",
		}
	}
	if errors.Is(err, model.ErrModelProviderRequired) || errors.Is(err, model.ErrModelProviderUnsupported) {
		return nil, &rpcError{
			Code:    1008001,
			Message: "MODEL_PROVIDER_NOT_FOUND",
			Detail:  err.Error(),
			TraceID: "trace_model_provider_not_found",
		}
	}
	if model.IsProviderRuntimeUnavailable(err) {
		return nil, &rpcError{
			Code:    1008007,
			Message: "MODEL_RUNTIME_UNAVAILABLE",
			Detail:  err.Error(),
			TraceID: "trace_model_runtime_unavailable",
		}
	}
	if errors.Is(err, model.ErrToolCallingNotSupported) || errors.Is(err, model.ErrOpenAIEndpointRequired) || errors.Is(err, model.ErrOpenAIModelIDRequired) || errors.Is(err, model.ErrOpenAIHTTPStatus) {
		return nil, &rpcError{
			Code:    1008002,
			Message: "MODEL_NOT_ALLOWED",
			Detail:  err.Error(),
			TraceID: "trace_model_not_allowed",
		}
	}
	if errors.Is(err, tools.ErrToolOutputInvalid) {
		return nil, &rpcError{
			Code:    1003004,
			Message: "TOOL_OUTPUT_INVALID",
			Detail:  err.Error(),
			TraceID: "trace_tool_output_invalid",
		}
	}

	return nil, &rpcError{
		Code:    errInvalidParams,
		Message: "INVALID_PARAMS",
		Detail:  err.Error(),
		TraceID: "trace_orchestrator_error",
	}
}
