package orchestrator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	serviceconfig "github.com/cialloclaw/cialloclaw/services/local-service/internal/config"
	contextsvc "github.com/cialloclaw/cialloclaw/services/local-service/internal/context"
	"github.com/cialloclaw/cialloclaw/services/local-service/internal/delivery"
	"github.com/cialloclaw/cialloclaw/services/local-service/internal/intent"
	"github.com/cialloclaw/cialloclaw/services/local-service/internal/memory"
	"github.com/cialloclaw/cialloclaw/services/local-service/internal/model"
	"github.com/cialloclaw/cialloclaw/services/local-service/internal/plugin"
	"github.com/cialloclaw/cialloclaw/services/local-service/internal/risk"
	"github.com/cialloclaw/cialloclaw/services/local-service/internal/runengine"
	"github.com/cialloclaw/cialloclaw/services/local-service/internal/tools"
)

// TestServiceStartTaskAndConfirmFlow verifies that a confirmed standard task
// continues execution and completes delivery.
func TestServiceStartTaskAndConfirmFlow(t *testing.T) {
	service := NewService(
		contextsvc.NewService(),
		intent.NewService(),
		runengine.NewEngine(),
		delivery.NewService(),
		memory.NewService(),
		risk.NewService(),
		model.NewService(modelConfig()),
		tools.NewRegistry(),
		plugin.NewService(),
	)

	startResult, err := service.StartTask(map[string]any{
		"session_id": "sess_demo",
		"source":     "floating_ball",
		"trigger":    "text_selected_click",
		"input": map[string]any{
			"type": "text_selection",
			"text": "这里是一段需要解释的内容",
		},
	})
	if err != nil {
		t.Fatalf("start task failed: %v", err)
	}

	startedTask := startResult["task"].(map[string]any)
	if startedTask["status"] != "confirming_intent" {
		t.Fatalf("expected confirming_intent status, got %v", startedTask["status"])
	}

	taskID := startedTask["task_id"].(string)
	confirmResult, err := service.ConfirmTask(map[string]any{
		"task_id":   taskID,
		"confirmed": true,
	})
	if err != nil {
		t.Fatalf("confirm task failed: %v", err)
	}

	confirmedTask := confirmResult["task"].(map[string]any)
	if confirmedTask["status"] != "completed" {
		t.Fatalf("expected completed status after confirmation, got %v", confirmedTask["status"])
	}

	deliveryResult, ok := confirmResult["delivery_result"].(map[string]any)
	if !ok {
		t.Fatal("expected confirmation flow to return delivery_result")
	}
	if deliveryResult["type"] != "bubble" {
		t.Fatalf("expected explain intent to deliver by bubble, got %v", deliveryResult["type"])
	}

	record, ok := service.runEngine.GetTask(taskID)
	if !ok {
		t.Fatal("expected confirmed task to remain available in runtime")
	}
	if record.Status != "completed" {
		t.Fatalf("expected runtime task to be completed, got %s", record.Status)
	}
	if len(record.MemoryWritePlans) == 0 {
		t.Fatal("expected confirmation flow to attach memory write plans")
	}
	if record.DeliveryResult == nil {
		t.Fatal("expected confirmation flow to persist delivery result")
	}
}

func TestServiceSubmitInputKeepsUnknownShortTextInIntentConfirmation(t *testing.T) {
	service := newTestService()

	result, err := service.SubmitInput(map[string]any{
		"session_id": "sess_unknown_text",
		"source":     "floating_ball",
		"trigger":    "hover_text_input",
		"input": map[string]any{
			"type": "text",
			"text": "你好",
		},
	})
	if err != nil {
		t.Fatalf("submit input failed: %v", err)
	}

	task := result["task"].(map[string]any)
	if task["status"] != "confirming_intent" {
		t.Fatalf("expected unknown short text to remain in confirming_intent, got %v", task["status"])
	}
	intentValue, ok := task["intent"].(map[string]any)
	if !ok || len(intentValue) != 0 {
		t.Fatalf("expected unknown short text task to keep empty intent payload, got %+v", task["intent"])
	}
	bubble := result["bubble_message"].(map[string]any)
	if bubble["text"] != "我还不确定你想如何处理这段内容，请确认目标。" {
		t.Fatalf("expected neutral confirmation prompt, got %v", bubble["text"])
	}
	if result["delivery_result"] != nil {
		t.Fatalf("expected no delivery result before intent is confirmed, got %+v", result["delivery_result"])
	}
	if _, ok := service.runEngine.GetTask(task["task_id"].(string)); !ok {
		t.Fatal("expected task to remain available in runtime")
	}
}

func TestServiceSubmitInputRoutesClearCommandToAgentLoopWithoutForcedConfirmation(t *testing.T) {
	service, _ := newTestServiceWithExecution(t, "Translated note ready.")

	result, err := service.SubmitInput(map[string]any{
		"session_id": "sess_clear_command",
		"source":     "floating_ball",
		"trigger":    "hover_text_input",
		"input": map[string]any{
			"type": "text",
			"text": "Translate this note into English",
		},
	})
	if err != nil {
		t.Fatalf("submit input failed: %v", err)
	}

	task := result["task"].(map[string]any)
	if task["status"] != "completed" {
		t.Fatalf("expected clear command to execute directly, got %v", task["status"])
	}
	intentValue, ok := task["intent"].(map[string]any)
	if !ok || intentValue["name"] != "agent_loop" {
		t.Fatalf("expected clear command to route through agent_loop, got %+v", task["intent"])
	}
	deliveryResult, ok := result["delivery_result"].(map[string]any)
	if !ok {
		t.Fatal("expected direct command to return delivery_result")
	}
	if deliveryResult["type"] != "bubble" {
		t.Fatalf("expected short command to prefer bubble delivery, got %v", deliveryResult["type"])
	}
}

func TestServiceSubmitInputUsesConfiguredOpenAIResponsesClient(t *testing.T) {
	type capturedRequest struct {
		Model      string        `json:"model"`
		Input      string        `json:"input"`
		Tools      []interface{} `json:"tools"`
		ToolChoice string        `json:"tool_choice"`
	}

	var captured capturedRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		if got := r.Header.Get("Authorization"); got != "Bearer formal-mainline-key" {
			t.Fatalf("authorization header mismatch: got %q", got)
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read request body: %v", err)
		}
		if err := json.Unmarshal(body, &captured); err != nil {
			t.Fatalf("parse request body: %v", err)
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"resp_formal_mainline","model":"gpt-5.4","output_text":"Configured Responses output.","usage":{"input_tokens":5,"output_tokens":7,"total_tokens":12}}`))
	}))
	defer server.Close()

	modelService, err := model.NewServiceFromConfig(model.ServiceConfig{
		ModelConfig: serviceconfig.ModelConfig{
			Provider: model.OpenAIResponsesProvider,
			ModelID:  "gpt-5.4",
			Endpoint: server.URL,
		},
		APIKey: "formal-mainline-key",
	})
	if err != nil {
		t.Fatalf("new model service from config: %v", err)
	}

	service, _, _ := newTestServiceWithModelService(t, modelService)
	result, err := service.SubmitInput(map[string]any{
		"session_id": "sess_formal_mainline",
		"source":     "floating_ball",
		"trigger":    "hover_text_input",
		"input": map[string]any{
			"type": "text",
			"text": "Translate this note into English",
		},
	})
	if err != nil {
		t.Fatalf("submit input failed: %v", err)
	}
	task := result["task"].(map[string]any)
	if task["status"] != "completed" {
		t.Fatalf("expected configured model path to complete task, got %+v", task)
	}
	if captured.Model != "gpt-5.4" {
		t.Fatalf("expected configured model id to reach responses client, got %+v", captured)
	}
	if captured.ToolChoice != "auto" || len(captured.Tools) == 0 {
		t.Fatalf("expected configured responses request to include tool-calling metadata, got %+v", captured)
	}
	if !strings.Contains(captured.Input, "Translate this note into English") {
		t.Fatalf("expected user request to reach responses client, got %+v", captured)
	}
	deliveryResult, ok := result["delivery_result"].(map[string]any)
	if !ok || deliveryResult["type"] != "bubble" {
		t.Fatalf("expected configured model path to return bubble delivery, got %+v", result["delivery_result"])
	}
}

func TestServiceSubmitInputUsesSuggestedWorkspaceDeliveryForLongAgentLoopInput(t *testing.T) {
	service, workspaceRoot := newTestServiceWithExecution(t, "Long-form result body.")

	result, err := service.SubmitInput(map[string]any{
		"session_id": "sess_long_command",
		"source":     "floating_ball",
		"trigger":    "hover_text_input",
		"input": map[string]any{
			"type": "text",
			"text": "Please review the following document notes and prepare a detailed deliverable:\nLine one explains the rollout plan.\nLine two adds implementation details.\nLine three adds follow-up tasks.",
		},
	})
	if err != nil {
		t.Fatalf("submit input failed: %v", err)
	}

	deliveryResult, ok := result["delivery_result"].(map[string]any)
	if !ok {
		t.Fatal("expected long direct command to return delivery_result")
	}
	if deliveryResult["type"] != "workspace_document" {
		t.Fatalf("expected long agent loop command to prefer workspace_document, got %v", deliveryResult["type"])
	}
	payload := deliveryResult["payload"].(map[string]any)
	outputPath := payload["path"].(string)
	if outputPath == "" {
		t.Fatal("expected workspace delivery to carry a path")
	}
	if _, err := os.Stat(filepath.Join(workspaceRoot, strings.TrimPrefix(outputPath, "workspace/"))); err != nil {
		t.Fatalf("expected workspace delivery file to exist, got %v", err)
	}
}

func TestServiceSubmitInputQueuesDirectAgentLoopTaskBehindSameSessionWork(t *testing.T) {
	service, _ := newTestServiceWithExecution(t, "Queued task output.")

	firstResult, err := service.StartTask(map[string]any{
		"session_id": "sess_serial",
		"source":     "floating_ball",
		"trigger":    "hover_text_input",
		"input": map[string]any{
			"type": "text",
			"text": "Please write this into a file after authorization.",
		},
		"intent": map[string]any{
			"name": "write_file",
			"arguments": map[string]any{
				"target_path":           "workspace_document",
				"require_authorization": true,
			},
		},
	})
	if err != nil {
		t.Fatalf("first start task failed: %v", err)
	}
	if firstResult["task"].(map[string]any)["status"] != "waiting_auth" {
		t.Fatalf("expected first task to wait for authorization, got %+v", firstResult["task"])
	}

	secondResult, err := service.SubmitInput(map[string]any{
		"session_id": "sess_serial",
		"source":     "floating_ball",
		"trigger":    "hover_text_input",
		"input": map[string]any{
			"type": "text",
			"text": "Translate this note into English",
		},
	})
	if err != nil {
		t.Fatalf("second submit input failed: %v", err)
	}

	secondTask := secondResult["task"].(map[string]any)
	if secondTask["status"] != "blocked" {
		t.Fatalf("expected second task to be queued as blocked, got %+v", secondTask)
	}
	if secondTask["current_step"] != "session_queue" {
		t.Fatalf("expected queued task current_step=session_queue, got %+v", secondTask)
	}
	if secondResult["delivery_result"] != nil {
		t.Fatalf("expected queued task not to return delivery_result yet, got %+v", secondResult["delivery_result"])
	}
}

func TestServiceConfirmTaskQueuesCorrectedTaskBehindSameSessionWork(t *testing.T) {
	service, _ := newTestServiceWithExecution(t, "Queued confirm output.")

	firstResult, err := service.StartTask(map[string]any{
		"session_id": "sess_confirm_queue",
		"source":     "floating_ball",
		"trigger":    "hover_text_input",
		"input": map[string]any{
			"type": "text",
			"text": "Please write this into a file after authorization.",
		},
		"intent": map[string]any{
			"name": "write_file",
			"arguments": map[string]any{
				"target_path":           "workspace_document",
				"require_authorization": true,
			},
		},
	})
	if err != nil {
		t.Fatalf("first start task failed: %v", err)
	}
	if firstResult["task"].(map[string]any)["status"] != "waiting_auth" {
		t.Fatalf("expected first task to wait for authorization, got %+v", firstResult["task"])
	}

	secondResult, err := service.SubmitInput(map[string]any{
		"session_id": "sess_confirm_queue",
		"source":     "floating_ball",
		"trigger":    "hover_text_input",
		"input": map[string]any{
			"type": "text",
			"text": "ok",
		},
	})
	if err != nil {
		t.Fatalf("second submit input failed: %v", err)
	}
	secondTaskID := secondResult["task"].(map[string]any)["task_id"].(string)

	confirmResult, err := service.ConfirmTask(map[string]any{
		"task_id":   secondTaskID,
		"confirmed": false,
		"corrected_intent": map[string]any{
			"name":      "agent_loop",
			"arguments": map[string]any{},
		},
	})
	if err != nil {
		t.Fatalf("confirm task failed: %v", err)
	}
	confirmedTask := confirmResult["task"].(map[string]any)
	if confirmedTask["status"] != "blocked" || confirmedTask["current_step"] != "session_queue" {
		t.Fatalf("expected corrected task to queue behind active session work, got %+v", confirmedTask)
	}
	if confirmResult["delivery_result"] != nil {
		t.Fatalf("expected queued corrected task not to return delivery_result, got %+v", confirmResult["delivery_result"])
	}
}

func TestServiceConfirmTaskRejectsUnknownIntentWithoutCorrection(t *testing.T) {
	service := newTestService()

	startResult, err := service.SubmitInput(map[string]any{
		"session_id": "sess_unknown_confirm",
		"source":     "floating_ball",
		"trigger":    "hover_text_input",
		"input": map[string]any{
			"type": "text",
			"text": "你好",
		},
	})
	if err != nil {
		t.Fatalf("submit input failed: %v", err)
	}

	taskID := startResult["task"].(map[string]any)["task_id"].(string)
	confirmResult, err := service.ConfirmTask(map[string]any{
		"task_id":   taskID,
		"confirmed": true,
	})
	if err != nil {
		t.Fatalf("confirm task failed: %v", err)
	}

	task := confirmResult["task"].(map[string]any)
	if task["status"] != "confirming_intent" {
		t.Fatalf("expected task to remain in confirming_intent when no corrected intent is provided, got %v", task["status"])
	}
	bubble := confirmResult["bubble_message"].(map[string]any)
	if bubble["text"] != "请先明确告诉我你希望执行的处理方式。" {
		t.Fatalf("expected clarification bubble, got %v", bubble["text"])
	}
	if confirmResult["delivery_result"] != nil {
		t.Fatalf("expected no delivery result while intent is still missing, got %+v", confirmResult["delivery_result"])
	}
}

func TestServiceConfirmTaskKeepsUnknownIntentInConfirmationWhenRejected(t *testing.T) {
	service := newTestService()

	startResult, err := service.SubmitInput(map[string]any{
		"session_id": "sess_unknown_cancel",
		"source":     "floating_ball",
		"trigger":    "hover_text_input",
		"input": map[string]any{
			"type": "text",
			"text": "你好",
		},
	})
	if err != nil {
		t.Fatalf("submit input failed: %v", err)
	}

	taskID := startResult["task"].(map[string]any)["task_id"].(string)
	confirmResult, err := service.ConfirmTask(map[string]any{
		"task_id":   taskID,
		"confirmed": false,
	})
	if err != nil {
		t.Fatalf("confirm task failed: %v", err)
	}

	task := confirmResult["task"].(map[string]any)
	if task["status"] != "confirming_intent" {
		t.Fatalf("expected rejected unknown intent task to remain in confirming_intent, got %v", task["status"])
	}
	if task["intent"] != nil {
		intentValue, ok := task["intent"].(map[string]any)
		if !ok || len(intentValue) != 0 {
			t.Fatalf("expected rejected unknown intent task to clear its current intent, got %+v", task["intent"])
		}
	}
	bubble := confirmResult["bubble_message"].(map[string]any)
	if bubble["text"] != "这不是我该做的处理方式。请重新说明你的目标，或给我一个更准确的处理意图。" {
		t.Fatalf("expected reconfirm bubble, got %v", bubble["text"])
	}
	if confirmResult["delivery_result"] != nil {
		t.Fatalf("expected rejected unknown intent task not to return delivery_result, got %+v", confirmResult["delivery_result"])
	}
}

func TestServiceConfirmTaskRewritesPlaceholderTitleAfterCorrection(t *testing.T) {
	service := newTestService()

	startResult, err := service.SubmitInput(map[string]any{
		"session_id": "sess_unknown_title",
		"source":     "floating_ball",
		"trigger":    "hover_text_input",
		"input": map[string]any{
			"type": "text",
			"text": "你好",
		},
	})
	if err != nil {
		t.Fatalf("submit input failed: %v", err)
	}

	taskID := startResult["task"].(map[string]any)["task_id"].(string)
	confirmResult, err := service.ConfirmTask(map[string]any{
		"task_id":   taskID,
		"confirmed": false,
		"corrected_intent": map[string]any{
			"name":      "translate",
			"arguments": map[string]any{"target_language": "en"},
		},
	})
	if err != nil {
		t.Fatalf("confirm task failed: %v", err)
	}

	task := confirmResult["task"].(map[string]any)
	if task["title"] != "翻译：你好" {
		t.Fatalf("expected corrected intent to rewrite placeholder title, got %v", task["title"])
	}
}

func TestServiceConfirmTaskIgnoresCorrectedIntentWhenConfirmedTrue(t *testing.T) {
	service, _ := newTestServiceWithExecution(t, "Explained content.")

	startResult, err := service.StartTask(map[string]any{
		"session_id": "sess_confirm_ignore_correction",
		"source":     "floating_ball",
		"trigger":    "text_selected_click",
		"input": map[string]any{
			"type": "text_selection",
			"text": "这里是一段需要解释的内容",
		},
	})
	if err != nil {
		t.Fatalf("start task failed: %v", err)
	}
	startTask := startResult["task"].(map[string]any)

	taskID := startTask["task_id"].(string)
	confirmResult, err := service.ConfirmTask(map[string]any{
		"task_id":   taskID,
		"confirmed": true,
		"corrected_intent": map[string]any{
			"name": "translate",
			"arguments": map[string]any{
				"target_language": "en",
			},
		},
	})
	if err != nil {
		t.Fatalf("confirm task failed: %v", err)
	}

	task := confirmResult["task"].(map[string]any)
	intentValue, ok := task["intent"].(map[string]any)
	if !ok || !reflect.DeepEqual(intentValue, startTask["intent"]) {
		t.Fatalf("expected confirm=true to keep the original task intent, got %+v", task["intent"])
	}
	if task["title"] != startTask["title"] {
		t.Fatalf("expected confirm=true to keep the original title, got %v", task["title"])
	}
}

// TestServiceConfirmTaskRejectsOutOfPhaseRequest ensures stale confirm requests
// cannot rewrite tasks that already moved beyond the confirmation phase.

// TestServiceConfirmTaskRejectsOutOfPhaseRequest ensures stale confirm requests
// cannot rewrite tasks that already moved beyond the confirmation phase.
func TestServiceConfirmTaskRejectsOutOfPhaseRequest(t *testing.T) {
	service := newTestService()

	startResult, err := service.StartTask(map[string]any{
		"session_id": "sess_confirm_out_of_phase",
		"source":     "floating_ball",
		"trigger":    "text_selected_click",
		"input": map[string]any{
			"type": "text_selection",
			"text": "请生成一个文件版本",
		},
	})
	if err != nil {
		t.Fatalf("start task failed: %v", err)
	}

	taskID := startResult["task"].(map[string]any)["task_id"].(string)
	_, err = service.ConfirmTask(map[string]any{
		"task_id":   taskID,
		"confirmed": false,
		"corrected_intent": map[string]any{
			"name": "write_file",
			"arguments": map[string]any{
				"require_authorization": true,
				"target_path":           "workspace_document",
			},
		},
	})
	if err != nil {
		t.Fatalf("seed confirm task failed: %v", err)
	}

	recordedTask, ok := service.runEngine.GetTask(taskID)
	if !ok {
		t.Fatal("expected seeded task to remain available")
	}
	originalTitle := recordedTask.Title
	originalIntent := cloneMap(recordedTask.Intent)

	_, err = service.ConfirmTask(map[string]any{
		"task_id":   taskID,
		"confirmed": false,
	})
	if !errors.Is(err, ErrTaskStatusInvalid) {
		t.Fatalf("expected out-of-phase confirm to return ErrTaskStatusInvalid, got %v", err)
	}

	recordedTask, ok = service.runEngine.GetTask(taskID)
	if !ok {
		t.Fatal("expected task to remain available after rejected confirm")
	}
	if recordedTask.Title != originalTitle {
		t.Fatalf("expected out-of-phase confirm not to rewrite title, got %q want %q", recordedTask.Title, originalTitle)
	}
	if !reflect.DeepEqual(recordedTask.Intent, originalIntent) {
		t.Fatalf("expected out-of-phase confirm not to rewrite intent, got %+v want %+v", recordedTask.Intent, originalIntent)
	}
}

func TestServiceSubmitInputWithFilesDoesNotWaitForInput(t *testing.T) {
	service := newTestService()

	result, err := service.SubmitInput(map[string]any{
		"session_id": "sess_files",
		"source":     "floating_ball",
		"input": map[string]any{
			"files": []any{"workspace/notes.md"},
		},
		"context": map[string]any{
			"page": map[string]any{
				"title":    "Workspace",
				"app_name": "desktop",
			},
		},
	})
	if err != nil {
		t.Fatalf("submit input failed: %v", err)
	}

	task := result["task"].(map[string]any)
	if task["status"] == "waiting_input" {
		t.Fatalf("expected file input to enter task flow instead of waiting_input, got %+v", task)
	}
	if task["source_type"] != "dragged_file" {
		t.Fatalf("expected file input to map to dragged_file source_type, got %v", task["source_type"])
	}
}

// TestServiceSubmitInputEmptyTextReturnsWaitingInput verifies that empty text
// submissions enter waiting_input.

// TestServiceSubmitInputEmptyTextReturnsWaitingInput verifies that empty text
// submissions enter waiting_input.
func TestServiceSubmitInputEmptyTextReturnsWaitingInput(t *testing.T) {
	service := NewService(
		contextsvc.NewService(),
		intent.NewService(),
		runengine.NewEngine(),
		delivery.NewService(),
		memory.NewService(),
		risk.NewService(),
		model.NewService(modelConfig()),
		tools.NewRegistry(),
		plugin.NewService(),
	)

	result, err := service.SubmitInput(map[string]any{
		"session_id": "sess_demo",
		"source":     "floating_ball",
		"trigger":    "hover_text_input",
		"input": map[string]any{
			"type":       "text",
			"text":       "   ",
			"input_mode": "text",
		},
		"context": map[string]any{},
	})
	if err != nil {
		t.Fatalf("submit input failed: %v", err)
	}

	task := result["task"].(map[string]any)
	if task["status"] != "waiting_input" {
		t.Fatalf("expected waiting_input status, got %v", task["status"])
	}
	if task["current_step"] != "collect_input" {
		t.Fatalf("expected collect_input current_step, got %v", task["current_step"])
	}
	if task["intent"] != nil {
		intentValue, ok := task["intent"].(map[string]any)
		if !ok || len(intentValue) != 0 {
			t.Fatalf("expected waiting_input task to keep empty intent, got %v", task["intent"])
		}
	}

	bubble := result["bubble_message"].(map[string]any)
	if bubble["type"] != "status" {
		t.Fatalf("expected waiting_input bubble type status, got %v", bubble["type"])
	}

	taskID := task["task_id"].(string)
	record, ok := service.runEngine.GetTask(taskID)
	if !ok {
		t.Fatal("expected waiting_input task to exist in runtime")
	}
	if record.FinishedAt != nil {
		t.Fatal("expected waiting_input task to keep finished_at nil")
	}
	if len(record.MemoryReadPlans) != 0 || len(record.MemoryWritePlans) != 0 {
		t.Fatal("expected waiting_input task not to attach memory handoff plans")
	}
}

// TestServiceDirectStartBuildsMemoryAndDeliveryHandoffs verifies direct starts
// attach memory and delivery handoffs.

// TestServiceDirectStartBuildsMemoryAndDeliveryHandoffs verifies direct starts
// attach memory and delivery handoffs.
func TestServiceDirectStartBuildsMemoryAndDeliveryHandoffs(t *testing.T) {
	service := NewService(
		contextsvc.NewService(),
		intent.NewService(),
		runengine.NewEngine(),
		delivery.NewService(),
		memory.NewService(),
		risk.NewService(),
		model.NewService(modelConfig()),
		tools.NewRegistry(),
		plugin.NewService(),
	)

	startResult, err := service.StartTask(map[string]any{
		"session_id": "sess_demo",
		"source":     "floating_ball",
		"trigger":    "hover_text_input",
		"input": map[string]any{
			"type": "text",
			"text": "直接总结这段文字",
		},
		"intent": map[string]any{
			"name": "summarize",
			"arguments": map[string]any{
				"style": "key_points",
			},
		},
	})
	if err != nil {
		t.Fatalf("start task failed: %v", err)
	}

	taskID := startResult["task"].(map[string]any)["task_id"].(string)
	record, ok := service.runEngine.GetTask(taskID)
	if !ok {
		t.Fatal("expected task to exist in runtime")
	}
	if len(record.MemoryReadPlans) == 0 || len(record.MemoryWritePlans) == 0 {
		t.Fatal("expected memory handoff plans to be attached")
	}
	if record.StorageWritePlan == nil || len(record.ArtifactPlans) == 0 {
		t.Fatal("expected delivery handoff plans to be attached")
	}
	if record.FinishedAt == nil {
		t.Fatal("expected direct completion flow to set finished_at only after completion")
	}

	notifications, ok := service.runEngine.PendingNotifications(taskID)
	if !ok {
		t.Fatal("expected notifications to be available")
	}
	hasDeliveryReady := false
	for _, notification := range notifications {
		if notification.Method == "delivery.ready" {
			hasDeliveryReady = true
			break
		}
	}
	if !hasDeliveryReady {
		t.Fatal("expected delivery.ready notification to be queued")
	}
}

// TestServiceStartTaskRespectsPreferredDelivery verifies direct starts preserve
// preferred and fallback delivery settings.

// TestServiceStartTaskRespectsPreferredDelivery verifies direct starts preserve
// preferred and fallback delivery settings.
func TestServiceStartTaskRespectsPreferredDelivery(t *testing.T) {
	service := newTestService()

	startResult, err := service.StartTask(map[string]any{
		"session_id": "sess_demo",
		"source":     "floating_ball",
		"trigger":    "hover_text_input",
		"input": map[string]any{
			"type": "text",
			"text": "direct summarize with bubble delivery",
		},
		"intent": map[string]any{
			"name": "summarize",
			"arguments": map[string]any{
				"style": "key_points",
			},
		},
		"delivery": map[string]any{
			"preferred": "bubble",
			"fallback":  "workspace_document",
		},
	})
	if err != nil {
		t.Fatalf("start task failed: %v", err)
	}

	deliveryResult, ok := startResult["delivery_result"].(map[string]any)
	if !ok {
		t.Fatal("expected direct start to return delivery_result")
	}
	if deliveryResult["type"] != "bubble" {
		t.Fatalf("expected preferred bubble delivery, got %v", deliveryResult["type"])
	}

	taskID := startResult["task"].(map[string]any)["task_id"].(string)
	record, ok := service.runEngine.GetTask(taskID)
	if !ok {
		t.Fatal("expected direct start task to exist in runtime")
	}
	if record.PreferredDelivery != "bubble" {
		t.Fatalf("expected runtime task to persist preferred delivery, got %q", record.PreferredDelivery)
	}
	if record.FallbackDelivery != "workspace_document" {
		t.Fatalf("expected runtime task to persist fallback delivery, got %q", record.FallbackDelivery)
	}
	if record.StorageWritePlan != nil || len(record.ArtifactPlans) != 0 {
		t.Fatal("expected bubble delivery not to create document persistence plans")
	}
}

func TestServiceStartTaskPersistsFormalReadFileSampleChain(t *testing.T) {
	service, workspaceRoot := newTestServiceWithExecution(t, "unused")
	readPath := filepath.Join(workspaceRoot, "notes", "source.txt")
	if err := os.MkdirAll(filepath.Dir(readPath), 0o755); err != nil {
		t.Fatalf("create notes dir: %v", err)
	}
	if err := os.WriteFile(readPath, []byte("hello from formal sample chain"), 0o644); err != nil {
		t.Fatalf("write source file: %v", err)
	}

	result, err := service.StartTask(map[string]any{
		"session_id": "sess_read_file_sample",
		"source":     "floating_ball",
		"trigger":    "hover_text_input",
		"input": map[string]any{
			"type": "text",
			"text": "请读取这个文件",
		},
		"intent": map[string]any{
			"name": "read_file",
			"arguments": map[string]any{
				"path": "notes/source.txt",
			},
		},
	})
	if err != nil {
		t.Fatalf("start task failed: %v", err)
	}
	taskID := result["task"].(map[string]any)["task_id"].(string)

	toolCallsResult, err := service.TaskToolCallsList(map[string]any{"task_id": taskID, "limit": 20, "offset": 0})
	if err != nil {
		t.Fatalf("task tool calls list failed: %v", err)
	}
	toolCalls := toolCallsResult["items"].([]map[string]any)
	if len(toolCalls) != 1 || toolCalls[0]["tool_name"] != "read_file" {
		t.Fatalf("expected one persisted read_file tool call, got %+v", toolCalls)
	}
	if _, ok := toolCalls[0]["created_at"].(string); !ok {
		t.Fatalf("expected persisted read_file tool call to expose created_at, got %+v", toolCalls[0])
	}
	if mapValue(toolCalls[0], "input")["path"] != "notes/source.txt" {
		t.Fatalf("expected persisted read_file path, got %+v", toolCalls[0])
	}

	eventsResult, err := service.TaskEventsList(map[string]any{"task_id": taskID, "limit": 20, "offset": 0})
	if err != nil {
		t.Fatalf("task events list failed: %v", err)
	}
	events := eventsResult["items"].([]map[string]any)
	if len(events) != 2 {
		t.Fatalf("expected tool_call.completed plus delivery.ready, got %+v", events)
	}
	foundToolCompleted := false
	foundDeliveryReady := false
	for _, event := range events {
		switch event["type"] {
		case "tool_call.completed":
			foundToolCompleted = true
		case "delivery.ready":
			foundDeliveryReady = true
		}
	}
	if !foundToolCompleted || !foundDeliveryReady {
		t.Fatalf("expected persisted read_file runtime events, got %+v", events)
	}

	deliveryRecord, ok, err := service.storage.LoopRuntimeStore().GetLatestDeliveryResult(context.Background(), taskID)
	if err != nil {
		t.Fatalf("get latest delivery result failed: %v", err)
	}
	if !ok || deliveryRecord.Type != "bubble" || !strings.Contains(deliveryRecord.PreviewText, "hello from formal sample chain") {
		t.Fatalf("expected persisted direct delivery result, ok=%v record=%+v", ok, deliveryRecord)
	}

	detailResult, err := service.TaskDetailGet(map[string]any{"task_id": taskID})
	if err != nil {
		t.Fatalf("task detail get failed: %v", err)
	}
	runtimeSummary := detailResult["runtime_summary"].(map[string]any)
	if runtimeSummary["events_count"] != 2 || runtimeSummary["latest_event_type"] != "delivery.ready" {
		t.Fatalf("expected task detail runtime summary to prefer formal event chain, got %+v", runtimeSummary)
	}
	deliveryResult := detailResult["delivery_result"].(map[string]any)
	if deliveryResult["type"] != "bubble" {
		t.Fatalf("expected task detail to expose formal delivery_result, got %+v", deliveryResult)
	}
}

func TestServiceSubmitInputRespectsPreferredDelivery(t *testing.T) {
	service := newTestService()

	result, err := service.SubmitInput(map[string]any{
		"session_id": "sess_demo",
		"source":     "floating_ball",
		"trigger":    "hover_text_input",
		"input": map[string]any{
			"type": "text",
			"text": "translate this line",
		},
		"options": map[string]any{
			"confirm_required":   false,
			"preferred_delivery": "workspace_document",
		},
	})
	if err != nil {
		t.Fatalf("submit input failed: %v", err)
	}

	deliveryResult, ok := result["delivery_result"].(map[string]any)
	if !ok {
		t.Fatal("expected submit input to return delivery_result")
	}
	if deliveryResult["type"] != "workspace_document" {
		t.Fatalf("expected preferred workspace_document delivery, got %v", deliveryResult["type"])
	}

	payload, ok := deliveryResult["payload"].(map[string]any)
	if !ok {
		t.Fatal("expected delivery_result payload")
	}
	if payload["path"] == nil {
		t.Fatal("expected workspace_document delivery to include payload path")
	}

	taskID := result["task"].(map[string]any)["task_id"].(string)
	record, ok := service.runEngine.GetTask(taskID)
	if !ok {
		t.Fatal("expected submit input task to exist in runtime")
	}
	if record.PreferredDelivery != "workspace_document" {
		t.Fatalf("expected runtime task to persist preferred delivery, got %q", record.PreferredDelivery)
	}
}

func TestServiceConfirmTaskRespectsStoredPreferredDelivery(t *testing.T) {
	service := newTestService()

	startResult, err := service.StartTask(map[string]any{
		"session_id": "sess_demo",
		"source":     "floating_ball",
		"trigger":    "text_selected_click",
		"input": map[string]any{
			"type": "text_selection",
			"text": "selected text for confirmation flow",
		},
		"delivery": map[string]any{
			"preferred": "workspace_document",
		},
	})
	if err != nil {
		t.Fatalf("start task failed: %v", err)
	}

	taskID := startResult["task"].(map[string]any)["task_id"].(string)
	confirmResult, err := service.ConfirmTask(map[string]any{
		"task_id":   taskID,
		"confirmed": true,
	})
	if err != nil {
		t.Fatalf("confirm task failed: %v", err)
	}

	deliveryResult, ok := confirmResult["delivery_result"].(map[string]any)
	if !ok {
		t.Fatal("expected confirm flow to return delivery_result")
	}
	if deliveryResult["type"] != "workspace_document" {
		t.Fatalf("expected stored preferred workspace_document delivery, got %v", deliveryResult["type"])
	}

	record, ok := service.runEngine.GetTask(taskID)
	if !ok {
		t.Fatal("expected confirmed task to exist in runtime")
	}
	if record.PreferredDelivery != "workspace_document" {
		t.Fatalf("expected runtime task to keep preferred delivery, got %q", record.PreferredDelivery)
	}
	if record.DeliveryResult["type"] != "workspace_document" {
		t.Fatalf("expected runtime delivery result to use workspace_document, got %v", record.DeliveryResult["type"])
	}
}

func TestServiceStartTaskWaitingAuthDoesNotSetFinishedAt(t *testing.T) {
	service := NewService(
		contextsvc.NewService(),
		intent.NewService(),
		runengine.NewEngine(),
		delivery.NewService(),
		memory.NewService(),
		risk.NewService(),
		model.NewService(modelConfig()),
		tools.NewRegistry(),
		plugin.NewService(),
	)

	startResult, err := service.StartTask(map[string]any{
		"session_id": "sess_demo",
		"source":     "floating_ball",
		"trigger":    "file_drop",
		"input": map[string]any{
			"type":  "file",
			"files": []any{"workspace/input.md"},
		},
		"intent": map[string]any{
			"name": "write_file",
			"arguments": map[string]any{
				"require_authorization": true,
				"target_path":           "workspace_document",
			},
		},
	})
	if err != nil {
		t.Fatalf("start task failed: %v", err)
	}

	startedTask := startResult["task"].(map[string]any)
	if startedTask["status"] != "waiting_auth" {
		t.Fatalf("expected waiting_auth status, got %v", startedTask["status"])
	}
	if startedTask["finished_at"] != nil {
		t.Fatalf("expected waiting_auth task to keep finished_at nil, got %v", startedTask["finished_at"])
	}

	taskID := startedTask["task_id"].(string)
	record, ok := service.runEngine.GetTask(taskID)
	if !ok {
		t.Fatal("expected task to remain in runtime")
	}
	if record.FinishedAt != nil {
		t.Fatal("expected runtime waiting_auth task to keep finished_at nil")
	}
}

// TestServiceConfirmCanEnterWaitingAuth verifies confirm flows can enter
// waiting_auth.

// TestServiceConfirmCanEnterWaitingAuth verifies confirm flows can enter
// waiting_auth.
func TestServiceConfirmCanEnterWaitingAuth(t *testing.T) {
	service := NewService(
		contextsvc.NewService(),
		intent.NewService(),
		runengine.NewEngine(),
		delivery.NewService(),
		memory.NewService(),
		risk.NewService(),
		model.NewService(modelConfig()),
		tools.NewRegistry(),
		plugin.NewService(),
	)

	startResult, err := service.StartTask(map[string]any{
		"session_id": "sess_demo",
		"source":     "floating_ball",
		"trigger":    "text_selected_click",
		"input": map[string]any{
			"type": "text_selection",
			"text": "这里是一段需要确认处理方式的内容",
		},
	})
	if err != nil {
		t.Fatalf("start task failed: %v", err)
	}

	taskID := startResult["task"].(map[string]any)["task_id"].(string)
	confirmResult, err := service.ConfirmTask(map[string]any{
		"task_id":   taskID,
		"confirmed": false,
		"corrected_intent": map[string]any{
			"name": "write_file",
			"arguments": map[string]any{
				"require_authorization": true,
				"target_path":           "workspace_document",
			},
		},
	})
	if err != nil {
		t.Fatalf("confirm task failed: %v", err)
	}

	confirmedTask := confirmResult["task"].(map[string]any)
	if confirmedTask["status"] != "waiting_auth" {
		t.Fatalf("expected waiting_auth status, got %v", confirmedTask["status"])
	}
	if confirmedTask["intent"].(map[string]any)["name"] != "write_file" {
		t.Fatalf("expected corrected intent to be persisted before waiting auth, got %v", confirmedTask["intent"])
	}

	notifications, ok := service.runEngine.PendingNotifications(taskID)
	if !ok {
		t.Fatal("expected notifications to exist for waiting task")
	}
	hasApprovalPending := false
	for _, notification := range notifications {
		if notification.Method == "approval.pending" {
			hasApprovalPending = true
			break
		}
	}
	if !hasApprovalPending {
		t.Fatal("expected approval.pending notification to be queued")
	}

	record, ok := service.runEngine.GetTask(taskID)
	if !ok {
		t.Fatal("expected task to remain in runtime after entering waiting_auth")
	}
	if record.Intent["name"] != "write_file" {
		t.Fatalf("expected runtime task intent to be updated before waiting auth, got %v", record.Intent)
	}
}

func TestServiceConfirmWaitingAuthPersistsApprovalRequestRecord(t *testing.T) {
	service, _ := newTestServiceWithExecution(t, "approval persistence output")
	if service.storage == nil {
		t.Fatal("expected storage service to be wired")
	}

	startResult, err := service.StartTask(map[string]any{
		"session_id": "sess_approval_store",
		"source":     "floating_ball",
		"trigger":    "text_selected_click",
		"input": map[string]any{
			"type": "text_selection",
			"text": "persist approval request before execution",
		},
	})
	if err != nil {
		t.Fatalf("start task failed: %v", err)
	}

	taskID := startResult["task"].(map[string]any)["task_id"].(string)
	_, err = service.ConfirmTask(map[string]any{
		"task_id":   taskID,
		"confirmed": false,
		"corrected_intent": map[string]any{
			"name": "write_file",
			"arguments": map[string]any{
				"require_authorization": true,
				"target_path":           "workspace_document",
			},
		},
	})
	if err != nil {
		t.Fatalf("confirm task failed: %v", err)
	}

	items, total, err := service.storage.ApprovalRequestStore().ListApprovalRequests(context.Background(), taskID, 20, 0)
	if err != nil {
		t.Fatalf("list approval requests failed: %v", err)
	}
	if total != 1 || len(items) != 1 {
		t.Fatalf("expected one persisted approval request, got total=%d items=%+v", total, items)
	}
	if items[0].TaskID != taskID || items[0].Status != "pending" {
		t.Fatalf("expected pending approval request for task %s, got %+v", taskID, items[0])
	}
	if items[0].OperationName != "write_file" {
		t.Fatalf("expected write_file approval request, got %+v", items[0])
	}
}

func TestServiceConfirmTaskReturnsStorageErrorWhenApprovalPersistenceFails(t *testing.T) {
	service, _ := newTestServiceWithExecution(t, "approval persistence failure output")
	if service.storage == nil {
		t.Fatal("expected storage service to be wired")
	}
	originalStore := service.storage.ApprovalRequestStore()
	defer replaceApprovalRequestStore(t, service.storage, originalStore)
	replaceApprovalRequestStore(t, service.storage, failingApprovalRequestStore{base: originalStore, err: errors.New("approval store unavailable")})

	startResult, err := service.StartTask(map[string]any{
		"session_id": "sess_approval_store_failure",
		"source":     "floating_ball",
		"trigger":    "text_selected_click",
		"input": map[string]any{
			"type": "text_selection",
			"text": "persist approval request before execution",
		},
	})
	if err != nil {
		t.Fatalf("start task failed: %v", err)
	}

	taskID := startResult["task"].(map[string]any)["task_id"].(string)
	_, err = service.ConfirmTask(map[string]any{
		"task_id":   taskID,
		"confirmed": false,
		"corrected_intent": map[string]any{
			"name": "write_file",
			"arguments": map[string]any{
				"require_authorization": true,
				"target_path":           "workspace_document",
			},
		},
	})
	if err == nil || !errors.Is(err, ErrStorageQueryFailed) {
		t.Fatalf("expected ErrStorageQueryFailed from approval persistence, got %v", err)
	}
}

// TestServiceSecurityRespondAllowOnceResumesAndCompletes verifies allow-once
// resumes execution and completes delivery.

func TestServiceStartTaskWritesRealMemorySummary(t *testing.T) {
	service, _ := newTestServiceWithExecution(t, "交付结果里包含 project alpha 的关键结论。")
	if service.storage == nil {
		t.Fatal("expected storage service to be wired")
	}

	result, err := service.StartTask(map[string]any{
		"session_id": "sess_memory_write",
		"source":     "floating_ball",
		"trigger":    "hover_text_input",
		"input": map[string]any{
			"type": "text",
			"text": "请总结 project alpha 的进展",
		},
		"intent": map[string]any{
			"name": "summarize",
			"arguments": map[string]any{
				"style": "key_points",
			},
		},
	})
	if err != nil {
		t.Fatalf("start task failed: %v", err)
	}

	taskID := result["task"].(map[string]any)["task_id"].(string)
	record, ok := service.runEngine.GetTask(taskID)
	if !ok {
		t.Fatal("expected completed task to remain in runtime")
	}
	if len(record.MirrorReferences) == 0 {
		t.Fatalf("expected real mirror reference after memory write, got %+v", record)
	}
	if !strings.HasPrefix(record.MirrorReferences[0]["memory_id"].(string), "memsum_") {
		t.Fatalf("expected real memory summary id, got %+v", record.MirrorReferences)
	}
	if querySQLiteCount(t, service.storage.DatabasePath(), `SELECT COUNT(1) FROM memory_summaries WHERE task_id = ?`, taskID) != 1 {
		t.Fatalf("expected one persisted memory summary for task %s", taskID)
	}
}

func TestServiceSubmitInputRoutesFollowUpIntoExistingTask(t *testing.T) {
	var activeTaskID string
	service, _ := newTestServiceWithModelClient(t, stubModelClient{
		generateText: func(request model.GenerateTextRequest) (model.GenerateTextResponse, error) {
			return model.GenerateTextResponse{
				TaskID:     request.TaskID,
				RunID:      request.RunID,
				RequestID:  "req_continue_same_task",
				Provider:   "openai_responses",
				ModelID:    "gpt-5.4",
				OutputText: fmt.Sprintf(`{"decision":"continue","task_id":"%s","reason":"follow-up text narrows the same task"}`, activeTaskID),
				Usage:      model.TokenUsage{InputTokens: 8, OutputTokens: 12, TotalTokens: 20},
				LatencyMS:  21,
			}, nil
		},
	})

	activeTask := service.runEngine.CreateTask(runengine.CreateTaskInput{
		SessionID:   "sess_follow_up_processing",
		Title:       "Analyze the current failure",
		SourceType:  "hover_input",
		Status:      "processing",
		CurrentStep: "agent_loop",
		RiskLevel:   "green",
	})
	activeTaskID = activeTask.TaskID
	activeSessionID := activeTask.SessionID

	followUpResult, err := service.SubmitInput(map[string]any{
		"source":  "floating_ball",
		"trigger": "hover_text_input",
		"input": map[string]any{
			"type":       "text",
			"text":       "重点看网络层，不要讲太泛",
			"input_mode": "text",
		},
		"context": map[string]any{},
	})
	if err != nil {
		t.Fatalf("submit follow-up failed: %v", err)
	}
	task := followUpResult["task"].(map[string]any)
	if task["task_id"] != activeTaskID {
		t.Fatalf("expected follow-up to stay on task %s, got %+v", activeTaskID, task)
	}
	if task["session_id"] != activeSessionID {
		t.Fatalf("expected follow-up to keep session %s, got %+v", activeSessionID, task)
	}
	record, ok := service.runEngine.GetTask(activeTaskID)
	if !ok {
		t.Fatal("expected continued task to remain in runtime")
	}
	if len(record.SteeringMessages) != 1 || !strings.Contains(record.SteeringMessages[0], "重点看网络层") {
		t.Fatalf("expected follow-up steering message to persist, got %+v", record.SteeringMessages)
	}
}

func TestServiceStartTaskNotificationIncludesSessionID(t *testing.T) {
	service := newTestService()

	startResult, err := service.StartTask(map[string]any{
		"session_id": "sess_notification_contract",
		"source":     "floating_ball",
		"trigger":    "hover_text_input",
		"input": map[string]any{
			"type": "text",
			"text": "Summarize this update",
		},
	})
	if err != nil {
		t.Fatalf("start task failed: %v", err)
	}

	taskID := startResult["task"].(map[string]any)["task_id"].(string)
	notifications, ok := service.runEngine.PendingNotifications(taskID)
	if !ok {
		t.Fatal("expected notifications to be available for task")
	}
	if len(notifications) == 0 {
		t.Fatal("expected at least one task.updated notification")
	}
	if notifications[0].Method != "task.updated" {
		t.Fatalf("expected first notification to be task.updated, got %+v", notifications[0])
	}
	if notifications[0].Params["session_id"] != "sess_notification_contract" {
		t.Fatalf("expected task.updated notification to carry session_id, got %+v", notifications[0].Params)
	}
}

func TestServiceStartTaskRoutesFileAttachmentIntoExistingTask(t *testing.T) {
	var activeTaskID string
	service, _ := newTestServiceWithModelClient(t, stubModelClient{
		generateText: func(request model.GenerateTextRequest) (model.GenerateTextResponse, error) {
			return model.GenerateTextResponse{
				TaskID:     request.TaskID,
				RunID:      request.RunID,
				RequestID:  "req_continue_file",
				Provider:   "openai_responses",
				ModelID:    "gpt-5.4",
				OutputText: fmt.Sprintf(`{"decision":"continue","task_id":"%s","reason":"the file is supplementary evidence for the same task"}`, activeTaskID),
				Usage:      model.TokenUsage{InputTokens: 9, OutputTokens: 13, TotalTokens: 22},
				LatencyMS:  25,
			}, nil
		},
	})

	activeTask := service.runEngine.CreateTask(runengine.CreateTaskInput{
		SessionID:   "sess_file_follow_up_processing",
		Title:       "Analyze the current service failure",
		SourceType:  "hover_input",
		Status:      "processing",
		CurrentStep: "agent_loop",
		RiskLevel:   "green",
	})
	activeTaskID = activeTask.TaskID

	followUpResult, err := service.StartTask(map[string]any{
		"source":  "floating_ball",
		"trigger": "file_drop",
		"input": map[string]any{
			"type":  "file",
			"files": []string{"logs/network.log"},
		},
	})
	if err != nil {
		t.Fatalf("start file follow-up failed: %v", err)
	}
	task := followUpResult["task"].(map[string]any)
	if task["task_id"] != activeTaskID {
		t.Fatalf("expected file follow-up to stay on task %s, got %+v", activeTaskID, task)
	}
	record, ok := service.runEngine.GetTask(activeTaskID)
	if !ok {
		t.Fatal("expected continued file task to remain in runtime")
	}
	if len(record.Snapshot.Files) != 1 || record.Snapshot.Files[0] != "logs/network.log" {
		t.Fatalf("expected file follow-up to merge snapshot files, got %+v", record.Snapshot.Files)
	}
}

func TestServiceSubmitInputDoesNotContinueWaitingAuthorizationTask(t *testing.T) {
	service := newTestService()

	startResult, err := service.StartTask(map[string]any{
		"source":  "floating_ball",
		"trigger": "hover_text_input",
		"input": map[string]any{
			"type": "text",
			"text": "把这段报错分析一下",
		},
		"intent": map[string]any{
			"name": "write_file",
			"arguments": map[string]any{
				"require_authorization": true,
			},
		},
	})
	if err != nil {
		t.Fatalf("start waiting_auth task failed: %v", err)
	}
	firstTask := startResult["task"].(map[string]any)

	followUpResult, err := service.SubmitInput(map[string]any{
		"source":  "floating_ball",
		"trigger": "hover_text_input",
		"input": map[string]any{
			"type":       "text",
			"text":       "重点看网络层，不要讲太泛",
			"input_mode": "text",
		},
		"context": map[string]any{},
	})
	if err != nil {
		t.Fatalf("submit follow-up after waiting_auth failed: %v", err)
	}
	secondTask := followUpResult["task"].(map[string]any)
	if secondTask["task_id"] == firstTask["task_id"] {
		t.Fatalf("expected waiting_auth task to reject implicit continuation, got %+v", secondTask)
	}
}

func TestServiceSubmitInputDoesNotContinuePausedTask(t *testing.T) {
	service := newTestService()

	activeTask := service.runEngine.CreateTask(runengine.CreateTaskInput{
		SessionID:   "sess_paused_follow_up",
		Title:       "Analyze the current failure",
		SourceType:  "hover_input",
		Status:      "processing",
		CurrentStep: "agent_loop",
		RiskLevel:   "green",
	})
	if _, err := service.TaskControl(map[string]any{
		"task_id": activeTask.TaskID,
		"action":  "pause",
	}); err != nil {
		t.Fatalf("pause task failed: %v", err)
	}

	followUpResult, err := service.SubmitInput(map[string]any{
		"source":  "floating_ball",
		"trigger": "hover_text_input",
		"input": map[string]any{
			"type":       "text",
			"text":       "重点看网络层，不要讲太泛",
			"input_mode": "text",
		},
		"context": map[string]any{},
	})
	if err != nil {
		t.Fatalf("submit follow-up after pause failed: %v", err)
	}
	secondTask := followUpResult["task"].(map[string]any)
	if secondTask["task_id"] == activeTask.TaskID {
		t.Fatalf("expected paused task to reject implicit continuation, got %+v", secondTask)
	}
}

func TestServiceStartTaskWithExplicitIntentDoesNotReuseWaitingTaskWithoutAnchors(t *testing.T) {
	service := newTestService()

	waitingTask := service.runEngine.CreateTask(runengine.CreateTaskInput{
		SessionID:   "sess_waiting_explicit_intent",
		Title:       "确认处理方式：当前内容",
		SourceType:  "hover_input",
		Status:      "waiting_input",
		CurrentStep: "collect_input",
		RiskLevel:   "green",
	})

	result, err := service.StartTask(map[string]any{
		"source":  "floating_ball",
		"trigger": "hover_text_input",
		"input": map[string]any{
			"type": "text",
			"text": "顺便帮我写一份周报",
		},
		"intent": map[string]any{
			"name": "write_file",
			"arguments": map[string]any{
				"target_path": "workspace/reports/weekly.md",
			},
		},
	})
	if err != nil {
		t.Fatalf("start explicit new task failed: %v", err)
	}

	task := result["task"].(map[string]any)
	if task["task_id"] == waitingTask.TaskID {
		t.Fatalf("expected explicit start intent without anchors to open a new task, got %+v", task)
	}
	if task["session_id"] == waitingTask.SessionID {
		t.Fatalf("expected explicit start intent without anchors to use a fresh hidden session, got waiting=%+v new=%+v", waitingTask, task)
	}
}

func TestServiceSubmitInputStartsNewTaskForUnrelatedRequest(t *testing.T) {
	service, _ := newTestServiceWithModelClient(t, stubModelClient{
		generateText: func(request model.GenerateTextRequest) (model.GenerateTextResponse, error) {
			return model.GenerateTextResponse{
				TaskID:     request.TaskID,
				RunID:      request.RunID,
				RequestID:  "req_new_task",
				Provider:   "openai_responses",
				ModelID:    "gpt-5.4",
				OutputText: `{"decision":"new_task","task_id":"","reason":"the new input starts a different top-level request"}`,
				Usage:      model.TokenUsage{InputTokens: 7, OutputTokens: 10, TotalTokens: 17},
				LatencyMS:  19,
			}, nil
		},
	})

	firstResult, err := service.StartTask(map[string]any{
		"source":  "floating_ball",
		"trigger": "hover_text_input",
		"input": map[string]any{
			"type": "text",
			"text": "帮我整理这份会议纪要并输出成文档",
		},
		"intent": map[string]any{
			"name": "write_file",
			"arguments": map[string]any{
				"require_authorization": true,
			},
		},
	})
	if err != nil {
		t.Fatalf("first task failed: %v", err)
	}
	firstTask := firstResult["task"].(map[string]any)

	secondResult, err := service.SubmitInput(map[string]any{
		"source":  "floating_ball",
		"trigger": "hover_text_input",
		"input": map[string]any{
			"type":       "text",
			"text":       "顺便再帮我写一份周报",
			"input_mode": "text",
		},
		"context": map[string]any{},
	})
	if err != nil {
		t.Fatalf("second task failed: %v", err)
	}
	secondTask := secondResult["task"].(map[string]any)
	if secondTask["task_id"] == firstTask["task_id"] {
		t.Fatalf("expected unrelated request to open a new task, got %+v", secondTask)
	}
	if secondTask["session_id"] == firstTask["session_id"] {
		t.Fatalf("expected unrelated request to use a new hidden session, got first=%+v second=%+v", firstTask, secondTask)
	}
}
