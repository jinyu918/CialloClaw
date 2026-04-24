package orchestrator

import (
	"fmt"
	"path"
	"strings"
	"time"

	contextsvc "github.com/cialloclaw/cialloclaw/services/local-service/internal/context"
	"github.com/cialloclaw/cialloclaw/services/local-service/internal/intent"
	"github.com/cialloclaw/cialloclaw/services/local-service/internal/runengine"
	"github.com/cialloclaw/cialloclaw/services/local-service/internal/tools"
)

func (s *Service) handleScreenAnalyzeStart(params map[string]any, snapshot contextsvc.TaskContextSnapshot, explicitIntent map[string]any) (map[string]any, bool, error) {
	if stringValue(explicitIntent, "name", "") != "screen_analyze" || s.executor == nil || s.executor.ScreenCapabilitySnapshot().Available == false {
		return nil, false, nil
	}
	resolvedIntent := s.resolveScreenAnalyzeIntent(snapshot, explicitIntent)
	task := s.runEngine.CreateTask(runengine.CreateTaskInput{
		SessionID:         stringValue(params, "session_id", ""),
		RequestSource:     stringValue(params, "source", ""),
		RequestTrigger:    stringValue(params, "trigger", ""),
		Title:             firstNonEmptyString(stringValue(resolvedIntent, "title", ""), inferredScreenTaskTitle(snapshot)),
		SourceType:        "screen_capture",
		Status:            "waiting_auth",
		Intent:            cloneMap(resolvedIntent),
		PreferredDelivery: "bubble",
		FallbackDelivery:  "bubble",
		CurrentStep:       "waiting_authorization",
		RiskLevel:         "yellow",
		Timeline:          initialTimeline("waiting_auth", "waiting_authorization"),
		Snapshot:          snapshot,
	})
	if queuedTask, queueBubble, queued, queueErr := s.queueTaskIfSessionBusy(task); queueErr != nil {
		return nil, false, queueErr
	} else if queued {
		return taskScreenAnalyzeResponse(queuedTask, queueBubble), true, nil
	}
	updatedTask, bubble, err := s.markTaskWaitingScreenApproval(task)
	if err != nil {
		return nil, false, err
	}
	return taskScreenAnalyzeResponse(updatedTask, bubble), true, nil
}

func (s *Service) handleScreenAnalyzeSuggestion(params map[string]any, snapshot contextsvc.TaskContextSnapshot, suggestion intent.Suggestion) (map[string]any, bool, error) {
	if stringValue(suggestion.Intent, "name", "") != "screen_analyze" || suggestion.RequiresConfirm {
		return nil, false, nil
	}
	return s.handleScreenAnalyzeStart(params, snapshot, suggestion.Intent)
}

func (s *Service) normalizeSuggestedIntentForAvailability(snapshot contextsvc.TaskContextSnapshot, suggestion intent.Suggestion, confirmRequired bool) intent.Suggestion {
	if stringValue(suggestion.Intent, "name", "") != "screen_analyze" {
		return suggestion
	}
	if s.executor != nil && s.executor.ScreenCapabilitySnapshot().Available {
		return suggestion
	}
	fallback := suggestion
	fallback.Intent = map[string]any{
		"name":      "agent_loop",
		"arguments": map[string]any{},
	}
	fallback.IntentConfirmed = true
	// Preserve the caller's confirmation gate when screen-specific handling is
	// unavailable so the downgrade does not auto-execute a generic task.
	fallback.RequiresConfirm = confirmRequired
	fallback.TaskSourceType = "hover_input"
	fallback.TaskTitle = "处理：" + inferredScreenFallbackSubject(snapshot)
	fallback.DirectDeliveryType = "bubble"
	fallback.ResultTitle = "处理结果"
	fallback.ResultPreview = "结果已通过气泡返回"
	fallback.ResultBubbleText = "当前环境暂不支持受控屏幕查看，已改为按现有文本和页面上下文继续处理。"
	return fallback
}

func inferredScreenFallbackSubject(snapshot contextsvc.TaskContextSnapshot) string {
	return truncateText(firstNonEmptyString(strings.TrimSpace(snapshot.Text), screenSubjectFromSnapshot(snapshot)), 18)
}

func (s *Service) markTaskWaitingScreenApproval(task runengine.TaskRecord) (runengine.TaskRecord, map[string]any, error) {
	approvalRequest, pendingExecution, bubble, err := s.buildScreenAnalysisApprovalState(task)
	if err != nil {
		return runengine.TaskRecord{}, nil, err
	}
	updatedTask, ok := s.runEngine.MarkWaitingApprovalWithPlan(task.TaskID, approvalRequest, pendingExecution, bubble)
	if !ok {
		return runengine.TaskRecord{}, nil, ErrTaskNotFound
	}
	if err := s.persistApprovalRequestState(updatedTask.TaskID, approvalRequest, mapValue(pendingExecution, "impact_scope")); err != nil {
		return runengine.TaskRecord{}, nil, err
	}
	return updatedTask, bubble, nil
}

func taskScreenAnalyzeResponse(task runengine.TaskRecord, bubble map[string]any) map[string]any {
	return map[string]any{
		"task":            taskMap(task),
		"bubble_message":  bubble,
		"delivery_result": nil,
	}
}

// buildScreenAnalysisApprovalState reconstructs the controlled approval plan
// from the task intent so queued resumes can re-enter the same authorization
// path instead of falling through to the generic executor.
func (s *Service) buildScreenAnalysisApprovalState(task runengine.TaskRecord) (map[string]any, map[string]any, map[string]any, error) {
	arguments := mapValue(task.Intent, "arguments")
	sourcePath := stringValue(arguments, "path", "")
	captureMode := screenCaptureModeForIntent(arguments)
	source := firstNonEmptyString(stringValue(arguments, "source", ""), "screen_capture")
	targetObject := screenTargetObject(arguments)
	approvalRequest := map[string]any{
		"approval_id":    fmt.Sprintf("appr_%s", task.TaskID),
		"task_id":        task.TaskID,
		"operation_name": "screen_capture",
		"risk_level":     "yellow",
		"target_object":  targetObject,
		"reason":         "screen_capture_requires_authorization",
		"status":         "pending",
		"created_at":     time.Now().Format(dateTimeLayout),
	}
	pendingExecution := map[string]any{
		"kind":           "screen_analysis",
		"operation_name": "screen_capture",
		"source_path":    sourcePath,
		"capture_mode":   string(captureMode),
		"source":         source,
		"target_object":  targetObject,
		"language":       firstNonEmptyString(stringValue(arguments, "language", ""), "eng"),
		"evidence_role":  firstNonEmptyString(stringValue(arguments, "evidence_role", ""), "error_evidence"),
		"delivery_type":  "bubble",
		"result_title":   "屏幕分析结果",
		"preview_text":   screenAnalysisPreviewText(captureMode),
		"impact_scope": map[string]any{
			"files":                    impactFilesForScreenTarget(sourcePath),
			"webpages":                 []string{},
			"apps":                     []string{},
			"out_of_workspace":         false,
			"overwrite_or_delete_risk": false,
		},
	}
	bubble := s.delivery.BuildBubbleMessage(task.TaskID, "status", "屏幕截图分析属于敏感能力，请先确认授权。", task.UpdatedAt.Format(dateTimeLayout))
	return approvalRequest, pendingExecution, bubble, nil
}

func (s *Service) resolveScreenAnalyzeIntent(snapshot contextsvc.TaskContextSnapshot, current map[string]any) map[string]any {
	updatedIntent := cloneMap(current)
	arguments := cloneMap(mapValue(updatedIntent, "arguments"))
	if arguments == nil {
		arguments = map[string]any{}
	}
	if strings.TrimSpace(stringValue(arguments, "language", "")) == "" {
		arguments["language"] = "eng"
	}
	if strings.TrimSpace(stringValue(arguments, "capture_mode", "")) == "" {
		arguments["capture_mode"] = string(screenCaptureModeForIntent(arguments))
	}
	if strings.TrimSpace(stringValue(arguments, "evidence_role", "")) == "" {
		arguments["evidence_role"] = inferredScreenEvidenceRole(snapshot, arguments)
	}
	if strings.TrimSpace(stringValue(arguments, "page_title", "")) == "" && strings.TrimSpace(snapshot.PageTitle) != "" {
		arguments["page_title"] = snapshot.PageTitle
	}
	if strings.TrimSpace(stringValue(arguments, "window_title", "")) == "" && strings.TrimSpace(snapshot.WindowTitle) != "" {
		arguments["window_title"] = snapshot.WindowTitle
	}
	if strings.TrimSpace(stringValue(arguments, "visible_text", "")) == "" && strings.TrimSpace(snapshot.VisibleText) != "" {
		arguments["visible_text"] = snapshot.VisibleText
	}
	if strings.TrimSpace(stringValue(arguments, "screen_summary", "")) == "" && strings.TrimSpace(snapshot.ScreenSummary) != "" {
		arguments["screen_summary"] = snapshot.ScreenSummary
	}
	updatedIntent["arguments"] = arguments
	if strings.TrimSpace(stringValue(updatedIntent, "title", "")) == "" {
		updatedIntent["title"] = inferredScreenTaskTitle(snapshot)
	}
	return updatedIntent
}

func screenCaptureModeForIntent(arguments map[string]any) tools.ScreenCaptureMode {
	switch strings.ToLower(strings.TrimSpace(stringValue(arguments, "capture_mode", ""))) {
	case string(tools.ScreenCaptureModeClip):
		return tools.ScreenCaptureModeClip
	case string(tools.ScreenCaptureModeKeyframe):
		return tools.ScreenCaptureModeKeyframe
	case string(tools.ScreenCaptureModeScreenshot):
		return tools.ScreenCaptureModeScreenshot
	}
	if isClipScreenSourcePath(stringValue(arguments, "path", "")) {
		return tools.ScreenCaptureModeClip
	}
	return tools.ScreenCaptureModeScreenshot
}

func isClipScreenSourcePath(pathValue string) bool {
	trimmedPath := strings.ToLower(strings.TrimSpace(pathValue))
	switch path.Ext(trimmedPath) {
	case ".mp4", ".webm", ".mov", ".mkv", ".avi":
		return true
	default:
		return false
	}
}

func inferredScreenTaskTitle(snapshot contextsvc.TaskContextSnapshot) string {
	target := screenSubjectFromSnapshot(snapshot)
	if strings.TrimSpace(snapshot.ErrorText) != "" || strings.Contains(strings.ToLower(snapshot.Text), "错误") || strings.Contains(strings.ToLower(snapshot.Text), "报错") || strings.Contains(strings.ToLower(snapshot.Text), "error") {
		return fmt.Sprintf("查看屏幕报错：%s", truncateText(target, 18))
	}
	return fmt.Sprintf("查看当前屏幕：%s", truncateText(target, 18))
}

func screenSubjectFromSnapshot(snapshot contextsvc.TaskContextSnapshot) string {
	return firstNonEmptyString(
		snapshot.PageTitle,
		firstNonEmptyString(
			snapshot.WindowTitle,
			firstNonEmptyString(snapshot.ScreenSummary, firstNonEmptyString(snapshot.VisibleText, "当前屏幕")),
		),
	)
}

func screenTargetObject(arguments map[string]any) string {
	if sourcePath := stringValue(arguments, "path", ""); strings.TrimSpace(sourcePath) != "" {
		return sourcePath
	}
	for _, value := range []string{
		stringValue(arguments, "page_title", ""),
		stringValue(arguments, "window_title", ""),
		stringValue(arguments, "screen_summary", ""),
		stringValue(arguments, "visible_text", ""),
	} {
		if strings.TrimSpace(value) != "" {
			return truncateText(value, 64)
		}
	}
	return "current_screen"
}

func screenCaptureModeFromArguments(arguments map[string]any) tools.ScreenCaptureMode {
	mode := tools.ScreenCaptureMode(strings.TrimSpace(stringValue(arguments, "capture_mode", string(tools.ScreenCaptureModeScreenshot))))
	switch mode {
	case tools.ScreenCaptureModeScreenshot, tools.ScreenCaptureModeKeyframe, tools.ScreenCaptureModeClip:
		return mode
	default:
		return tools.ScreenCaptureModeScreenshot
	}
}

func screenAnalysisPreviewText(captureMode tools.ScreenCaptureMode) string {
	if captureMode == tools.ScreenCaptureModeClip {
		return "已准备分析屏幕录屏片段"
	}
	return "已准备分析屏幕截图"
}

func impactFilesForScreenTarget(sourcePath string) []string {
	if strings.TrimSpace(sourcePath) == "" {
		return []string{}
	}
	return []string{sourcePath}
}

func inferredScreenEvidenceRole(snapshot contextsvc.TaskContextSnapshot, arguments map[string]any) string {
	if role := stringValue(arguments, "evidence_role", ""); strings.TrimSpace(role) != "" {
		return role
	}
	combined := strings.ToLower(strings.Join([]string{snapshot.Text, snapshot.ErrorText, snapshot.VisibleText, snapshot.ScreenSummary}, " "))
	if strings.Contains(combined, "error") || strings.Contains(combined, "warning") || strings.Contains(combined, "报错") || strings.Contains(combined, "错误") || strings.Contains(combined, "异常") {
		return "error_evidence"
	}
	return "page_context"
}
