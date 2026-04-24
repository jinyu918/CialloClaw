package orchestrator

import (
	"strings"
	"testing"
	"time"

	contextsvc "github.com/cialloclaw/cialloclaw/services/local-service/internal/context"
	"github.com/cialloclaw/cialloclaw/services/local-service/internal/platform"
	"github.com/cialloclaw/cialloclaw/services/local-service/internal/runengine"
)

func TestResolveTaskContinuationContextUsesSingleActiveSession(t *testing.T) {
	service := newTestService()
	activeTask := service.runEngine.CreateTask(runengine.CreateTaskInput{
		SessionID:   "sess_active",
		Title:       "Analyze the current failure",
		SourceType:  "hover_input",
		Status:      "processing",
		CurrentStep: "agent_loop",
		RiskLevel:   "yellow",
	})

	continuationContext := service.resolveTaskContinuationContext("")
	if continuationContext.SessionID != activeTask.SessionID {
		t.Fatalf("expected active session %s, got %+v", activeTask.SessionID, continuationContext)
	}
	if continuationContext.SessionMode != "implicit_active" {
		t.Fatalf("expected implicit_active session mode, got %+v", continuationContext)
	}
	if len(continuationContext.Candidates) != 1 || continuationContext.Candidates[0].TaskID != activeTask.TaskID {
		t.Fatalf("expected active task to remain the only continuation candidate, got %+v", continuationContext.Candidates)
	}
}

func TestResolveTaskContinuationContextSkipsWaitingAuthorizationTasks(t *testing.T) {
	service := newTestService()
	service.runEngine.CreateTask(runengine.CreateTaskInput{
		SessionID:   "sess_waiting_auth",
		Title:       "Write into a file after approval",
		SourceType:  "hover_input",
		Status:      "waiting_auth",
		CurrentStep: "waiting_authorization",
		RiskLevel:   "yellow",
	})

	continuationContext := service.resolveTaskContinuationContext("")
	if continuationContext.SessionID != "" || len(continuationContext.Candidates) != 0 {
		t.Fatalf("expected waiting_auth task to stay out of implicit continuation candidates, got %+v", continuationContext)
	}
}

func TestResolveTaskContinuationContextSkipsPausedTasks(t *testing.T) {
	service := newTestService()
	service.runEngine.CreateTask(runengine.CreateTaskInput{
		SessionID:   "sess_paused",
		Title:       "Summarize the incident report",
		SourceType:  "hover_input",
		Status:      "paused",
		CurrentStep: "generate_output",
		RiskLevel:   "green",
	})

	continuationContext := service.resolveTaskContinuationContext("")
	if continuationContext.SessionID != "" || len(continuationContext.Candidates) != 0 {
		t.Fatalf("expected paused task to stay out of implicit continuation candidates, got %+v", continuationContext)
	}
}

func TestBuildTaskContinuationPromptRedactsSensitivePayloads(t *testing.T) {
	snapshot := contextsvc.TaskContextSnapshot{
		Trigger:       "hover_text_input",
		InputType:     "text",
		Text:          "Focus on the database timeout and keep the scope narrow.",
		SelectionText: "panic: dial tcp timeout",
		ErrorText:     "database timeout",
		Files:         []string{"logs/network.log"},
		PageTitle:     "Internal dashboard",
		PageURL:       "https://internal.example/tasks/1",
		ScreenSummary: "database panel shows critical errors",
	}
	candidate := runengine.TaskRecord{
		TaskID:      "task_001",
		SessionID:   "sess_secret",
		Title:       "Investigate the production timeout",
		Status:      "processing",
		CurrentStep: "agent_loop",
		SourceType:  "hover_input",
		UpdatedAt:   time.Now().Add(-30 * time.Second),
		Intent:      map[string]any{"name": "agent_loop"},
		Snapshot: contextsvc.TaskContextSnapshot{
			Files:     []string{"logs/private.log"},
			PageTitle: "Production dashboard",
		},
	}

	prompt := buildTaskContinuationPrompt(snapshot, map[string]any{
		"name": "write_file",
		"arguments": map[string]any{
			"path": "C:/secrets/todo.md",
		},
	}, taskContinuationContext{
		SessionMode: "implicit_active",
		Candidates:  []runengine.TaskRecord{candidate},
	})

	for _, sensitive := range []string{
		snapshot.Text,
		snapshot.SelectionText,
		snapshot.ErrorText,
		snapshot.PageURL,
		"logs/network.log",
		"logs/private.log",
		"C:/secrets/todo.md",
		candidate.SessionID,
		candidate.Title,
	} {
		if strings.Contains(prompt, sensitive) {
			t.Fatalf("expected prompt to redact %q, got %s", sensitive, prompt)
		}
	}
	if !strings.Contains(prompt, "task_id=task_001") {
		t.Fatalf("expected prompt to retain stable task identifiers, got %s", prompt)
	}
	for _, expected := range []string{
		"resolved_intent_name=write_file",
		"resolved_delivery_type=workspace_document",
		"intent_name=agent_loop",
		"delivery_type=bubble",
	} {
		if !strings.Contains(prompt, expected) {
			t.Fatalf("expected prompt to include %q, got %s", expected, prompt)
		}
	}
	if strings.Contains(prompt, "continuation_markers=") {
		t.Fatalf("expected prompt to stop relying on continuation markers, got %s", prompt)
	}
}

func TestCanContinueTaskOnlyAllowsExplicitFollowUpAndProcessingStates(t *testing.T) {
	for _, status := range []string{"waiting_input", "confirming_intent", "processing"} {
		if !canContinueTask(runengine.TaskRecord{Status: status}) {
			t.Fatalf("expected %s to remain continuation-eligible", status)
		}
	}
	for _, status := range []string{"waiting_auth", "paused", "blocked", "failed", "completed"} {
		if canContinueTask(runengine.TaskRecord{Status: status}) {
			t.Fatalf("expected %s to be excluded from continuation eligibility", status)
		}
	}
}

func TestClassifyTaskContinuationContinuesExplicitWaitingTaskWithoutSignalWords(t *testing.T) {
	service := newTestService()
	service.model = nil

	decision := service.classifyTaskContinuation(
		contextsvc.TaskContextSnapshot{
			Trigger:   "hover_text_input",
			InputType: "text",
			Text:      "把输出换成表格格式。",
		},
		nil,
		taskContinuationContext{
			SessionID:   "sess_active",
			SessionMode: "explicit_active",
			Candidates: []runengine.TaskRecord{{
				TaskID:      "task_001",
				SessionID:   "sess_active",
				Status:      "waiting_input",
				CurrentStep: "collect_input",
				UpdatedAt:   time.Now().Add(-10 * time.Second),
			}},
		},
	)

	if decision.Decision != "continue" || decision.TaskID != "task_001" {
		t.Fatalf("expected explicit waiting task to continue without signal words, got %+v", decision)
	}
}

func TestClassifyTaskContinuationStartsNewTaskForExplicitIntentWithoutAnchors(t *testing.T) {
	service := newTestService()
	service.model = nil

	decision := service.classifyTaskContinuation(
		contextsvc.TaskContextSnapshot{
			Trigger:   "hover_text_input",
			InputType: "text",
			Text:      "顺便帮我写一份周报。",
		},
		map[string]any{
			"name": "write_file",
			"arguments": map[string]any{
				"target_path": "workspace/reports/weekly.md",
			},
		},
		taskContinuationContext{
			SessionMode: "implicit_active",
			Candidates: []runengine.TaskRecord{{
				TaskID:      "task_001",
				Status:      "waiting_input",
				CurrentStep: "collect_input",
				UpdatedAt:   time.Now().Add(-10 * time.Second),
			}},
		},
	)

	if decision.Decision != "new_task" {
		t.Fatalf("expected explicit intent without anchors to open a new task, got %+v", decision)
	}
}

func TestClassifyTaskContinuationRejectsWaitingTaskWhenAnchorsConflict(t *testing.T) {
	service := newTestService()
	service.model = nil

	decision := service.classifyTaskContinuation(
		contextsvc.TaskContextSnapshot{
			Trigger:   "hover_text_input",
			InputType: "text",
			Text:      "检查新的报错。",
			PageURL:   "https://example.com/build-b",
			AppName:   "Chrome",
		},
		nil,
		taskContinuationContext{
			SessionMode: "implicit_active",
			Candidates: []runengine.TaskRecord{{
				TaskID:      "task_001",
				Status:      "waiting_input",
				CurrentStep: "collect_input",
				UpdatedAt:   time.Now().Add(-10 * time.Second),
				Snapshot: contextsvc.TaskContextSnapshot{
					PageURL: "https://example.com/build-a",
					AppName: "Chrome",
				},
			}},
		},
	)

	if decision.Decision != "new_task" {
		t.Fatalf("expected conflicting anchors to force a new task, got %+v", decision)
	}
}

func TestClassifyTaskContinuationContinuesProcessingTaskOnStrongAttachmentEvidence(t *testing.T) {
	service := newTestService()
	service.model = nil

	decision := service.classifyTaskContinuation(
		contextsvc.TaskContextSnapshot{
			Trigger:   "file_drop",
			InputType: "file",
			Files:     []string{"logs/network.log"},
			PageURL:   "https://example.com/build",
			AppName:   "Chrome",
		},
		nil,
		taskContinuationContext{
			SessionMode: "implicit_active",
			Candidates: []runengine.TaskRecord{{
				TaskID:      "task_001",
				Status:      "processing",
				CurrentStep: "agent_loop",
				UpdatedAt:   time.Now().Add(-10 * time.Second),
				Snapshot: contextsvc.TaskContextSnapshot{
					PageURL: "https://example.com/build",
					AppName: "Chrome",
				},
			}},
		},
	)

	if decision.Decision != "continue" || decision.TaskID != "task_001" {
		t.Fatalf("expected strong context plus attachment evidence to continue the processing task, got %+v", decision)
	}
}

func TestContinuePendingTaskPromotesScreenAnalyzeToWaitingAuth(t *testing.T) {
	service, _ := newTestServiceWithExecutionOptions(t, "unused", platform.LocalExecutionBackend{}, nil)
	task := service.runEngine.CreateTask(runengine.CreateTaskInput{
		SessionID:   "sess_continue_screen",
		Title:       "等待补充输入",
		SourceType:  "hover_input",
		Status:      "waiting_input",
		CurrentStep: "collect_input",
		RiskLevel:   "green",
		Snapshot: contextsvc.TaskContextSnapshot{
			PageTitle:   "Build Dashboard",
			WindowTitle: "Build Dashboard",
			AppName:     "Chrome",
		},
	})

	result, err := service.continuePendingTask(task, contextsvc.TaskContextSnapshot{
		InputType:     "text",
		Text:          "请查看当前页面报错",
		PageTitle:     "Build Dashboard",
		WindowTitle:   "Build Dashboard",
		AppName:       "Chrome",
		VisibleText:   "fatal build error",
		ScreenSummary: "build failed on current page",
	}, nil)
	if err != nil {
		t.Fatalf("continue pending task with inferred screen analyze failed: %v", err)
	}

	updatedTask := result["task"].(map[string]any)
	if updatedTask["task_id"] != task.TaskID {
		t.Fatalf("expected continuation to reuse task identity, got %+v", updatedTask)
	}
	if updatedTask["status"] != "waiting_auth" {
		t.Fatalf("expected inferred screen continuation to enter waiting_auth, got %+v", updatedTask)
	}
	if result["delivery_result"] != nil {
		t.Fatalf("expected inferred screen continuation to wait for authorization, got %+v", result)
	}
	approvalRequests, total := service.runEngine.PendingApprovalRequests(20, 0)
	if total != 1 || len(approvalRequests) != 1 {
		t.Fatalf("expected one pending approval request after continuation, got total=%d items=%+v", total, approvalRequests)
	}
	if approvalRequests[0]["task_id"] != task.TaskID {
		t.Fatalf("expected approval request to target continued task, got %+v", approvalRequests[0])
	}
}

func TestContinuePendingTaskUpdatesDeliveryPreferenceAndMemoryPlans(t *testing.T) {
	service, _ := newTestServiceWithExecutionOptions(t, "continuation output", platform.LocalExecutionBackend{}, nil)
	task := service.runEngine.CreateTask(runengine.CreateTaskInput{
		SessionID:   "sess_continue_delivery",
		Title:       "等待补充输入",
		SourceType:  "hover_input",
		Status:      "waiting_input",
		CurrentStep: "collect_input",
		RiskLevel:   "green",
	})

	longText := strings.Repeat("需要整理成本周的进展摘要，并且保留关键风险与后续计划。", 4)
	result, err := service.continuePendingTask(task, contextsvc.TaskContextSnapshot{
		InputType: "text",
		Text:      longText,
	}, nil)
	if err != nil {
		t.Fatalf("continue pending task for delivery hints failed: %v", err)
	}
	if result["delivery_result"] == nil {
		t.Fatalf("expected actionable continuation to complete delivery, got %+v", result)
	}
	storedTask, ok := service.runEngine.GetTask(task.TaskID)
	if !ok {
		t.Fatalf("expected continued task %s to remain queryable", task.TaskID)
	}
	if storedTask.PreferredDelivery != "workspace_document" {
		t.Fatalf("expected continuation to persist suggested workspace delivery, got %q", storedTask.PreferredDelivery)
	}
	if len(storedTask.MemoryReadPlans) == 0 {
		t.Fatalf("expected continuation to attach memory read plans, got %+v", storedTask)
	}
}

func TestHeuristicTaskContinuationDecisionDoesNotAutoMergeBareFileDropWithoutAnchors(t *testing.T) {
	decision := heuristicTaskContinuationDecision(
		contextsvc.TaskContextSnapshot{
			Trigger:   "file_drop",
			InputType: "file",
			Files:     []string{"logs/network.log"},
		},
		nil,
		taskContinuationContext{
			Candidates: []runengine.TaskRecord{{
				TaskID:      "task_001",
				Status:      "processing",
				CurrentStep: "agent_loop",
				SourceType:  "hover_input",
				UpdatedAt:   time.Now().Add(-10 * time.Second),
				Intent:      map[string]any{"name": "write_file"},
			}},
		},
	)

	if decision.Decision != "new_task" {
		t.Fatalf("expected bare file drop to stay a new task when fallback runs, got %+v", decision)
	}
}

func TestClassifyTaskContinuationDoesNotAutoMergeSameExplicitIntentName(t *testing.T) {
	service := newTestService()
	service.model = nil

	decision := service.classifyTaskContinuation(
		contextsvc.TaskContextSnapshot{
			Trigger:   "hover_text_input",
			InputType: "text",
			Text:      "Draft a new release checklist.",
		},
		map[string]any{
			"name": "write_file",
			"arguments": map[string]any{
				"path": "C:/secrets/release-checklist.md",
			},
		},
		taskContinuationContext{
			Candidates: []runengine.TaskRecord{{
				TaskID:      "task_001",
				Status:      "processing",
				CurrentStep: "agent_loop",
				SourceType:  "hover_input",
				UpdatedAt:   time.Now().Add(-10 * time.Second),
				Intent:      map[string]any{"name": "write_file"},
			}},
		},
	)

	if decision.Decision != "new_task" {
		t.Fatalf("expected same explicit intent name to stay a new task in fallback mode, got %+v", decision)
	}
}

func TestClassifyTaskContinuationDoesNotAutoMergeGenericFocusCueWithoutContext(t *testing.T) {
	service := newTestService()
	service.model = nil

	decision := service.classifyTaskContinuation(
		contextsvc.TaskContextSnapshot{
			Trigger:   "hover_text_input",
			InputType: "text",
			Text:      "Focus on drafting a release checklist.",
		},
		nil,
		taskContinuationContext{
			Candidates: []runengine.TaskRecord{{
				TaskID:      "task_001",
				Status:      "processing",
				CurrentStep: "agent_loop",
				SourceType:  "hover_input",
				UpdatedAt:   time.Now().Add(-10 * time.Second),
			}},
		},
	)

	if decision.Decision != "new_task" {
		t.Fatalf("expected generic focus cue without shared context to stay a new task in fallback mode, got %+v", decision)
	}
}
