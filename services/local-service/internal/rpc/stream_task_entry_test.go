package rpc

import (
	"encoding/json"
	"net"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cialloclaw/cialloclaw/services/local-service/internal/model"
)

// TestHandleStreamConnEmitsApprovalNotifications verifies that approval notifications
// are emitted on the stream connection after task confirmation enters waiting_auth.
func TestHandleStreamConnEmitsApprovalNotifications(t *testing.T) {
	server := newTestServer()
	startResult, err := server.orchestrator.StartTask(map[string]any{
		"session_id": "sess_demo",
		"source":     "floating_ball",
		"trigger":    "text_selected_click",
		"input": map[string]any{
			"type": "text_selection",
			"text": "请生成一个文件版本",
		},
	})
	if err != nil {
		t.Fatalf("seed task.start: %v", err)
	}
	taskID := startResult["task"].(map[string]any)["task_id"].(string)
	if startResult["task"].(map[string]any)["status"] != "confirming_intent" {
		t.Fatalf("expected seeded task to wait for confirm, got %+v", startResult["task"])
	}
	left, right := net.Pipe()
	defer left.Close()
	defer right.Close()

	go server.handleStreamConn(left)

	encoder := json.NewEncoder(right)
	decoder := json.NewDecoder(right)

	request := requestEnvelope{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`"req-1"`),
		Method:  "agent.task.confirm",
		Params: mustMarshal(t, map[string]any{
			"task_id":   taskID,
			"confirmed": false,
			"corrected_intent": map[string]any{
				"name": "write_file",
				"arguments": map[string]any{
					"require_authorization": true,
					"target_path":           "workspace_document",
				},
			},
		}),
	}

	if err := encoder.Encode(request); err != nil {
		t.Fatalf("encode request: %v", err)
	}

	var response successEnvelope
	if err := decoder.Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.Result.Data.(map[string]any)["task"].(map[string]any)["status"] != "waiting_auth" {
		t.Fatalf("expected waiting_auth task status in response")
	}

	if err := right.SetReadDeadline(time.Now().Add(200 * time.Millisecond)); err != nil {
		t.Fatalf("set read deadline: %v", err)
	}

	seenApprovalPending := false
	for index := 0; index < 8; index++ {
		var notification notificationEnvelope
		if err := decoder.Decode(&notification); err != nil {
			break
		}
		if notification.Method == "approval.pending" {
			seenApprovalPending = true
		}
	}

	if !seenApprovalPending {
		t.Fatal("expected approval.pending notification to be emitted on stream connection")
	}
}

func TestHandleStreamConnEmitsLoopLifecycleNotifications(t *testing.T) {
	server := newTestServer()
	startResult, err := server.orchestrator.StartTask(map[string]any{
		"session_id": "sess_loop_notify",
		"source":     "floating_ball",
		"trigger":    "hover_text_input",
		"input": map[string]any{
			"type": "text",
			"text": "Inspect the workspace and answer.",
		},
		"intent": map[string]any{
			"name": "summarize",
			"arguments": map[string]any{
				"style": "key_points",
			},
		},
	})
	if err != nil {
		t.Fatalf("seed task.start: %v", err)
	}
	taskID := startResult["task"].(map[string]any)["task_id"].(string)
	if _, ok := server.orchestrator.RunEngine().EmitRuntimeNotification(taskID, "loop.round.completed", map[string]any{
		"loop_round":  1,
		"stop_reason": "completed",
	}); !ok {
		t.Fatal("expected runtime notification injection to succeed")
	}
	left, right := net.Pipe()
	defer left.Close()
	defer right.Close()

	go server.handleStreamConn(left)

	encoder := json.NewEncoder(right)
	decoder := json.NewDecoder(right)

	request := requestEnvelope{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`"req-task-detail"`),
		Method:  "agent.task.detail.get",
		Params: mustMarshal(t, map[string]any{
			"task_id": taskID,
		}),
	}

	if err := encoder.Encode(request); err != nil {
		t.Fatalf("encode request: %v", err)
	}

	var response successEnvelope
	if err := decoder.Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.Result.Data.(map[string]any)["task"].(map[string]any)["task_id"] != taskID {
		t.Fatalf("expected task detail response for %s, got %+v", taskID, response)
	}

	if err := right.SetReadDeadline(time.Now().Add(300 * time.Millisecond)); err != nil {
		t.Fatalf("set read deadline: %v", err)
	}
	seenLoopNotification := false
	for index := 0; index < 12; index++ {
		var notification notificationEnvelope
		if err := decoder.Decode(&notification); err != nil {
			break
		}
		if strings.HasPrefix(notification.Method, "loop.") {
			seenLoopNotification = true
			break
		}
	}
	if !seenLoopNotification {
		t.Fatal("expected loop.* notification to be emitted on stream connection")
	}
}

func TestHandleStreamConnStreamsLoopLifecycleNotificationsBeforeResponse(t *testing.T) {
	modelClient := &stubLoopModelClient{
		toolResult: model.ToolCallResult{
			OutputText: "Loop runtime finished in-flight.",
		},
		generateToolWait: make(chan struct{}),
		generateToolSeen: make(chan struct{}),
	}
	server := newTestServerWithModelClient(modelClient)
	startResult, err := server.orchestrator.StartTask(map[string]any{
		"session_id": "sess_loop_stream",
		"source":     "floating_ball",
		"trigger":    "text_selected_click",
		"input": map[string]any{
			"type": "text_selection",
			"text": "inspect this workspace",
		},
	})
	if err != nil {
		t.Fatalf("seed task.start: %v", err)
	}
	taskID := startResult["task"].(map[string]any)["task_id"].(string)
	if startResult["task"].(map[string]any)["status"] != "confirming_intent" {
		t.Fatalf("expected seeded task to wait for confirm, got %+v", startResult["task"])
	}
	left, right := net.Pipe()
	defer left.Close()
	defer right.Close()

	go server.handleStreamConn(left)

	encoder := json.NewEncoder(right)
	decoder := json.NewDecoder(right)
	request := requestEnvelope{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`"req-loop-stream"`),
		Method:  "agent.task.confirm",
		Params: mustMarshal(t, map[string]any{
			"task_id":   taskID,
			"confirmed": false,
			"corrected_intent": map[string]any{
				"name":      "agent_loop",
				"arguments": map[string]any{},
			},
		}),
	}

	if err := encoder.Encode(request); err != nil {
		t.Fatalf("encode request: %v", err)
	}
	if err := right.SetReadDeadline(time.Now().Add(500 * time.Millisecond)); err != nil {
		t.Fatalf("set read deadline: %v", err)
	}

	var firstEnvelope map[string]any
	if err := decoder.Decode(&firstEnvelope); err != nil {
		t.Fatalf("decode first envelope: %v", err)
	}
	if method, _ := firstEnvelope["method"].(string); !strings.HasPrefix(method, "loop.") {
		t.Fatalf("expected first streamed envelope to be loop.* notification, got %+v", firstEnvelope)
	}
	if err := right.SetReadDeadline(time.Time{}); err != nil {
		t.Fatalf("clear read deadline: %v", err)
	}

	close(modelClient.generateToolWait)

	if err := right.SetReadDeadline(time.Now().Add(500 * time.Millisecond)); err != nil {
		t.Fatalf("set response deadline: %v", err)
	}
	responseSeen := false
	for index := 0; index < 8; index++ {
		var envelope map[string]any
		if err := decoder.Decode(&envelope); err != nil {
			t.Fatalf("decode response envelope: %v", err)
		}
		if envelope["id"] == nil {
			continue
		}
		result, ok := envelope["result"].(map[string]any)
		if !ok {
			t.Fatalf("expected success result envelope, got %+v", envelope)
		}
		data, ok := result["data"].(map[string]any)
		if !ok {
			t.Fatalf("expected response data payload, got %+v", envelope)
		}
		task, ok := data["task"].(map[string]any)
		if !ok || task["status"] != "completed" {
			t.Fatalf("expected completed task response, got %+v", envelope)
		}
		responseSeen = true
		break
	}
	if !responseSeen {
		t.Fatal("expected final response after streamed loop notifications")
	}
}

func TestHandleStreamConnStreamsLoopLifecycleNotificationsBeforeResponseForSubmitInput(t *testing.T) {
	modelClient := &stubLoopModelClient{
		toolResult: model.ToolCallResult{
			OutputText: "Loop runtime finished from input.submit.",
		},
		generateToolWait: make(chan struct{}),
		generateToolSeen: make(chan struct{}),
	}
	server := newTestServerWithModelClient(modelClient)

	left, right := net.Pipe()
	defer left.Close()
	defer right.Close()

	go server.handleStreamConn(left)

	encoder := json.NewEncoder(right)
	decoder := json.NewDecoder(right)
	request := requestEnvelope{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`"req-input-submit-loop-stream"`),
		Method:  "agent.input.submit",
		Params: mustMarshal(t, map[string]any{
			"session_id": "sess_input_submit_loop_stream",
			"input": map[string]any{
				"type": "text",
				"text": "inspect this workspace and answer directly",
			},
			"options": map[string]any{
				"confirm_required": false,
			},
		}),
	}

	if err := encoder.Encode(request); err != nil {
		t.Fatalf("encode request: %v", err)
	}
	if err := right.SetReadDeadline(time.Now().Add(500 * time.Millisecond)); err != nil {
		t.Fatalf("set read deadline: %v", err)
	}

	var firstEnvelope map[string]any
	if err := decoder.Decode(&firstEnvelope); err != nil {
		t.Fatalf("decode first envelope: %v", err)
	}
	if method, _ := firstEnvelope["method"].(string); !strings.HasPrefix(method, "loop.") {
		t.Fatalf("expected first streamed envelope to be loop.* notification, got %+v", firstEnvelope)
	}
	if err := right.SetReadDeadline(time.Time{}); err != nil {
		t.Fatalf("clear read deadline: %v", err)
	}

	close(modelClient.generateToolWait)

	if err := right.SetReadDeadline(time.Now().Add(500 * time.Millisecond)); err != nil {
		t.Fatalf("set response deadline: %v", err)
	}
	responseSeen := false
	for index := 0; index < 8; index++ {
		var envelope map[string]any
		if err := decoder.Decode(&envelope); err != nil {
			t.Fatalf("decode response envelope: %v", err)
		}
		if envelope["id"] == nil {
			continue
		}
		result, ok := envelope["result"].(map[string]any)
		if !ok {
			t.Fatalf("expected success result envelope, got %+v", envelope)
		}
		data, ok := result["data"].(map[string]any)
		if !ok {
			t.Fatalf("expected response data payload, got %+v", envelope)
		}
		task, ok := data["task"].(map[string]any)
		if !ok || task["status"] != "completed" {
			t.Fatalf("expected completed task response, got %+v", envelope)
		}
		responseSeen = true
		break
	}
	if !responseSeen {
		t.Fatal("expected final response after streamed loop notifications")
	}
}

func TestHandleStreamConnDoesNotReplayStreamedRuntimeNotificationsAfterResponse(t *testing.T) {
	modelClient := &stubLoopModelClient{
		toolResult: model.ToolCallResult{
			OutputText: "Loop runtime should not replay live events.",
		},
		generateToolWait: make(chan struct{}),
		generateToolSeen: make(chan struct{}),
	}
	server := newTestServerWithModelClient(modelClient)

	left, right := net.Pipe()
	defer left.Close()
	defer right.Close()

	go server.handleStreamConn(left)

	encoder := json.NewEncoder(right)
	decoder := json.NewDecoder(right)
	request := requestEnvelope{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`"req-loop-no-replay"`),
		Method:  "agent.input.submit",
		Params: mustMarshal(t, map[string]any{
			"session_id": "sess_input_submit_no_replay",
			"input": map[string]any{
				"type": "text",
				"text": "inspect this workspace and answer directly",
			},
			"options": map[string]any{
				"confirm_required": false,
			},
		}),
	}

	if err := encoder.Encode(request); err != nil {
		t.Fatalf("encode request: %v", err)
	}
	if err := right.SetReadDeadline(time.Now().Add(500 * time.Millisecond)); err != nil {
		t.Fatalf("set first notification deadline: %v", err)
	}

	var firstEnvelope notificationEnvelope
	if err := decoder.Decode(&firstEnvelope); err != nil {
		t.Fatalf("decode first notification: %v", err)
	}
	if !strings.HasPrefix(firstEnvelope.Method, "loop.") {
		t.Fatalf("expected first streamed envelope to be loop.* notification, got %+v", firstEnvelope)
	}
	if err := right.SetReadDeadline(time.Time{}); err != nil {
		t.Fatalf("clear read deadline: %v", err)
	}

	close(modelClient.generateToolWait)

	if err := right.SetReadDeadline(time.Now().Add(500 * time.Millisecond)); err != nil {
		t.Fatalf("set response deadline: %v", err)
	}
	responseSeen := false
	for index := 0; index < 8; index++ {
		var envelope map[string]any
		if err := decoder.Decode(&envelope); err != nil {
			t.Fatalf("decode response envelope: %v", err)
		}
		if envelope["id"] == nil {
			continue
		}
		responseSeen = true
		break
	}
	if !responseSeen {
		t.Fatal("expected final response after streamed loop notifications")
	}

	if err := right.SetReadDeadline(time.Now().Add(250 * time.Millisecond)); err != nil {
		t.Fatalf("set replay deadline: %v", err)
	}
	for {
		var envelope notificationEnvelope
		if err := decoder.Decode(&envelope); err != nil {
			break
		}
		if isLiveRuntimeMethod(envelope.Method) {
			t.Fatalf("expected streamed runtime notifications to be skipped after response, got %+v", envelope)
		}
	}
	if err := right.SetReadDeadline(time.Time{}); err != nil {
		t.Fatalf("clear replay deadline: %v", err)
	}
}

func TestHandleStreamConnFiltersRuntimeNotificationsToRequestTask(t *testing.T) {
	modelClient := &selectiveWaitLoopModelClient{
		stubLoopModelClient: stubLoopModelClient{
			toolResult: model.ToolCallResult{
				OutputText: "Scoped runtime finished.",
			},
			generateToolWait: make(chan struct{}),
		},
	}
	server := newTestServerWithModelClient(modelClient)

	startTask := func(sessionID string) string {
		t.Helper()
		result, err := server.orchestrator.StartTask(map[string]any{
			"session_id": sessionID,
			"source":     "floating_ball",
			"trigger":    "text_selected_click",
			"input": map[string]any{
				"type": "text_selection",
				"text": "inspect this workspace",
			},
		})
		if err != nil {
			t.Fatalf("seed task.start for %s: %v", sessionID, err)
		}
		return result["task"].(map[string]any)["task_id"].(string)
	}

	taskA := startTask("sess_loop_scope_a")
	taskB := startTask("sess_loop_scope_b")
	modelClient.blockedTaskID = taskA

	left, right := net.Pipe()
	defer left.Close()
	defer right.Close()

	go server.handleStreamConn(left)

	encoder := json.NewEncoder(right)
	decoder := json.NewDecoder(right)
	request := requestEnvelope{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`"req-loop-scope"`),
		Method:  "agent.task.confirm",
		Params: mustMarshal(t, map[string]any{
			"task_id":   taskA,
			"confirmed": false,
			"corrected_intent": map[string]any{
				"name":      "agent_loop",
				"arguments": map[string]any{},
			},
		}),
	}

	if err := encoder.Encode(request); err != nil {
		t.Fatalf("encode request: %v", err)
	}
	if err := right.SetReadDeadline(time.Now().Add(500 * time.Millisecond)); err != nil {
		t.Fatalf("set first notification deadline: %v", err)
	}

	var firstEnvelope notificationEnvelope
	if err := decoder.Decode(&firstEnvelope); err != nil {
		t.Fatalf("decode first notification: %v", err)
	}
	if !strings.HasPrefix(firstEnvelope.Method, "loop.") {
		t.Fatalf("expected first streamed envelope to be loop.* notification, got %+v", firstEnvelope)
	}

	if _, err := server.orchestrator.TaskSteer(map[string]any{
		"task_id": taskB,
		"message": "Mention the unrelated steering marker.",
	}); err != nil {
		t.Fatalf("queue steering for unrelated task: %v", err)
	}

	confirmDone := make(chan error, 1)
	go func() {
		_, err := server.orchestrator.ConfirmTask(map[string]any{
			"task_id":   taskB,
			"confirmed": false,
			"corrected_intent": map[string]any{
				"name":      "agent_loop",
				"arguments": map[string]any{},
			},
		})
		confirmDone <- err
	}()

	if err := right.SetReadDeadline(time.Now().Add(250 * time.Millisecond)); err != nil {
		t.Fatalf("set scoped notification deadline: %v", err)
	}
	for {
		var envelope notificationEnvelope
		if err := decoder.Decode(&envelope); err != nil {
			break
		}
		params, ok := envelope.Params.(map[string]any)
		if !ok {
			t.Fatalf("expected notification params map, got %+v", envelope)
		}
		taskID := stringValue(params, "task_id", "")
		if taskID == taskB {
			t.Fatalf("expected stream to suppress unrelated runtime notification for task %s, got %+v", taskB, envelope)
		}
	}
	if err := right.SetReadDeadline(time.Time{}); err != nil {
		t.Fatalf("clear read deadline: %v", err)
	}

	select {
	case err := <-confirmDone:
		if err != nil {
			t.Fatalf("confirm unrelated task: %v", err)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("expected unrelated task confirmation to complete")
	}

	close(modelClient.generateToolWait)
}

// TestHandleDebugEventsReturnsQueuedNotifications verifies that queued
// notifications can be fetched through the debug events endpoint.
func TestHandleDebugEventsReturnsQueuedNotifications(t *testing.T) {
	server := newTestServer()
	result, err := server.orchestrator.StartTask(map[string]any{
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
		t.Fatalf("start task: %v", err)
	}

	taskID := result["task"].(map[string]any)["task_id"].(string)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest("GET", "/events?task_id="+taskID, nil)
	server.handleDebugEvents(recorder, request)

	if recorder.Code != 200 {
		t.Fatalf("expected 200 status, got %d", recorder.Code)
	}

	var payload map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode payload: %v", err)
	}

	items := payload["items"].([]any)
	if len(items) == 0 {
		t.Fatal("expected queued notifications to be returned")
	}
}
