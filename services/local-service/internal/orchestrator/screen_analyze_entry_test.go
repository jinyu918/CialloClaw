package orchestrator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	contextsvc "github.com/cialloclaw/cialloclaw/services/local-service/internal/context"
	"github.com/cialloclaw/cialloclaw/services/local-service/internal/model"
	"github.com/cialloclaw/cialloclaw/services/local-service/internal/platform"
	"github.com/cialloclaw/cialloclaw/services/local-service/internal/runengine"
	"github.com/cialloclaw/cialloclaw/services/local-service/internal/tools"
	"github.com/cialloclaw/cialloclaw/services/local-service/internal/tools/sidecarclient"
)

func TestServiceSecurityRespondResumesQueuedScreenAnalyzeTaskThroughApproval(t *testing.T) {
	ocrStub := stubOCRWorkerClient{result: tools.OCRTextResult{Path: "temp/screen_local_0001/frame_0001.png", Text: "fatal build error", Language: "eng", Source: "ocr_worker_text"}}
	service, workspaceRoot := newTestServiceWithExecutionWorkers(t, "unused", platform.LocalExecutionBackend{}, nil, sidecarclient.NewNoopPlaywrightSidecarClient(), ocrStub, sidecarclient.NewNoopMediaWorkerClient())
	if err := os.MkdirAll(filepath.Join(workspaceRoot, "inputs"), 0o755); err != nil {
		t.Fatalf("mkdir inputs failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workspaceRoot, "inputs", "screen.png"), []byte("fake screen capture"), 0o644); err != nil {
		t.Fatalf("write screen input failed: %v", err)
	}

	firstResult, err := service.StartTask(map[string]any{
		"session_id": "sess_screen_queue",
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
	firstTaskID := firstResult["task"].(map[string]any)["task_id"].(string)

	secondResult, err := service.StartTask(map[string]any{
		"session_id": "sess_screen_queue",
		"source":     "floating_ball",
		"trigger":    "hover_text_input",
		"input": map[string]any{
			"type": "text",
			"text": "请分析屏幕中的错误",
		},
		"intent": map[string]any{
			"name": "screen_analyze",
			"arguments": map[string]any{
				"path": "inputs/screen.png",
			},
		},
	})
	if err != nil {
		t.Fatalf("second start task failed: %v", err)
	}
	secondTaskID := secondResult["task"].(map[string]any)["task_id"].(string)
	if secondResult["task"].(map[string]any)["status"] != "blocked" {
		t.Fatalf("expected queued screen task to stay blocked before approval is created, got %+v", secondResult["task"])
	}

	if _, err := service.SecurityRespond(map[string]any{
		"task_id":       firstTaskID,
		"approval_id":   "appr_screen_queue_first",
		"decision":      "allow_once",
		"remember_rule": false,
	}); err != nil {
		t.Fatalf("security respond failed for first task: %v", err)
	}

	secondTask, ok := service.runEngine.GetTask(secondTaskID)
	if !ok {
		t.Fatal("expected queued screen task to remain available in runtime")
	}
	if secondTask.Status != "waiting_auth" || secondTask.CurrentStep != "waiting_authorization" {
		t.Fatalf("expected queued screen task to re-enter waiting authorization, got %+v", secondTask)
	}
	if len(secondTask.ApprovalRequest) == 0 || stringValue(secondTask.PendingExecution, "kind", "") != "screen_analysis" {
		t.Fatalf("expected queued screen task to rebuild approval state, got %+v", secondTask)
	}

	screenResult, err := service.SecurityRespond(map[string]any{
		"task_id":       secondTaskID,
		"approval_id":   "appr_screen_queue_second",
		"decision":      "allow_once",
		"remember_rule": false,
	})
	if err != nil {
		t.Fatalf("security respond failed for queued screen task: %v", err)
	}
	if screenResult["task"].(map[string]any)["status"] != "completed" {
		t.Fatalf("expected queued screen task to complete after approval, got %+v", screenResult["task"])
	}
	detailResult, err := service.TaskDetailGet(map[string]any{"task_id": secondTaskID})
	if err != nil {
		t.Fatalf("task detail get for queued screen task failed: %v", err)
	}
	if detailResult["approval_request"] != nil {
		t.Fatalf("expected completed queued screen task to clear approval_request, got %+v", detailResult["approval_request"])
	}
	authorizationRecord, ok := detailResult["authorization_record"].(map[string]any)
	if !ok || authorizationRecord["task_id"] != secondTaskID || authorizationRecord["decision"] != "allow_once" {
		t.Fatalf("expected queued screen task detail to retain authorization record, got %+v", detailResult["authorization_record"])
	}
	auditRecord, ok := detailResult["audit_record"].(map[string]any)
	if !ok || auditRecord["task_id"] != secondTaskID {
		t.Fatalf("expected queued screen task detail to retain latest audit record, got %+v", detailResult["audit_record"])
	}
	citations := detailResult["citations"].([]map[string]any)
	if len(citations) != 1 {
		t.Fatalf("expected queued screen task detail to retain one formal citation, got %+v", citations)
	}
	if !strings.Contains(stringValue(citations[0], "label", ""), "error_evidence") {
		t.Fatalf("expected queued screen task citation to preserve evidence role, got %+v", citations[0])
	}
	securitySummary := detailResult["security_summary"].(map[string]any)
	if securitySummary["latest_restore_point"] == nil {
		t.Fatalf("expected queued screen task detail to retain recovery point summary, got %+v", securitySummary)
	}
}

func TestServiceStartTaskHandlesControlledScreenAnalyzeIntent(t *testing.T) {
	ocrStub := stubOCRWorkerClient{result: tools.OCRTextResult{Path: "temp/screen_local_0001/frame_0001.png", Text: "fatal build error", Language: "eng", Source: "ocr_worker_text"}}
	service, workspaceRoot := newTestServiceWithExecutionWorkers(t, "unused", platform.LocalExecutionBackend{}, nil, sidecarclient.NewNoopPlaywrightSidecarClient(), ocrStub, sidecarclient.NewNoopMediaWorkerClient())
	if err := os.MkdirAll(filepath.Join(workspaceRoot, "inputs"), 0o755); err != nil {
		t.Fatalf("mkdir inputs failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workspaceRoot, "inputs", "screen.png"), []byte("fake screen capture"), 0o644); err != nil {
		t.Fatalf("write screen input failed: %v", err)
	}
	result, err := service.StartTask(map[string]any{
		"session_id": "sess_screen_task",
		"source":     "floating_ball",
		"trigger":    "hover_text_input",
		"input": map[string]any{
			"type": "text",
			"text": "请分析屏幕中的错误",
		},
		"intent": map[string]any{
			"name": "screen_analyze",
			"arguments": map[string]any{
				"path": "inputs/screen.png",
			},
		},
	})
	if err != nil {
		t.Fatalf("start screen analyze task failed: %v", err)
	}
	task := result["task"].(map[string]any)
	if task["source_type"] != "screen_capture" {
		t.Fatalf("expected screen source_type, got %+v", task)
	}
	if task["status"] != "waiting_auth" {
		t.Fatalf("expected screen analyze task to require authorization first, got %+v", task)
	}
	bubble := result["bubble_message"].(map[string]any)
	if bubble["type"] != "status" {
		t.Fatalf("expected waiting authorization status bubble, got %+v", bubble)
	}
	approvalRequests, total := service.runEngine.PendingApprovalRequests(20, 0)
	if total != 1 || len(approvalRequests) != 1 {
		t.Fatalf("expected one pending approval request, got total=%d items=%+v", total, approvalRequests)
	}
	record, exists := service.runEngine.GetTask(task["task_id"].(string))
	if !exists || record.Status != "waiting_auth" {
		t.Fatalf("expected runtime screen task to wait for auth, got %+v", record)
	}
	if record.ApprovalRequest == nil || record.PendingExecution == nil {
		t.Fatalf("expected approval request and pending execution, got %+v", record)
	}
	respondResult, err := service.SecurityRespond(map[string]any{
		"task_id":  task["task_id"],
		"decision": "allow_once",
	})
	if err != nil {
		t.Fatalf("security respond allow_once failed: %v", err)
	}
	respondTask := respondResult["task"].(map[string]any)
	if respondTask["status"] != "completed" {
		t.Fatalf("expected authorized screen task to complete, got %+v", respondTask)
	}
	record, exists = service.runEngine.GetTask(task["task_id"].(string))
	if !exists || record.Status != "completed" {
		t.Fatalf("expected controlled screen task to complete, got %+v", record)
	}
	if len(record.Artifacts) != 1 || record.Artifacts[0]["artifact_type"] != "screen_capture" {
		t.Fatalf("expected one screen artifact in runtime task, got %+v", record.Artifacts)
	}
	if record.Authorization == nil || record.Authorization["decision"] != "allow_once" {
		t.Fatalf("expected authorization record to be stored, got %+v", record.Authorization)
	}
	artifacts, total, err := service.storage.ArtifactStore().ListArtifacts(context.Background(), task["task_id"].(string), 20, 0)
	if err != nil {
		t.Fatalf("list persisted artifacts failed: %v", err)
	}
	if total != 1 || len(artifacts) != 1 {
		t.Fatalf("expected one persisted screen artifact, total=%d len=%d", total, len(artifacts))
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(artifacts[0].DeliveryPayloadJSON), &payload); err != nil {
		t.Fatalf("decode persisted screen payload failed: %v", err)
	}
	if payload["screen_session_id"] == "" || payload["capture_mode"] != "screenshot" || payload["retention_policy"] == "" || payload["evidence_role"] != "error_evidence" {
		t.Fatalf("expected persisted artifact payload to retain screen metadata, got %+v", payload)
	}
	detailResult, err := service.TaskDetailGet(map[string]any{"task_id": task["task_id"]})
	if err != nil {
		t.Fatalf("task detail get for screen task failed: %v", err)
	}
	authorizationRecord, ok := detailResult["authorization_record"].(map[string]any)
	if !ok || authorizationRecord["decision"] != "allow_once" {
		t.Fatalf("expected task detail to expose latest authorization_record, got %+v", detailResult["authorization_record"])
	}
	auditRecord, ok := detailResult["audit_record"].(map[string]any)
	if !ok || auditRecord["task_id"] != task["task_id"] {
		t.Fatalf("expected task detail to expose latest audit_record, got %+v", detailResult["audit_record"])
	}
	citations := detailResult["citations"].([]map[string]any)
	if len(citations) != 1 {
		t.Fatalf("expected one formal citation for screen task, got %+v", citations)
	}
	if citations[0]["source_type"] != "file" || !strings.Contains(stringValue(citations[0], "label", ""), "error_evidence") {
		t.Fatalf("expected citation to preserve artifact-backed screen evidence metadata, got %+v", citations[0])
	}
	if citations[0]["artifact_type"] != "screen_capture" || citations[0]["evidence_role"] != "error_evidence" {
		t.Fatalf("expected citation to expose structured screen evidence role and artifact type, got %+v", citations[0])
	}
	if citations[0]["excerpt_text"] == nil || citations[0]["screen_session_id"] == nil {
		t.Fatalf("expected citation to expose OCR excerpt and screen session metadata, got %+v", citations[0])
	}
	deliveryResult, ok := detailResult["delivery_result"].(map[string]any)
	if !ok || stringValue(deliveryResult, "preview_text", "") == "" {
		t.Fatalf("expected task detail to expose formal delivery_result, got %+v", detailResult["delivery_result"])
	}
	record, exists = service.runEngine.GetTask(task["task_id"].(string))
	if !exists || len(record.Citations) != 1 {
		t.Fatalf("expected runtime task to retain one formal citation, got %+v", record)
	}
}

func TestServiceStartTaskPreservesClipCaptureModeThroughScreenApproval(t *testing.T) {
	ocrStub := stubOCRWorkerClient{result: tools.OCRTextResult{Path: "temp/screen_local_0001/frame_0001_clip_frames/frame-001.jpg", Text: "fatal clip error", Language: "eng", Source: "ocr_worker_text"}}
	mediaStub := stubMediaWorkerClient{framesResult: tools.MediaFrameExtractResult{InputPath: "temp/screen_local_0001/frame_0001.webm", OutputDir: "temp/screen_local_0001/frame_0001_clip_frames", FramePaths: []string{"temp/screen_local_0001/frame_0001_clip_frames/frame-001.jpg"}, FrameCount: 1, Source: "media_worker_frames"}}
	service, workspaceRoot := newTestServiceWithExecutionWorkers(t, "unused", platform.LocalExecutionBackend{}, nil, sidecarclient.NewNoopPlaywrightSidecarClient(), ocrStub, mediaStub)
	if err := os.MkdirAll(filepath.Join(workspaceRoot, "inputs"), 0o755); err != nil {
		t.Fatalf("mkdir inputs failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workspaceRoot, "inputs", "screen.webm"), []byte("fake screen clip"), 0o644); err != nil {
		t.Fatalf("write clip input failed: %v", err)
	}

	result, err := service.StartTask(map[string]any{
		"session_id": "sess_screen_clip_task",
		"source":     "floating_ball",
		"trigger":    "hover_text_input",
		"input": map[string]any{
			"type": "text",
			"text": "请分析录屏里的错误",
		},
		"intent": map[string]any{
			"name": "screen_analyze",
			"arguments": map[string]any{
				"path":         "inputs/screen.webm",
				"capture_mode": string(tools.ScreenCaptureModeClip),
			},
		},
	})
	if err != nil {
		t.Fatalf("start clip screen analyze task failed: %v", err)
	}
	task := result["task"].(map[string]any)
	record, exists := service.runEngine.GetTask(task["task_id"].(string))
	if !exists || stringValue(record.PendingExecution, "capture_mode", "") != string(tools.ScreenCaptureModeClip) {
		t.Fatalf("expected pending execution to preserve clip capture mode, got %+v", record.PendingExecution)
	}
	respondResult, err := service.SecurityRespond(map[string]any{
		"task_id":  task["task_id"],
		"decision": "allow_once",
	})
	if err != nil {
		t.Fatalf("security respond allow_once failed: %v", err)
	}
	respondTask := respondResult["task"].(map[string]any)
	if respondTask["status"] != "completed" {
		t.Fatalf("expected authorized clip screen task to complete, got %+v", respondTask)
	}
	artifacts, total, err := service.storage.ArtifactStore().ListArtifacts(context.Background(), task["task_id"].(string), 20, 0)
	if err != nil || total != 1 || len(artifacts) != 1 {
		t.Fatalf("expected one persisted clip screen artifact, total=%d len=%d err=%v", total, len(artifacts), err)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(artifacts[0].DeliveryPayloadJSON), &payload); err != nil {
		t.Fatalf("decode persisted clip payload failed: %v", err)
	}
	if payload["capture_mode"] != string(tools.ScreenCaptureModeClip) {
		t.Fatalf("expected persisted clip payload to keep clip capture_mode, got %+v", payload)
	}
	if !strings.HasSuffix(artifacts[0].Path, ".webm") {
		t.Fatalf("expected clip artifact path to keep webm extension, got %+v", artifacts[0])
	}
}

func TestServiceStartTaskInfersScreenAnalyzeFromVisualErrorRequest(t *testing.T) {
	ocrStub := stubOCRWorkerClient{result: tools.OCRTextResult{Path: "temp/screen_local_0001/frame_0001.png", Text: "fatal build error", Language: "eng", Source: "ocr_worker_text"}}
	service, _ := newTestServiceWithExecutionWorkers(t, "unused", platform.LocalExecutionBackend{}, nil, sidecarclient.NewNoopPlaywrightSidecarClient(), ocrStub, sidecarclient.NewNoopMediaWorkerClient())

	result, err := service.StartTask(map[string]any{
		"session_id": "sess_screen_infer_start",
		"source":     "floating_ball",
		"trigger":    "hover_text_input",
		"input": map[string]any{
			"type": "text",
			"text": "帮我看看这个页面的报错",
			"page_context": map[string]any{
				"title":        "Build Dashboard",
				"url":          "https://example.com/build",
				"app_name":     "Chrome",
				"window_title": "Browser - Build Dashboard",
				"visible_text": "Fatal build error: missing release asset",
			},
		},
		"context": map[string]any{
			"screen_summary": "release validation failed on current screen",
		},
	})
	if err != nil {
		t.Fatalf("start inferred screen analyze task failed: %v", err)
	}
	task := result["task"].(map[string]any)
	if task["source_type"] != "screen_capture" {
		t.Fatalf("expected screen_capture source type, got %+v", task)
	}
	if task["status"] != "waiting_auth" {
		t.Fatalf("expected inferred screen analyze task to wait for auth, got %+v", task)
	}
	intentValue := task["intent"].(map[string]any)
	if intentValue["name"] != "screen_analyze" {
		t.Fatalf("expected screen_analyze intent, got %+v", intentValue)
	}
	arguments := intentValue["arguments"].(map[string]any)
	if arguments["evidence_role"] != "error_evidence" || arguments["page_title"] != "Build Dashboard" {
		t.Fatalf("expected inferred visual arguments to be preserved, got %+v", arguments)
	}
	record, exists := service.runEngine.GetTask(task["task_id"].(string))
	if !exists || record.PendingExecution == nil {
		t.Fatalf("expected runtime task to keep pending execution for inferred screen task, got %+v", record)
	}
	if stringValue(record.PendingExecution, "source_path", "") != "" {
		t.Fatalf("expected inferred screen task to authorize current screen instead of an existing file, got %+v", record.PendingExecution)
	}
	if stringValue(record.PendingExecution, "target_object", "") != "Build Dashboard" {
		t.Fatalf("expected inferred screen target to use page context, got %+v", record.PendingExecution)
	}
}

func TestServiceStartTaskExplicitScreenAnalyzeKeepsFreshAuthorizationBoundary(t *testing.T) {
	ocrStub := stubOCRWorkerClient{result: tools.OCRTextResult{Path: "temp/screen_local_0001/frame_0001.png", Text: "fatal build error", Language: "eng", Source: "ocr_worker_text"}}
	service, workspaceRoot := newTestServiceWithExecutionWorkers(t, "unused", platform.LocalExecutionBackend{}, nil, sidecarclient.NewNoopPlaywrightSidecarClient(), ocrStub, sidecarclient.NewNoopMediaWorkerClient())
	if err := os.MkdirAll(filepath.Join(workspaceRoot, "inputs"), 0o755); err != nil {
		t.Fatalf("mkdir inputs failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workspaceRoot, "inputs", "screen.png"), []byte("fake screen capture"), 0o644); err != nil {
		t.Fatalf("write screen input failed: %v", err)
	}

	activeTask := service.runEngine.CreateTask(runengine.CreateTaskInput{
		SessionID:   "sess_screen_follow_up",
		Title:       "Analyze the current failure",
		SourceType:  "hover_input",
		Status:      "waiting_input",
		CurrentStep: "collect_input",
		RiskLevel:   "green",
		Snapshot: contextsvc.TaskContextSnapshot{
			PageURL:     "https://example.com/build/1",
			AppName:     "Chrome",
			WindowTitle: "Build 1",
		},
	})

	modelCalled := false
	service.model = model.NewService(modelConfig(), stubModelClient{
		generateText: func(request model.GenerateTextRequest) (model.GenerateTextResponse, error) {
			modelCalled = true
			return model.GenerateTextResponse{
				TaskID:     request.TaskID,
				RunID:      request.RunID,
				RequestID:  "req_continue_screen",
				Provider:   "openai_responses",
				ModelID:    "gpt-5.4",
				OutputText: fmt.Sprintf(`{"decision":"continue","task_id":"%s","reason":"same session and anchors"}`, activeTask.TaskID),
			}, nil
		},
	})

	result, err := service.StartTask(map[string]any{
		"session_id": activeTask.SessionID,
		"source":     "floating_ball",
		"trigger":    "hover_text_input",
		"input": map[string]any{
			"type": "text",
			"text": "请分析当前屏幕里的错误",
		},
		"context": map[string]any{
			"page": map[string]any{
				"url":          "https://example.com/build/1",
				"app_name":     "Chrome",
				"window_title": "Build 1",
			},
		},
		"intent": map[string]any{
			"name": "screen_analyze",
			"arguments": map[string]any{
				"path": "inputs/screen.png",
			},
		},
	})
	if err != nil {
		t.Fatalf("start explicit screen analyze task failed: %v", err)
	}
	if modelCalled {
		t.Fatal("expected explicit screen_analyze to bypass continuation classification")
	}

	task := result["task"].(map[string]any)
	if task["task_id"] == activeTask.TaskID {
		t.Fatalf("expected explicit screen_analyze to open a fresh task, got %+v", task)
	}
	if task["status"] != "waiting_auth" {
		t.Fatalf("expected explicit screen_analyze to establish waiting_auth, got %+v", task)
	}

	approvalRequests, total := service.runEngine.PendingApprovalRequests(20, 0)
	if total != 1 || len(approvalRequests) != 1 {
		t.Fatalf("expected one pending approval request, got total=%d items=%+v", total, approvalRequests)
	}
	if approvalRequests[0]["task_id"] != task["task_id"] {
		t.Fatalf("expected approval to target new screen task, got %+v", approvalRequests[0])
	}
}

func TestServiceConfirmTaskCorrectedScreenAnalyzeEstablishesWaitingAuth(t *testing.T) {
	service, _ := newTestServiceWithExecutionWorkers(t, "unused", platform.LocalExecutionBackend{}, nil, sidecarclient.NewNoopPlaywrightSidecarClient(), sidecarclient.NewNoopOCRWorkerClient(), sidecarclient.NewNoopMediaWorkerClient())
	task := service.runEngine.CreateTask(runengine.CreateTaskInput{
		SessionID:   "sess_confirm_screen",
		Title:       "确认处理方式：当前屏幕问题",
		SourceType:  "hover_input",
		Status:      "confirming_intent",
		CurrentStep: "intent_confirmation",
		RiskLevel:   "green",
		Intent: map[string]any{
			"name":      "agent_loop",
			"arguments": map[string]any{},
		},
		Snapshot: contextsvc.TaskContextSnapshot{
			InputType:     "text",
			Text:          "请分析当前界面里的报错",
			PageTitle:     "Build Dashboard",
			WindowTitle:   "Build Dashboard",
			VisibleText:   "fatal build error",
			ScreenSummary: "build failed on current page",
		},
	})

	result, err := service.ConfirmTask(map[string]any{
		"task_id":   task.TaskID,
		"confirmed": false,
		"corrected_intent": map[string]any{
			"name":      "screen_analyze",
			"arguments": map[string]any{},
		},
	})
	if err != nil {
		t.Fatalf("confirm task with corrected screen intent failed: %v", err)
	}

	updatedTask := result["task"].(map[string]any)
	if updatedTask["task_id"] != task.TaskID {
		t.Fatalf("expected corrected screen confirm to reuse task identity, got %+v", updatedTask)
	}
	if updatedTask["status"] != "waiting_auth" {
		t.Fatalf("expected corrected screen confirm to enter waiting_auth, got %+v", updatedTask)
	}
	if result["delivery_result"] != nil {
		t.Fatalf("expected corrected screen confirm to wait for authorization before delivery, got %+v", result)
	}
	approvalRequests, total := service.runEngine.PendingApprovalRequests(20, 0)
	if total != 1 || len(approvalRequests) != 1 {
		t.Fatalf("expected one pending approval request after corrected screen confirm, got total=%d items=%+v", total, approvalRequests)
	}
	if approvalRequests[0]["task_id"] != task.TaskID {
		t.Fatalf("expected approval request to target confirmed task, got %+v", approvalRequests[0])
	}
	storedTask, ok := service.runEngine.GetTask(task.TaskID)
	if !ok {
		t.Fatalf("expected task %s to remain in runtime", task.TaskID)
	}
	if stringValue(storedTask.Intent, "name", "") != "screen_analyze" {
		t.Fatalf("expected corrected screen intent to persist, got %+v", storedTask.Intent)
	}
}

func TestResolveScreenAnalyzeIntentInfersClipModeFromVideoPath(t *testing.T) {
	service := newTestService()
	resolvedIntent := service.resolveScreenAnalyzeIntent(contextsvc.TaskContextSnapshot{}, map[string]any{
		"name": "screen_analyze",
		"arguments": map[string]any{
			"path": "clips/demo.webm",
		},
	})
	arguments := mapValue(resolvedIntent, "arguments")
	if arguments["capture_mode"] != "clip" {
		t.Fatalf("expected video-backed screen analyze intent to infer clip capture mode, got %+v", arguments)
	}
}

func TestServiceStartTaskHandlesClipScreenAnalyzePath(t *testing.T) {
	ocrStub := stubOCRWorkerClient{result: tools.OCRTextResult{Path: "temp/screen_sess_0001/frame_0001_frames/frame-001.jpg", Text: "release validation failed in recording", Language: "eng", Source: "ocr_worker_text"}}
	mediaStub := stubMediaWorkerClient{
		transcodeResult: tools.MediaTranscodeResult{InputPath: "temp/screen_sess_0001/frame_0001.webm", OutputPath: "temp/screen_sess_0001/frame_0001_normalized.mp4", Format: "mp4", Source: "media_worker_ffmpeg"},
		framesResult:    tools.MediaFrameExtractResult{InputPath: "temp/screen_sess_0001/frame_0001_normalized.mp4", OutputDir: "temp/screen_sess_0001/frame_0001_frames", FramePaths: []string{"temp/screen_sess_0001/frame_0001_frames/frame-001.jpg"}, FrameCount: 1, Source: "media_worker_frames"},
	}
	service, workspaceRoot := newTestServiceWithExecutionWorkers(t, "unused", platform.LocalExecutionBackend{}, nil, sidecarclient.NewNoopPlaywrightSidecarClient(), ocrStub, mediaStub)
	if err := os.MkdirAll(filepath.Join(workspaceRoot, "clips"), 0o755); err != nil {
		t.Fatalf("mkdir clip source dir failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workspaceRoot, "clips", "demo.webm"), []byte("fake clip"), 0o644); err != nil {
		t.Fatalf("write clip source failed: %v", err)
	}

	result, err := service.StartTask(map[string]any{
		"session_id": "sess_screen_clip_start",
		"source":     "floating_ball",
		"trigger":    "hover_text_input",
		"input": map[string]any{
			"type": "text",
			"text": "请分析这段录屏",
		},
		"intent": map[string]any{
			"name": "screen_analyze",
			"arguments": map[string]any{
				"path": "clips/demo.webm",
			},
		},
	})
	if err != nil {
		t.Fatalf("start clip screen analyze task failed: %v", err)
	}
	result, err = service.SecurityRespond(map[string]any{
		"task_id":  result["task"].(map[string]any)["task_id"],
		"decision": "allow_once",
	})
	if err != nil {
		t.Fatalf("security respond for clip screen analyze failed: %v", err)
	}
	task := result["task"].(map[string]any)
	if task["status"] != "completed" || task["source_type"] != "screen_capture" {
		t.Fatalf("expected clip screen analyze task to complete on screen_capture path, got %+v", task)
	}
	taskID := task["task_id"].(string)
	record, exists := service.runEngine.GetTask(taskID)
	if !exists || len(record.Artifacts) != 1 {
		t.Fatalf("expected clip screen analyze to persist one runtime artifact, got %+v", record)
	}
	if record.Artifacts[0]["mime_type"] != "video/webm" {
		t.Fatalf("expected clip screen analyze to keep video artifact mime type, got %+v", record.Artifacts)
	}
	artifacts, total, err := service.storage.ArtifactStore().ListArtifacts(context.Background(), taskID, 20, 0)
	if err != nil || total != 1 || len(artifacts) != 1 {
		t.Fatalf("expected one persisted clip artifact, total=%d len=%d err=%v", total, len(artifacts), err)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(artifacts[0].DeliveryPayloadJSON), &payload); err != nil {
		t.Fatalf("decode persisted clip artifact payload failed: %v", err)
	}
	if payload["capture_mode"] != "clip" || payload["screen_session_id"] == "" {
		t.Fatalf("expected clip artifact payload to preserve clip capture metadata, got %+v", payload)
	}
	detailResult, err := service.TaskDetailGet(map[string]any{"task_id": taskID})
	if err != nil {
		t.Fatalf("task detail get for clip screen task failed: %v", err)
	}
	citations := detailResult["citations"].([]map[string]any)
	if len(citations) != 1 || citations[0]["artifact_type"] != "screen_capture" || citations[0]["excerpt_text"] == nil {
		t.Fatalf("expected clip screen task detail to expose one formal citation, got %+v", citations)
	}
}

func TestServiceScreenAnalyzeStopsSessionAfterSuccessfulApproval(t *testing.T) {
	baseScreenClient := sidecarclient.NewInMemoryScreenCaptureClient()
	expiredSession, err := baseScreenClient.StartSession(context.Background(), tools.ScreenSessionStartInput{SessionID: "sess_expired_cleanup", TaskID: "task_expired_cleanup", RunID: "run_expired_cleanup", CaptureMode: tools.ScreenCaptureModeScreenshot, TTL: time.Millisecond})
	if err != nil {
		t.Fatalf("start expired cleanup seed session failed: %v", err)
	}
	if _, err := baseScreenClient.CaptureScreenshot(context.Background(), tools.ScreenCaptureInput{ScreenSessionID: expiredSession.ScreenSessionID, CaptureMode: tools.ScreenCaptureModeScreenshot, Source: "screen_capture"}); err != nil {
		t.Fatalf("capture expired cleanup seed frame failed: %v", err)
	}
	time.Sleep(5 * time.Millisecond)
	screenClient := &recordingScreenCaptureClient{base: baseScreenClient}
	ocrStub := stubOCRWorkerClient{result: tools.OCRTextResult{Path: "temp/screen_sess_0001/frame_0001.png", Text: "fatal build error", Language: "eng", Source: "ocr_worker_text"}}
	service, _ := newTestServiceWithExecutionWorkersAndScreen(t, "unused", platform.LocalExecutionBackend{}, nil, sidecarclient.NewNoopPlaywrightSidecarClient(), ocrStub, sidecarclient.NewNoopMediaWorkerClient(), screenClient)

	result, err := service.StartTask(map[string]any{
		"session_id": "sess_screen_stop",
		"source":     "floating_ball",
		"trigger":    "hover_text_input",
		"input": map[string]any{
			"type": "text",
			"text": "请分析屏幕中的错误",
		},
		"intent": map[string]any{
			"name": "screen_analyze",
			"arguments": map[string]any{
				"path": "inputs/screen.png",
			},
		},
	})
	if err != nil {
		t.Fatalf("start screen analyze task failed: %v", err)
	}
	_, err = service.SecurityRespond(map[string]any{
		"task_id":  result["task"].(map[string]any)["task_id"],
		"decision": "allow_once",
	})
	if err != nil {
		t.Fatalf("security respond allow_once failed: %v", err)
	}
	if len(screenClient.stopCalls) != 1 || screenClient.stopCalls[0].reason != "analysis_completed" {
		t.Fatalf("expected one successful screen session stop, got %+v", screenClient.stopCalls)
	}
	if len(screenClient.expiredCleanupScanCalls) != 1 || screenClient.expiredCleanupScanCalls[0].Reason != "expired_session_scan" {
		t.Fatalf("expected successful screen analysis to scan expired sessions once, got %+v", screenClient.expiredCleanupScanCalls)
	}
	cleanupResult, err := baseScreenClient.CleanupSessionArtifacts(context.Background(), tools.ScreenCleanupInput{ScreenSessionID: expiredSession.ScreenSessionID, Reason: "assert_cleanup_scan"})
	if err != nil || cleanupResult.DeletedCount != 0 {
		t.Fatalf("expected expired cleanup scan to reclaim old temp artifacts before new execution, result=%+v err=%v", cleanupResult, err)
	}
	if len(screenClient.expireCalls) != 0 {
		t.Fatalf("expected successful screen analysis to avoid expire semantics, got %+v", screenClient.expireCalls)
	}
	if len(screenClient.cleanupCalls) != 1 || screenClient.cleanupCalls[0].Reason != "analysis_completed" || len(screenClient.cleanupCalls[0].Paths) != 1 {
		t.Fatalf("expected successful screen analysis to cleanup only the tracked capture residue, got %+v", screenClient.cleanupCalls)
	}
	if _, err := screenClient.GetSession(context.Background(), screenClient.stopCalls[0].sessionID); !errors.Is(err, tools.ErrScreenCaptureSessionExpired) {
		t.Fatalf("expected stopped session to become terminal, got err=%v", err)
	}
}

func TestServiceScreenAnalyzeFailureExpiresAndCleansSession(t *testing.T) {
	screenClient := &recordingScreenCaptureClient{base: sidecarclient.NewInMemoryScreenCaptureClient()}
	ocrStub := stubOCRWorkerClient{err: tools.ErrOCRWorkerFailed}
	service, _ := newTestServiceWithExecutionWorkersAndScreen(t, "unused", platform.LocalExecutionBackend{}, nil, sidecarclient.NewNoopPlaywrightSidecarClient(), ocrStub, sidecarclient.NewNoopMediaWorkerClient(), screenClient)

	result, err := service.StartTask(map[string]any{
		"session_id": "sess_screen_cleanup",
		"source":     "floating_ball",
		"trigger":    "hover_text_input",
		"input": map[string]any{
			"type": "text",
			"text": "请分析屏幕中的错误",
		},
		"intent": map[string]any{
			"name": "screen_analyze",
			"arguments": map[string]any{
				"path": "inputs/screen.png",
			},
		},
	})
	if err != nil {
		t.Fatalf("start screen analyze task failed: %v", err)
	}
	respondResult, err := service.SecurityRespond(map[string]any{
		"task_id":  result["task"].(map[string]any)["task_id"],
		"decision": "allow_once",
	})
	if err != nil {
		t.Fatalf("security respond should surface task-centric failure result, got %v", err)
	}
	if respondResult["task"].(map[string]any)["status"] != "failed" {
		t.Fatalf("expected screen analysis failure to end in failed status, got %+v", respondResult)
	}
	if len(screenClient.stopCalls) != 0 {
		t.Fatalf("expected failed screen analysis to avoid stop semantics, got %+v", screenClient.stopCalls)
	}
	if len(screenClient.expiredCleanupScanCalls) != 1 || screenClient.expiredCleanupScanCalls[0].Reason != "expired_session_scan" {
		t.Fatalf("expected failed screen analysis to scan expired sessions once, got %+v", screenClient.expiredCleanupScanCalls)
	}
	if len(screenClient.expireCalls) != 1 || screenClient.expireCalls[0].reason != "analysis_failed" {
		t.Fatalf("expected failed screen analysis to expire session, got %+v", screenClient.expireCalls)
	}
	if len(screenClient.cleanupCalls) != 1 || screenClient.cleanupCalls[0].Reason != "analysis_failed" || screenClient.cleanupCalls[0].ScreenSessionID != screenClient.expireCalls[0].sessionID {
		t.Fatalf("expected failed screen analysis to cleanup the expired session, got %+v", screenClient.cleanupCalls)
	}
	if _, err := screenClient.GetSession(context.Background(), screenClient.expireCalls[0].sessionID); !errors.Is(err, tools.ErrScreenCaptureSessionExpired) {
		t.Fatalf("expected expired session to become terminal, got err=%v", err)
	}
}

func TestServiceScreenAnalyzeCaptureFailureExpiresAndCleansSession(t *testing.T) {
	screenClient := &recordingScreenCaptureClient{base: sidecarclient.NewInMemoryScreenCaptureClient(), captureErr: tools.ErrScreenCaptureFailed}
	service, _ := newTestServiceWithExecutionWorkersAndScreen(t, "unused", platform.LocalExecutionBackend{}, nil, sidecarclient.NewNoopPlaywrightSidecarClient(), sidecarclient.NewNoopOCRWorkerClient(), sidecarclient.NewNoopMediaWorkerClient(), screenClient)

	result, err := service.StartTask(map[string]any{
		"session_id": "sess_screen_capture_failure",
		"source":     "floating_ball",
		"trigger":    "hover_text_input",
		"input": map[string]any{
			"type": "text",
			"text": "请分析屏幕中的错误",
		},
		"intent": map[string]any{
			"name": "screen_analyze",
			"arguments": map[string]any{
				"path": "inputs/screen.png",
			},
		},
	})
	if err != nil {
		t.Fatalf("start screen analyze task failed: %v", err)
	}
	respondResult, err := service.SecurityRespond(map[string]any{
		"task_id":  result["task"].(map[string]any)["task_id"],
		"decision": "allow_once",
	})
	if err != nil {
		t.Fatalf("security respond should surface task-centric capture failure result, got %v", err)
	}
	if respondResult["task"].(map[string]any)["status"] != "failed" {
		t.Fatalf("expected screen capture failure to end in failed status, got %+v", respondResult)
	}
	if len(screenClient.stopCalls) != 0 {
		t.Fatalf("expected capture failure to avoid stop semantics, got %+v", screenClient.stopCalls)
	}
	if len(screenClient.expiredCleanupScanCalls) != 1 || screenClient.expiredCleanupScanCalls[0].Reason != "expired_session_scan" {
		t.Fatalf("expected capture failure to scan expired sessions once, got %+v", screenClient.expiredCleanupScanCalls)
	}
	if len(screenClient.expireCalls) != 1 || screenClient.expireCalls[0].reason != "capture_failed" {
		t.Fatalf("expected capture failure to expire session, got %+v", screenClient.expireCalls)
	}
	if len(screenClient.cleanupCalls) != 1 || screenClient.cleanupCalls[0].Reason != "capture_failed" || screenClient.cleanupCalls[0].ScreenSessionID != screenClient.expireCalls[0].sessionID {
		t.Fatalf("expected capture failure to cleanup the expired session, got %+v", screenClient.cleanupCalls)
	}
}

func TestServiceStartTaskInfersScreenAnalyzeTitleFromScreenSummaryWhenTitlesAreMissing(t *testing.T) {
	ocrStub := stubOCRWorkerClient{result: tools.OCRTextResult{Path: "temp/screen_local_0001/frame_0001.png", Text: "fatal build error", Language: "eng", Source: "ocr_worker_text"}}
	service, _ := newTestServiceWithExecutionWorkers(t, "unused", platform.LocalExecutionBackend{}, nil, sidecarclient.NewNoopPlaywrightSidecarClient(), ocrStub, sidecarclient.NewNoopMediaWorkerClient())

	result, err := service.StartTask(map[string]any{
		"session_id": "sess_screen_summary_title",
		"source":     "floating_ball",
		"trigger":    "hover_text_input",
		"input": map[string]any{
			"type": "text",
			"text": "看看当前屏幕上哪里出错了",
		},
		"context": map[string]any{
			"screen": map[string]any{
				"summary":      "release validation failed before publish",
				"visible_text": "fatal build error",
			},
		},
	})
	if err != nil {
		t.Fatalf("start inferred screen task failed: %v", err)
	}

	task := result["task"].(map[string]any)
	if !strings.Contains(stringValue(task, "title", ""), "release validation") {
		t.Fatalf("expected screen summary to drive inferred task title, got %+v", task)
	}
	intentValue := task["intent"].(map[string]any)
	arguments := intentValue["arguments"].(map[string]any)
	if arguments["screen_summary"] != "release validation failed before publish" || arguments["visible_text"] != "fatal build error" {
		t.Fatalf("expected inferred intent to preserve screen summary and visible text, got %+v", arguments)
	}
}

func TestServiceSubmitInputInfersScreenAnalyzeFromVisualErrorRequest(t *testing.T) {
	ocrStub := stubOCRWorkerClient{result: tools.OCRTextResult{Path: "temp/screen_local_0001/frame_0001.png", Text: "fatal build error", Language: "eng", Source: "ocr_worker_text"}}
	service, _ := newTestServiceWithExecutionWorkers(t, "unused", platform.LocalExecutionBackend{}, nil, sidecarclient.NewNoopPlaywrightSidecarClient(), ocrStub, sidecarclient.NewNoopMediaWorkerClient())

	result, err := service.SubmitInput(map[string]any{
		"session_id": "sess_screen_infer_submit",
		"source":     "floating_ball",
		"trigger":    "hover_text_input",
		"input": map[string]any{
			"type":       "text",
			"text":       "看看当前屏幕上的报错",
			"input_mode": "text",
		},
		"context": map[string]any{
			"page": map[string]any{
				"title":        "Release Checklist",
				"url":          "https://example.com/release",
				"app_name":     "Chrome",
				"window_title": "Browser - Release Checklist",
				"visible_text": "Warning: release notes are incomplete.",
			},
			"screen_summary": "release checklist shows blocking warning",
		},
	})
	if err != nil {
		t.Fatalf("submit inferred screen analyze task failed: %v", err)
	}
	task := result["task"].(map[string]any)
	if task["status"] != "waiting_auth" || task["source_type"] != "screen_capture" {
		t.Fatalf("expected submit input to route into waiting screen authorization, got %+v", task)
	}
	bubble := result["bubble_message"].(map[string]any)
	if bubble["type"] != "status" {
		t.Fatalf("expected waiting authorization status bubble, got %+v", bubble)
	}
}

func TestServiceStartTaskFallsBackWhenScreenCapabilityUnavailable(t *testing.T) {
	service := newTestService()

	result, err := service.StartTask(map[string]any{
		"session_id": "sess_screen_capability_fallback",
		"source":     "floating_ball",
		"trigger":    "hover_text_input",
		"input": map[string]any{
			"type": "text",
			"text": "帮我看看这个页面的报错",
			"page_context": map[string]any{
				"title":        "Build Dashboard",
				"window_title": "Browser - Build Dashboard",
				"visible_text": "Fatal build error: missing release asset",
			},
		},
		"context": map[string]any{
			"screen_summary": "release validation failed on current screen",
		},
	})
	if err != nil {
		t.Fatalf("start task with unavailable screen capability failed: %v", err)
	}
	task := result["task"].(map[string]any)
	if task["source_type"] == "screen_capture" {
		t.Fatalf("expected unavailable screen capability to avoid screen_capture task, got %+v", task)
	}
	intentValue := task["intent"].(map[string]any)
	if intentValue["name"] != "agent_loop" {
		t.Fatalf("expected fallback to agent_loop, got %+v", intentValue)
	}
	if task["status"] != "completed" {
		t.Fatalf("expected fallback task to continue through normal flow, got %+v", task)
	}
	if task["current_step"] == "waiting_authorization" {
		t.Fatalf("expected fallback task to avoid visual waiting_auth flow, got %+v", task)
	}
}

func TestServiceSubmitInputFallbackKeepsExplicitConfirmationWhenScreenCapabilityUnavailable(t *testing.T) {
	service := newTestService()

	result, err := service.SubmitInput(map[string]any{
		"session_id": "sess_screen_capability_confirm_fallback",
		"source":     "floating_ball",
		"trigger":    "hover_text_input",
		"input": map[string]any{
			"type":       "text",
			"text":       "帮我看看这个页面的报错",
			"input_mode": "text",
		},
		"context": map[string]any{
			"page": map[string]any{
				"title":        "Build Dashboard",
				"window_title": "Browser - Build Dashboard",
				"visible_text": "Fatal build error: missing release asset",
			},
			"screen_summary": "release validation failed on current screen",
		},
		"options": map[string]any{
			"confirm_required": true,
		},
	})
	if err != nil {
		t.Fatalf("submit input with unavailable screen capability failed: %v", err)
	}
	task := result["task"].(map[string]any)
	if task["status"] != "confirming_intent" {
		t.Fatalf("expected fallback task to preserve confirming_intent, got %+v", task)
	}
	if task["current_step"] != "intent_confirmation" {
		t.Fatalf("expected fallback task to wait for confirmation, got %+v", task)
	}
	intentValue := task["intent"].(map[string]any)
	if intentValue["name"] != "agent_loop" {
		t.Fatalf("expected unavailable screen capability to downgrade into agent_loop, got %+v", intentValue)
	}
	if result["delivery_result"] != nil {
		t.Fatalf("expected confirming fallback to skip direct delivery, got %+v", result["delivery_result"])
	}
	bubble := result["bubble_message"].(map[string]any)
	if bubble["type"] != "intent_confirm" {
		t.Fatalf("expected confirmation bubble for downgraded task, got %+v", bubble)
	}
}

func TestSecurityRespondScreenAnalyzeFailureReconcilesTaskState(t *testing.T) {
	ocrStub := stubOCRWorkerClient{result: tools.OCRTextResult{Path: "temp/screen_local_0001/frame_0001.png", Text: "fatal build error", Language: "eng", Source: "ocr_worker_text"}}
	service, _ := newTestServiceWithExecutionWorkers(t, "unused", platform.LocalExecutionBackend{}, nil, sidecarclient.NewNoopPlaywrightSidecarClient(), ocrStub, sidecarclient.NewNoopMediaWorkerClient())
	result, err := service.StartTask(map[string]any{
		"session_id": "sess_screen_fail",
		"source":     "floating_ball",
		"trigger":    "hover_text_input",
		"input": map[string]any{
			"type": "text",
			"text": "请分析屏幕中的错误",
		},
		"intent": map[string]any{
			"name": "screen_analyze",
			"arguments": map[string]any{
				"path": "inputs/missing-screen.png",
			},
		},
	})
	if err != nil {
		t.Fatalf("start screen analyze task failed: %v", err)
	}
	taskID := result["task"].(map[string]any)["task_id"].(string)
	respondResult, err := service.SecurityRespond(map[string]any{
		"task_id":  taskID,
		"decision": "allow_once",
	})
	if err != nil {
		t.Fatalf("security respond allow_once failed: %v", err)
	}
	respondTask := respondResult["task"].(map[string]any)
	if respondTask["status"] != "failed" {
		t.Fatalf("expected failed task after approved screen capture error, got %+v", respondTask)
	}
	bubble := respondResult["bubble_message"].(map[string]any)
	if bubble["type"] != "status" {
		t.Fatalf("expected failure status bubble, got %+v", bubble)
	}
	record, exists := service.runEngine.GetTask(taskID)
	if !exists || record.Status != "failed" || record.PendingExecution != nil {
		t.Fatalf("expected runtime task to reconcile to failed terminal state, got %+v", record)
	}
}
