package rpc

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/cialloclaw/cialloclaw/services/local-service/internal/audit"
	"github.com/cialloclaw/cialloclaw/services/local-service/internal/checkpoint"
	"github.com/cialloclaw/cialloclaw/services/local-service/internal/platform"
	"github.com/cialloclaw/cialloclaw/services/local-service/internal/plugin"
	"github.com/cialloclaw/cialloclaw/services/local-service/internal/storage"
	"github.com/cialloclaw/cialloclaw/services/local-service/internal/tools"
)

func TestDispatchTaskStartIgnoresUnsupportedIntentField(t *testing.T) {
	server := newTestServer()

	response := server.dispatch(requestEnvelope{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`"req-task-start-ignore-intent"`),
		Method:  "agent.task.start",
		Params: mustMarshal(t, map[string]any{
			"session_id": "sess_ignore_intent",
			"source":     "floating_ball",
			"trigger":    "text_selected_click",
			"input": map[string]any{
				"type": "text_selection",
				"text": "select this content",
			},
			"intent": map[string]any{
				"name": "write_file",
				"arguments": map[string]any{
					"require_authorization": true,
				},
			},
		}),
	})

	success, ok := response.(successEnvelope)
	if !ok {
		t.Fatalf("expected success response envelope, got %#v", response)
	}
	task := success.Result.Data.(map[string]any)["task"].(map[string]any)
	if task["status"] != "confirming_intent" {
		t.Fatalf("expected task.start to stay in confirming_intent when intent is stripped, got %+v", task)
	}
	intentValue, ok := task["intent"].(map[string]any)
	if !ok || intentValue["name"] != "agent_loop" {
		t.Fatalf("expected task.start to rely on backend suggestion instead of request intent, got %+v", task["intent"])
	}
}

// TestHandleDebugEventsReturnsQueuedNotifications verifies that queued
// notifications can be fetched through the debug events endpoint.

func TestDispatchTaskDetailGetIncludesActiveApprovalAnchor(t *testing.T) {
	server := newTestServer()

	startResult, err := server.orchestrator.StartTask(map[string]any{
		"session_id": "sess_detail_rpc",
		"source":     "floating_ball",
		"trigger":    "hover_text_input",
		"input": map[string]any{
			"type": "text",
			"text": "rpc task detail should expose active approval anchor",
		},
		"intent": map[string]any{
			"name": "write_file",
			"arguments": map[string]any{
				"require_authorization": true,
			},
		},
	})
	if err != nil {
		t.Fatalf("start task: %v", err)
	}

	taskID := startResult["task"].(map[string]any)["task_id"].(string)
	response := server.dispatch(requestEnvelope{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`"req-task-detail-anchor"`),
		Method:  "agent.task.detail.get",
		Params: mustMarshal(t, map[string]any{
			"task_id": taskID,
		}),
	})

	success, ok := response.(successEnvelope)
	if !ok {
		t.Fatalf("expected success response envelope, got %#v", response)
	}
	data := success.Result.Data.(map[string]any)
	approvalRequest, ok := data["approval_request"].(map[string]any)
	if !ok {
		t.Fatalf("expected approval_request in rpc result, got %+v", data["approval_request"])
	}
	if approvalRequest["task_id"] != taskID {
		t.Fatalf("expected approval_request task_id %s, got %+v", taskID, approvalRequest)
	}

	securitySummary := data["security_summary"].(map[string]any)
	if numericValue(t, securitySummary["pending_authorizations"]) != 1 {
		t.Fatalf("expected pending_authorizations 1 in rpc result, got %+v", securitySummary["pending_authorizations"])
	}
	if securitySummary["latest_restore_point"] != nil {
		t.Fatalf("expected latest_restore_point nil in rpc result, got %+v", securitySummary["latest_restore_point"])
	}
}

func TestDispatchTaskDetailGetOmitsApprovalAnchorForCompletedTask(t *testing.T) {
	server := newTestServer()

	startResult, err := server.orchestrator.StartTask(map[string]any{
		"session_id": "sess_detail_rpc_done",
		"source":     "floating_ball",
		"trigger":    "hover_text_input",
		"input": map[string]any{
			"type": "text",
			"text": "rpc task detail should omit anchor for completed task",
		},
		"intent": map[string]any{
			"name": "summarize",
			"arguments": map[string]any{
				"style": "key_points",
			},
		},
	})
	if err != nil {
		t.Fatalf("start task: %v", err)
	}

	taskID := startResult["task"].(map[string]any)["task_id"].(string)
	if _, ok := server.orchestrator.RunEngine().CompleteTask(taskID, map[string]any{"type": "task_detail", "payload": map[string]any{"task_id": taskID}}, map[string]any{"task_id": taskID, "type": "result", "text": "done"}, nil); !ok {
		t.Fatal("expected runtime task completion to succeed")
	}
	response := server.dispatch(requestEnvelope{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`"req-task-detail-no-anchor"`),
		Method:  "agent.task.detail.get",
		Params: mustMarshal(t, map[string]any{
			"task_id": taskID,
		}),
	})

	success, ok := response.(successEnvelope)
	if !ok {
		t.Fatalf("expected success response envelope, got %#v", response)
	}
	data := success.Result.Data.(map[string]any)
	if data["approval_request"] != nil {
		t.Fatalf("expected approval_request to be nil for completed task, got %+v", data["approval_request"])
	}

	securitySummary := data["security_summary"].(map[string]any)
	if numericValue(t, securitySummary["pending_authorizations"]) != 0 {
		t.Fatalf("expected pending_authorizations 0 in rpc result, got %+v", securitySummary["pending_authorizations"])
	}
	if _, ok := securitySummary["latest_restore_point"].(map[string]any); !ok {
		t.Fatalf("expected latest_restore_point object for completed task, got %+v", securitySummary["latest_restore_point"])
	}
}

func TestDispatchMapsTaskControlStatusErrors(t *testing.T) {
	server := newTestServer()

	startResult, err := server.orchestrator.StartTask(map[string]any{
		"session_id": "sess_demo",
		"source":     "floating_ball",
		"trigger":    "text_selected_click",
		"input": map[string]any{
			"type": "text_selection",
			"text": "still waiting for intent confirmation",
		},
	})
	if err != nil {
		t.Fatalf("start task: %v", err)
	}

	taskID := startResult["task"].(map[string]any)["task_id"].(string)
	response := server.dispatch(requestEnvelope{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`"req-task-control-invalid"`),
		Method:  "agent.task.control",
		Params: mustMarshal(t, map[string]any{
			"task_id": taskID,
			"action":  "pause",
		}),
	})

	errEnvelope, ok := response.(errorEnvelope)
	if !ok {
		t.Fatalf("expected error response envelope, got %#v", response)
	}
	if errEnvelope.Error.Code != 1001004 || errEnvelope.Error.Message != "TASK_STATUS_INVALID" {
		t.Fatalf("expected TASK_STATUS_INVALID mapping, got code=%d message=%s", errEnvelope.Error.Code, errEnvelope.Error.Message)
	}
}

func TestDispatchMapsTaskControlFinishedErrors(t *testing.T) {
	server := newTestServer()

	startResult, err := server.orchestrator.StartTask(map[string]any{
		"session_id": "sess_demo",
		"source":     "floating_ball",
		"trigger":    "hover_text_input",
		"input": map[string]any{
			"type": "text",
			"text": "completed task for rpc error mapping",
		},
		"intent": map[string]any{
			"name": "summarize",
			"arguments": map[string]any{
				"style": "key_points",
			},
		},
	})
	if err != nil {
		t.Fatalf("start task: %v", err)
	}

	taskID := startResult["task"].(map[string]any)["task_id"].(string)
	if _, ok := server.orchestrator.RunEngine().CompleteTask(taskID, map[string]any{"type": "task_detail", "payload": map[string]any{"task_id": taskID}}, map[string]any{"task_id": taskID, "type": "result", "text": "done"}, nil); !ok {
		t.Fatal("expected runtime task completion to succeed")
	}
	response := server.dispatch(requestEnvelope{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`"req-task-control-finished"`),
		Method:  "agent.task.control",
		Params: mustMarshal(t, map[string]any{
			"task_id": taskID,
			"action":  "cancel",
		}),
	})

	errEnvelope, ok := response.(errorEnvelope)
	if !ok {
		t.Fatalf("expected error response envelope, got %#v", response)
	}
	if errEnvelope.Error.Code != 1001005 || errEnvelope.Error.Message != "TASK_ALREADY_FINISHED" {
		t.Fatalf("expected TASK_ALREADY_FINISHED mapping, got code=%d message=%s", errEnvelope.Error.Code, errEnvelope.Error.Message)
	}
}

func TestDispatchReturnsSecurityAuditList(t *testing.T) {
	server := newTestServer()
	storageService := storage.NewService(platform.NewLocalStorageAdapter(filepath.Join(t.TempDir(), "audit.db")))
	defer func() { _ = storageService.Close() }()
	server.orchestrator.WithStorage(storageService)
	err := storageService.AuditWriter().WriteAuditRecord(context.Background(), audit.Record{
		AuditID:   "audit_001",
		TaskID:    "task_001",
		Type:      "file",
		Action:    "write_file",
		Summary:   "stored audit record",
		Target:    "workspace/result.md",
		Result:    "success",
		CreatedAt: "2026-04-08T10:00:00Z",
	})
	if err != nil {
		t.Fatalf("write audit record: %v", err)
	}

	response := server.dispatch(requestEnvelope{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`"req-security-audit-list"`),
		Method:  "agent.security.audit.list",
		Params: mustMarshal(t, map[string]any{
			"task_id": "task_001",
			"limit":   20,
			"offset":  0,
		}),
	})

	success, ok := response.(successEnvelope)
	if !ok {
		t.Fatalf("expected success response envelope, got %#v", response)
	}
	items := success.Result.Data.(map[string]any)["items"].([]map[string]any)
	if len(items) != 1 {
		t.Fatalf("expected one audit item, got %d", len(items))
	}
	if items[0]["audit_id"] != "audit_001" {
		t.Fatalf("expected stored audit_001, got %+v", items[0])
	}
}

func TestDispatchReturnsSecurityRestorePointsList(t *testing.T) {
	server := newTestServer()
	storageService := storage.NewService(platform.NewLocalStorageAdapter(filepath.Join(t.TempDir(), "restore.db")))
	defer func() { _ = storageService.Close() }()
	server.orchestrator.WithStorage(storageService)
	err := storageService.RecoveryPointWriter().WriteRecoveryPoint(context.Background(), checkpoint.RecoveryPoint{
		RecoveryPointID: "rp_001",
		TaskID:          "task_001",
		Summary:         "stored recovery point",
		CreatedAt:       "2026-04-08T10:00:00Z",
		Objects:         []string{"workspace/result.md"},
	})
	if err != nil {
		t.Fatalf("write recovery point: %v", err)
	}

	response := server.dispatch(requestEnvelope{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`"req-security-restore-points-list"`),
		Method:  "agent.security.restore_points.list",
		Params: mustMarshal(t, map[string]any{
			"task_id": "task_001",
			"limit":   20,
			"offset":  0,
		}),
	})

	success, ok := response.(successEnvelope)
	if !ok {
		t.Fatalf("expected success response envelope, got %#v", response)
	}
	items := success.Result.Data.(map[string]any)["items"].([]map[string]any)
	if len(items) != 1 {
		t.Fatalf("expected one recovery point item, got %d", len(items))
	}
	if items[0]["recovery_point_id"] != "rp_001" {
		t.Fatalf("expected stored rp_001, got %+v", items[0])
	}
}

func TestDispatchReturnsSecurityRestoreApplyResult(t *testing.T) {
	server := newTestServer()
	storageService := storage.NewService(platform.NewLocalStorageAdapter(filepath.Join(t.TempDir(), "restore-apply.db")))
	defer func() { _ = storageService.Close() }()
	server.orchestrator.WithStorage(storageService)
	startResult, err := server.orchestrator.StartTask(map[string]any{
		"session_id": "sess_restore",
		"source":     "floating_ball",
		"trigger":    "text_selected_click",
		"input": map[string]any{
			"type": "text_selection",
			"text": "restore runtime task",
		},
	})
	if err != nil {
		t.Fatalf("start task: %v", err)
	}
	taskID := startResult["task"].(map[string]any)["task_id"].(string)

	err = storageService.RecoveryPointWriter().WriteRecoveryPoint(context.Background(), checkpoint.RecoveryPoint{
		RecoveryPointID: "rp_001",
		TaskID:          taskID,
		Summary:         "stored recovery point",
		CreatedAt:       "2026-04-08T10:00:00Z",
		Objects:         []string{"workspace/result.md"},
	})
	if err != nil {
		t.Fatalf("write recovery point: %v", err)
	}

	response := server.dispatch(requestEnvelope{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`"req-security-restore-apply"`),
		Method:  "agent.security.restore.apply",
		Params: mustMarshal(t, map[string]any{
			"task_id":           taskID,
			"recovery_point_id": "rp_001",
		}),
	})

	success, ok := response.(successEnvelope)
	if !ok {
		t.Fatalf("expected success response envelope, got %#v", response)
	}
	data := success.Result.Data.(map[string]any)
	if _, ok := data["applied"].(bool); !ok {
		t.Fatalf("expected applied flag in restore result, got %+v", data)
	}
	if data["task"].(map[string]any)["status"] != "waiting_auth" {
		t.Fatalf("expected restore apply rpc to enter waiting_auth, got %+v", data)
	}
	if data["recovery_point"].(map[string]any)["recovery_point_id"] != "rp_001" {
		t.Fatalf("expected rp_001 restore result, got %+v", data)
	}
}

func TestDispatchReturnsNotepadUpdateResult(t *testing.T) {
	server := newTestServer()

	response := server.dispatch(requestEnvelope{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`"req-notepad-update"`),
		Method:  "agent.notepad.update",
		Params: mustMarshal(t, map[string]any{
			"item_id": "todo_002",
			"action":  "move_upcoming",
		}),
	})

	success, ok := response.(successEnvelope)
	if !ok {
		t.Fatalf("expected success response envelope, got %#v", response)
	}

	data := success.Result.Data.(map[string]any)
	notepadItem, ok := data["notepad_item"].(map[string]any)
	if !ok {
		t.Fatalf("expected notepad_item payload, got %+v", data)
	}
	if notepadItem["bucket"] != "upcoming" {
		t.Fatalf("expected updated notepad item bucket upcoming, got %+v", notepadItem)
	}
	refreshGroups := data["refresh_groups"].([]string)
	if len(refreshGroups) != 2 || refreshGroups[0] != "later" || refreshGroups[1] != "upcoming" {
		t.Fatalf("expected refresh_groups to include source and target buckets, got %+v", refreshGroups)
	}
}

func TestDispatchReturnsTaskArtifactList(t *testing.T) {
	server := newTestServer()
	storageService := storage.NewService(platform.NewLocalStorageAdapter(filepath.Join(t.TempDir(), "artifact-list.db")))
	defer func() { _ = storageService.Close() }()
	server.orchestrator.WithStorage(storageService)
	err := storageService.ArtifactStore().SaveArtifacts(context.Background(), []storage.ArtifactRecord{{
		ArtifactID:          "art_rpc_001",
		TaskID:              "task_rpc_001",
		ArtifactType:        "generated_doc",
		Title:               "rpc-artifact.md",
		Path:                "workspace/rpc-artifact.md",
		MimeType:            "text/markdown",
		DeliveryType:        "workspace_document",
		DeliveryPayloadJSON: `{"path":"workspace/rpc-artifact.md","task_id":"task_rpc_001"}`,
		CreatedAt:           "2026-04-14T10:00:00Z",
	}})
	if err != nil {
		t.Fatalf("write artifact: %v", err)
	}
	response := server.dispatch(requestEnvelope{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`"req-task-artifact-list"`),
		Method:  "agent.task.artifact.list",
		Params: mustMarshal(t, map[string]any{
			"task_id": "task_rpc_001",
			"limit":   20,
			"offset":  0,
		}),
	})
	success, ok := response.(successEnvelope)
	if !ok {
		t.Fatalf("expected success response envelope, got %#v", response)
	}
	items := success.Result.Data.(map[string]any)["items"].([]map[string]any)
	if len(items) != 1 || items[0]["artifact_id"] != "art_rpc_001" {
		t.Fatalf("expected artifact list item, got %+v", items)
	}
}

func TestDispatchReturnsTaskArtifactOpen(t *testing.T) {
	server := newTestServer()
	storageService := storage.NewService(platform.NewLocalStorageAdapter(filepath.Join(t.TempDir(), "artifact-open.db")))
	defer func() { _ = storageService.Close() }()
	server.orchestrator.WithStorage(storageService)
	err := storageService.ArtifactStore().SaveArtifacts(context.Background(), []storage.ArtifactRecord{{
		ArtifactID:          "art_rpc_open_001",
		TaskID:              "task_rpc_open_001",
		ArtifactType:        "generated_doc",
		Title:               "rpc-open.md",
		Path:                "workspace/rpc-open.md",
		MimeType:            "text/markdown",
		DeliveryType:        "open_file",
		DeliveryPayloadJSON: `{"path":"workspace/rpc-open.md","task_id":"task_rpc_open_001"}`,
		CreatedAt:           "2026-04-14T10:05:00Z",
	}})
	if err != nil {
		t.Fatalf("write artifact: %v", err)
	}
	response := server.dispatch(requestEnvelope{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`"req-task-artifact-open"`),
		Method:  "agent.task.artifact.open",
		Params: mustMarshal(t, map[string]any{
			"task_id":     "task_rpc_open_001",
			"artifact_id": "art_rpc_open_001",
		}),
	})
	success, ok := response.(successEnvelope)
	if !ok {
		t.Fatalf("expected success response envelope, got %#v", response)
	}
	data := success.Result.Data.(map[string]any)
	if data["open_action"] != "open_file" {
		t.Fatalf("expected open_file action, got %+v", data)
	}
	if data["artifact"].(map[string]any)["artifact_id"] != "art_rpc_open_001" {
		t.Fatalf("expected opened artifact, got %+v", data)
	}
}

func TestDispatchReturnsDeliveryOpenForArtifact(t *testing.T) {
	server := newTestServer()
	storageService := storage.NewService(platform.NewLocalStorageAdapter(filepath.Join(t.TempDir(), "delivery-open-artifact.db")))
	defer func() { _ = storageService.Close() }()
	server.orchestrator.WithStorage(storageService)
	err := storageService.ArtifactStore().SaveArtifacts(context.Background(), []storage.ArtifactRecord{{
		ArtifactID:          "art_delivery_rpc_001",
		TaskID:              "task_delivery_rpc_001",
		ArtifactType:        "generated_doc",
		Title:               "delivery-rpc.md",
		Path:                "workspace/delivery-rpc.md",
		MimeType:            "text/markdown",
		DeliveryType:        "open_file",
		DeliveryPayloadJSON: `{"path":"workspace/delivery-rpc.md","task_id":"task_delivery_rpc_001"}`,
		CreatedAt:           "2026-04-14T10:10:00Z",
	}})
	if err != nil {
		t.Fatalf("write artifact: %v", err)
	}
	response := server.dispatch(requestEnvelope{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`"req-delivery-open-artifact"`),
		Method:  "agent.delivery.open",
		Params: mustMarshal(t, map[string]any{
			"task_id":     "task_delivery_rpc_001",
			"artifact_id": "art_delivery_rpc_001",
		}),
	})
	success, ok := response.(successEnvelope)
	if !ok {
		t.Fatalf("expected success response envelope, got %#v", response)
	}
	data := success.Result.Data.(map[string]any)
	if data["open_action"] != "open_file" {
		t.Fatalf("expected open_file action, got %+v", data)
	}
}

func TestDispatchReturnsDeliveryOpenForTaskResult(t *testing.T) {
	server := newTestServer()
	startResult, err := server.orchestrator.StartTask(map[string]any{
		"session_id": "sess_delivery_rpc",
		"source":     "floating_ball",
		"trigger":    "hover_text_input",
		"input": map[string]any{
			"type": "text",
			"text": "请整理成文档",
		},
		"intent": map[string]any{
			"name": "summarize",
			"arguments": map[string]any{
				"style": "key_points",
			},
		},
	})
	if err != nil {
		t.Fatalf("start task: %v", err)
	}
	taskID := startResult["task"].(map[string]any)["task_id"].(string)
	response := server.dispatch(requestEnvelope{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`"req-delivery-open-task"`),
		Method:  "agent.delivery.open",
		Params: mustMarshal(t, map[string]any{
			"task_id": taskID,
		}),
	})
	success, ok := response.(successEnvelope)
	if !ok {
		t.Fatalf("expected success response envelope, got %#v", response)
	}
	data := success.Result.Data.(map[string]any)
	if data["open_action"] != "task_detail" {
		t.Fatalf("expected task_detail action, got %+v", data)
	}
}

func TestDispatchReturnsSettingsGet(t *testing.T) {
	server := newTestServer()
	storageService := storage.NewService(testStorageAdapter{databasePath: filepath.Join(t.TempDir(), "settings-get.db")})
	defer func() { _ = storageService.Close() }()
	server.orchestrator.WithStorage(storageService)
	response := server.dispatch(requestEnvelope{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`"req-settings-get"`),
		Method:  "agent.settings.get",
		Params:  mustMarshal(t, map[string]any{"scope": "all"}),
	})
	success, ok := response.(successEnvelope)
	if !ok {
		t.Fatalf("expected success response envelope, got %#v", response)
	}
	models := success.Result.Data.(map[string]any)["settings"].(map[string]any)["models"].(map[string]any)
	credentials := models["credentials"].(map[string]any)
	if _, ok := credentials["stronghold"].(map[string]any); !ok {
		t.Fatalf("expected settings get to include stronghold status, got %+v", credentials)
	}
}

func TestDispatchReturnsSettingsUpdate(t *testing.T) {
	server := newTestServer()
	storageService := storage.NewService(testStorageAdapter{databasePath: filepath.Join(t.TempDir(), "settings-update.db")})
	defer func() { _ = storageService.Close() }()
	server.orchestrator.WithStorage(storageService)
	response := server.dispatch(requestEnvelope{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`"req-settings-update"`),
		Method:  "agent.settings.update",
		Params: mustMarshal(t, map[string]any{
			"models": map[string]any{
				"provider": "openai",
				"api_key":  "rpc-secret-key",
			},
		}),
	})
	success, ok := response.(successEnvelope)
	if !ok {
		t.Fatalf("expected success response envelope, got %#v", response)
	}
	models := success.Result.Data.(map[string]any)["effective_settings"].(map[string]any)["models"].(map[string]any)
	if models["provider_api_key_configured"] != true {
		t.Fatalf("expected settings update to mark provider key configured, got %+v", models)
	}
	if _, exists := models["api_key"]; exists {
		t.Fatalf("expected settings update response to keep api_key redacted, got %+v", models)
	}
	if success.Result.Data.(map[string]any)["apply_mode"] != "restart_required" || success.Result.Data.(map[string]any)["need_restart"] != true {
		t.Fatalf("expected model settings update to require restart, got %+v", success.Result.Data)
	}
}

func TestDispatchReturnsPluginRuntimeList(t *testing.T) {
	server := newTestServer()
	response := server.dispatch(requestEnvelope{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`"req-plugin-runtime-list"`),
		Method:  "agent.plugin.runtime.list",
		Params:  mustMarshal(t, map[string]any{}),
	})
	success, ok := response.(successEnvelope)
	if !ok {
		t.Fatalf("expected success response envelope, got %#v", response)
	}
	data := success.Result.Data.(map[string]any)
	items := data["items"].([]map[string]any)
	if len(items) == 0 {
		t.Fatalf("expected plugin runtime query to return declared runtimes, got %+v", data)
	}
	manifest, ok := items[0]["manifest"].(map[string]any)
	if !ok || manifest["plugin_id"] == nil || manifest["source"] == nil {
		t.Fatalf("expected plugin runtime items to include formal manifest linkage, got %+v", items[0])
	}
	metrics := data["metrics"].([]map[string]any)
	if len(metrics) == 0 {
		t.Fatalf("expected plugin runtime query to return metric snapshots, got %+v", data)
	}
}

func TestDispatchReturnsPluginList(t *testing.T) {
	server := newTestServer()
	response := server.dispatch(requestEnvelope{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`"req-plugin-list"`),
		Method:  "agent.plugin.list",
		Params: mustMarshal(t, map[string]any{
			"query": "ocr",
			"page":  map[string]any{"limit": 10, "offset": 0},
		}),
	})
	success, ok := response.(successEnvelope)
	if !ok {
		t.Fatalf("expected success response envelope, got %#v", response)
	}
	data := success.Result.Data.(map[string]any)
	items := data["items"].([]map[string]any)
	if len(items) != 1 || items[0]["plugin_id"] != "ocr" {
		t.Fatalf("expected plugin list query to return filtered ocr plugin, got %+v", data)
	}
}

func TestDispatchReturnsPluginDetail(t *testing.T) {
	server, toolRegistry, pluginService := newTestServerWithDependencies(nil)
	response := server.dispatch(requestEnvelope{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`"req-plugin-detail"`),
		Method:  "agent.plugin.detail.get",
		Params: mustMarshal(t, map[string]any{
			"plugin_id": "ocr",
		}),
	})
	success, ok := response.(successEnvelope)
	if !ok {
		t.Fatalf("expected success response envelope, got %#v", response)
	}
	data := success.Result.Data.(map[string]any)
	if data["plugin"].(map[string]any)["plugin_id"] != "ocr" {
		t.Fatalf("expected plugin detail query to resolve ocr plugin, got %+v", data)
	}
	runtime, ok := pluginService.RuntimeState(plugin.RuntimeKindWorker, "ocr_worker")
	if !ok {
		t.Fatalf("expected ocr worker runtime to exist")
	}
	toolItems := data["tools"].([]map[string]any)
	if len(toolItems) != len(runtime.Capabilities) {
		t.Fatalf("expected plugin detail query to return one contract per declared capability, got %+v", data)
	}
	for _, item := range toolItems {
		toolName := item["tool_name"].(string)
		tool, err := toolRegistry.Get(toolName)
		if err != nil {
			t.Fatalf("expected tool %q to exist in registry: %v", toolName, err)
		}
		metadata := tool.Metadata()
		if item["display_name"] != metadata.DisplayName || item["source"] != string(metadata.Source) {
			t.Fatalf("expected plugin detail payload to mirror registry metadata for %q, got %+v", toolName, item)
		}
	}
}

func TestDispatchMapsTaskControlInvalidActionToInvalidParams(t *testing.T) {
	server := newTestServer()

	startResult, err := server.orchestrator.StartTask(map[string]any{
		"session_id": "sess_demo",
		"source":     "floating_ball",
		"trigger":    "text_selected_click",
		"input": map[string]any{
			"type": "text_selection",
			"text": "task for invalid action",
		},
	})
	if err != nil {
		t.Fatalf("start task: %v", err)
	}

	taskID := startResult["task"].(map[string]any)["task_id"].(string)
	response := server.dispatch(requestEnvelope{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`"req-task-control-invalid-action"`),
		Method:  "agent.task.control",
		Params: mustMarshal(t, map[string]any{
			"task_id": taskID,
			"action":  "skip",
		}),
	})

	errEnvelope, ok := response.(errorEnvelope)
	if !ok {
		t.Fatalf("expected error response envelope, got %#v", response)
	}
	if errEnvelope.Error.Code != 1002001 || errEnvelope.Error.Message != "INVALID_PARAMS" {
		t.Fatalf("expected INVALID_PARAMS mapping for unsupported action, got code=%d message=%s", errEnvelope.Error.Code, errEnvelope.Error.Message)
	}
}

func TestDispatchMapsTaskControlMissingTaskIDToInvalidParams(t *testing.T) {
	server := newTestServer()

	response := server.dispatch(requestEnvelope{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`"req-task-control-missing-task-id"`),
		Method:  "agent.task.control",
		Params: mustMarshal(t, map[string]any{
			"action": "pause",
		}),
	})

	errEnvelope, ok := response.(errorEnvelope)
	if !ok {
		t.Fatalf("expected error response envelope, got %#v", response)
	}
	if errEnvelope.Error.Code != 1002001 || errEnvelope.Error.Message != "INVALID_PARAMS" {
		t.Fatalf("expected INVALID_PARAMS mapping for missing task_id, got code=%d message=%s", errEnvelope.Error.Code, errEnvelope.Error.Message)
	}
}

func TestDispatchMapsTaskControlMissingActionToInvalidParams(t *testing.T) {
	server := newTestServer()

	startResult, err := server.orchestrator.StartTask(map[string]any{
		"session_id": "sess_demo",
		"source":     "floating_ball",
		"trigger":    "hover_text_input",
		"input": map[string]any{
			"type": "text",
			"text": "task control rpc validation",
		},
		"intent": map[string]any{
			"name": "write_file",
			"arguments": map[string]any{
				"require_authorization": true,
			},
		},
	})
	if err != nil {
		t.Fatalf("start task: %v", err)
	}

	taskID := startResult["task"].(map[string]any)["task_id"].(string)
	response := server.dispatch(requestEnvelope{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`"req-task-control-missing-action"`),
		Method:  "agent.task.control",
		Params: mustMarshal(t, map[string]any{
			"task_id": taskID,
		}),
	})

	errEnvelope, ok := response.(errorEnvelope)
	if !ok {
		t.Fatalf("expected error response envelope, got %#v", response)
	}
	if errEnvelope.Error.Code != 1002001 || errEnvelope.Error.Message != "INVALID_PARAMS" {
		t.Fatalf("expected INVALID_PARAMS mapping for missing action, got code=%d message=%s", errEnvelope.Error.Code, errEnvelope.Error.Message)
	}
}

func TestDispatchTaskListClampsPagingParams(t *testing.T) {
	server := newTestServer()

	for index := 0; index < 25; index++ {
		_, err := server.orchestrator.StartTask(map[string]any{
			"session_id": fmt.Sprintf("sess_rpc_task_list_%02d", index),
			"source":     "floating_ball",
			"trigger":    "hover_text_input",
			"input": map[string]any{
				"type": "text",
				"text": fmt.Sprintf("rpc task list clamp %02d", index),
			},
			"intent": map[string]any{
				"name": "write_file",
				"arguments": map[string]any{
					"require_authorization": true,
				},
			},
		})
		if err != nil {
			t.Fatalf("start task %d: %v", index, err)
		}
	}

	response := server.dispatch(requestEnvelope{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`"req-task-list-clamp"`),
		Method:  "agent.task.list",
		Params: mustMarshal(t, map[string]any{
			"group":      "unfinished",
			"limit":      0,
			"offset":     -5,
			"sort_by":    "updated_at",
			"sort_order": "desc",
		}),
	})

	success, ok := response.(successEnvelope)
	if !ok {
		t.Fatalf("expected success response envelope, got %#v", response)
	}
	data := success.Result.Data.(map[string]any)
	items := data["items"].([]map[string]any)
	if len(items) != 20 {
		t.Fatalf("expected rpc task.list to clamp zero limit to 20 items, got %d", len(items))
	}
	page := data["page"].(map[string]any)
	if numericValue(t, page["limit"]) != 20 {
		t.Fatalf("expected clamped rpc page limit 20, got %+v", page)
	}
	if numericValue(t, page["offset"]) != 0 {
		t.Fatalf("expected clamped rpc page offset 0, got %+v", page)
	}
	if page["has_more"] != true {
		t.Fatalf("expected rpc page has_more to remain true after clamping, got %+v", page)
	}
	if numericValue(t, page["total"]) != 25 {
		t.Fatalf("expected rpc page total 25, got %+v", page)
	}
}

func TestDispatchTaskEventsListReturnsLoopEvents(t *testing.T) {
	server := newTestServer()
	storageService := storage.NewService(testStorageAdapter{databasePath: filepath.Join(t.TempDir(), "rpc-loop-events.db")})
	defer func() { _ = storageService.Close() }()
	server.orchestrator.WithStorage(storageService)
	if err := storageService.LoopRuntimeStore().SaveEvents(context.Background(), []storage.EventRecord{{
		EventID:     "evt_rpc_loop_001",
		RunID:       "run_rpc_loop_001",
		TaskID:      "task_rpc_loop_001",
		StepID:      "step_rpc_loop_001",
		Type:        "loop.completed",
		Level:       "info",
		PayloadJSON: `{"stop_reason":"completed"}`,
		CreatedAt:   "2026-04-17T10:00:00Z",
	}}); err != nil {
		t.Fatalf("save loop events failed: %v", err)
	}

	response := server.dispatch(requestEnvelope{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`"req-task-events-list"`),
		Method:  "agent.task.events.list",
		Params: mustMarshal(t, map[string]any{
			"task_id": "task_rpc_loop_001",
			"run_id":  "run_rpc_loop_001",
			"type":    "loop.completed",
			"limit":   20,
			"offset":  0,
		}),
	})

	success, ok := response.(successEnvelope)
	if !ok {
		t.Fatalf("expected success response envelope, got %#v", response)
	}
	data := success.Result.Data.(map[string]any)
	items := data["items"].([]map[string]any)
	if len(items) != 1 || items[0]["type"] != "loop.completed" {
		t.Fatalf("expected rpc task events list to return loop.completed, got %+v", items)
	}
}

func TestDispatchTaskToolCallsListReturnsPersistedToolCalls(t *testing.T) {
	server := newTestServer()
	storageService := storage.NewService(testStorageAdapter{databasePath: filepath.Join(t.TempDir(), "rpc-tool-calls.db")})
	defer func() { _ = storageService.Close() }()
	server.orchestrator.WithStorage(storageService)
	if err := storageService.ToolCallStore().SaveToolCall(context.Background(), tools.ToolCallRecord{
		ToolCallID: "tool_call_rpc_001",
		RunID:      "run_rpc_tool_001",
		TaskID:     "task_rpc_tool_001",
		ToolName:   "read_file",
		Status:     tools.ToolCallStatusSucceeded,
		Input:      map[string]any{"path": "notes/source.txt"},
		Output:     map[string]any{"path": "notes/source.txt"},
		DurationMS: 9,
	}); err != nil {
		t.Fatalf("save tool call failed: %v", err)
	}

	response := server.dispatch(requestEnvelope{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`"req-task-tool-calls-list"`),
		Method:  "agent.task.tool_calls.list",
		Params: mustMarshal(t, map[string]any{
			"task_id": "task_rpc_tool_001",
			"limit":   20,
			"offset":  0,
		}),
	})

	success, ok := response.(successEnvelope)
	if !ok {
		t.Fatalf("expected success response envelope, got %#v", response)
	}
	data := success.Result.Data.(map[string]any)
	items := data["items"].([]map[string]any)
	if len(items) != 1 || items[0]["tool_name"] != "read_file" {
		t.Fatalf("expected rpc task tool calls list to return read_file, got %+v", items)
	}
}

func TestDispatchTaskSteerReturnsUpdatedTask(t *testing.T) {
	server := newTestServer()
	startResult, err := server.orchestrator.StartTask(map[string]any{
		"session_id": "sess_rpc_task_steer",
		"source":     "floating_ball",
		"trigger":    "hover_text_input",
		"input": map[string]any{
			"type": "text",
			"text": "Please write this into a file after authorization.",
		},
		"intent": map[string]any{
			"name": "write_file",
			"arguments": map[string]any{
				"require_authorization": true,
			},
		},
	})
	if err != nil {
		t.Fatalf("start task: %v", err)
	}
	taskID := startResult["task"].(map[string]any)["task_id"].(string)

	response := server.dispatch(requestEnvelope{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`"req-task-steer"`),
		Method:  "agent.task.steer",
		Params: mustMarshal(t, map[string]any{
			"task_id": taskID,
			"message": "Also include a short summary section.",
		}),
	})

	success, ok := response.(successEnvelope)
	if !ok {
		t.Fatalf("expected success response envelope, got %#v", response)
	}
	data := success.Result.Data.(map[string]any)
	if data["task"].(map[string]any)["task_id"] != taskID {
		t.Fatalf("expected rpc task steer to keep task id, got %+v", data)
	}
}
