package orchestrator

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/cialloclaw/cialloclaw/services/local-service/internal/perception"
	"github.com/cialloclaw/cialloclaw/services/local-service/internal/runengine"
	"github.com/cialloclaw/cialloclaw/services/local-service/internal/storage"
)

// DashboardOverviewGet handles `agent.dashboard.overview.get`.
func (s *Service) DashboardOverviewGet(params map[string]any) (map[string]any, error) {
	queryViews := newTaskQueryViews(s)
	unfinishedTasks := queryViews.tasks("unfinished", "updated_at", "desc")
	finishedTasks := queryViews.tasks("finished", "finished_at", "desc")
	pendingApprovals, runtimePendingTotal := s.runEngine.PendingApprovalRequests(20, 0)
	needStorageFallback := !queryViews.hasRuntimeState()

	pendingApprovals = pendingApprovalsFromTasks(unfinishedTasks)
	pendingTotal := mergedPendingApprovalTotal(unfinishedTasks, runtimePendingTotal)
	focusMode := boolValue(params, "focus_mode", false)
	requestedIncludes := stringSliceValue(params["include"])
	includeAll := len(requestedIncludes) == 0
	includeSet := make(map[string]struct{}, len(requestedIncludes))
	for _, value := range requestedIncludes {
		includeSet[value] = struct{}{}
	}

	focusTask, hasFocusTask := focusTaskForOverview(unfinishedTasks, finishedTasks)
	var focusSummary map[string]any
	if hasFocusTask && shouldIncludeOverviewField(includeAll, includeSet, "focus_summary") {
		focusSummary = map[string]any{
			"task_id":      focusTask.TaskID,
			"title":        focusTask.Title,
			"status":       focusTask.Status,
			"current_step": focusTask.CurrentStep,
			"next_action":  nextActionForTask(focusTask),
			"updated_at":   focusTask.UpdatedAt.Format(dateTimeLayout),
		}
	}

	allTasks := append(append([]runengine.TaskRecord{}, unfinishedTasks...), finishedTasks...)
	hasRestorePoint := latestRestorePointFromTasks(allTasks) != nil
	if !hasRestorePoint {
		hasRestorePoint = s.latestRestorePointFromStorage("") != nil
	}
	latestAudit := latestAuditRecordFromTasks(allTasks)
	if latestAudit == nil {
		latestAudit = s.latestAuditRecordFromStorage("")
	}
	quickActions := []string(nil)
	if shouldIncludeOverviewField(includeAll, includeSet, "quick_actions") {
		quickActions = buildDashboardQuickActions(hasFocusTask, pendingTotal, len(finishedTasks))
		if focusMode {
			quickActions = filterDashboardQuickActionsForFocus(quickActions)
		}
	}
	var globalState map[string]any
	if shouldIncludeOverviewField(includeAll, includeSet, "global_state") {
		// Only include global_state when runtime engine has active state
		// to avoid contradictory data in cold-start fallback scenarios.
		if !needStorageFallback {
			globalState = s.Snapshot()
		}
	}
	highValueSignal := []string(nil)
	if shouldIncludeOverviewField(includeAll, includeSet, "high_value_signal") {
		highValueSignal = buildDashboardSignalsWithAudit(unfinishedTasks, finishedTasks, pendingApprovals, latestAudit)
		if contextValue := mapValue(params, "context"); len(contextValue) > 0 {
			highValueSignal = append(highValueSignal, perception.BehaviorSignals(perception.CaptureContextSignals("dashboard", "hover", contextValue))...)
			highValueSignal = dedupeStringSlice(highValueSignal)
		}
		if focusMode {
			highValueSignal = filterDashboardSignalsForFocus(highValueSignal)
		}
	}
	var trustSummary map[string]any
	if shouldIncludeOverviewField(includeAll, includeSet, "trust_summary") {
		trustSummary = map[string]any{
			"risk_level":             aggregateRiskLevel(allTasks, pendingApprovals, s.risk.DefaultLevel()),
			"pending_authorizations": pendingTotal,
			"has_restore_point":      hasRestorePoint,
			"workspace_path":         workspacePathFromSettings(s.runEngine.Settings()),
		}
	}

	overview := map[string]any{}
	if shouldIncludeOverviewField(includeAll, includeSet, "focus_summary") {
		overview["focus_summary"] = focusSummary
	} else {
		overview["focus_summary"] = nil
	}
	if shouldIncludeOverviewField(includeAll, includeSet, "trust_summary") {
		overview["trust_summary"] = trustSummary
	} else {
		overview["trust_summary"] = nil
	}
	if shouldIncludeOverviewField(includeAll, includeSet, "quick_actions") {
		overview["quick_actions"] = quickActions
	} else {
		overview["quick_actions"] = []string{}
	}
	if shouldIncludeOverviewField(includeAll, includeSet, "global_state") {
		overview["global_state"] = globalState
	} else {
		overview["global_state"] = map[string]any{}
	}
	if shouldIncludeOverviewField(includeAll, includeSet, "high_value_signal") {
		overview["high_value_signal"] = highValueSignal
	} else {
		overview["high_value_signal"] = []string{}
	}

	return map[string]any{"overview": overview}, nil
}

func pendingApprovalsFromTasks(tasks []runengine.TaskRecord) []map[string]any {
	items := make([]map[string]any, 0, len(tasks))
	for _, task := range tasks {
		if task.Status != "waiting_auth" || len(task.ApprovalRequest) == 0 {
			continue
		}
		item := cloneMap(task.ApprovalRequest)
		if stringValue(item, "task_id", "") == "" {
			item["task_id"] = task.TaskID
		}
		if stringValue(item, "risk_level", "") == "" {
			item["risk_level"] = task.RiskLevel
		}
		items = append(items, item)
	}
	return items
}

func approvalRequestRecordsToItems(records []storage.ApprovalRequestRecord) []map[string]any {
	items := make([]map[string]any, 0, len(records))
	for _, record := range records {
		item := map[string]any{
			"approval_id":    record.ApprovalID,
			"task_id":        record.TaskID,
			"operation_name": record.OperationName,
			"risk_level":     record.RiskLevel,
			"target_object":  record.TargetObject,
			"reason":         record.Reason,
			"status":         record.Status,
			"created_at":     record.CreatedAt,
			"updated_at":     record.UpdatedAt,
		}
		if strings.TrimSpace(record.ImpactScopeJSON) != "" {
			var scope map[string]any
			if err := json.Unmarshal([]byte(record.ImpactScopeJSON), &scope); err == nil && len(scope) > 0 {
				item["impact_scope"] = scope
			}
		}
		items = append(items, item)
	}
	return items
}

// mergedPendingApprovalTotal prefers the task-centric merged view so mixed
// runtime and storage snapshots report one stable pending-authorization count.
func mergedPendingApprovalTotal(unfinishedTasks []runengine.TaskRecord, runtimePendingTotal int) int {
	pendingTotal := countPendingApprovalTasks(unfinishedTasks)
	if pendingTotal == 0 && runtimePendingTotal > 0 {
		return runtimePendingTotal
	}
	return pendingTotal
}

// DashboardModuleGet handles `agent.dashboard.module.get`.
func (s *Service) DashboardModuleGet(params map[string]any) (map[string]any, error) {
	module := stringValue(params, "module", "mirror")
	tab := stringValue(params, "tab", "daily_summary")
	queryViews := newTaskQueryViews(s)
	finishedTasks := queryViews.tasks("finished", "finished_at", "desc")
	unfinishedTasks := queryViews.tasks("unfinished", "updated_at", "desc")
	_, runtimePendingTotal := s.runEngine.PendingApprovalRequests(20, 0)
	pendingTotal := mergedPendingApprovalTotal(unfinishedTasks, runtimePendingTotal)
	latestAudit := latestAuditRecordFromTasks(append(append([]runengine.TaskRecord{}, unfinishedTasks...), finishedTasks...))
	if latestAudit == nil {
		latestAudit = s.latestAuditRecordFromStorage("")
	}
	pluginSummary := s.pluginRuntimeSummary()
	summary := map[string]any{
		"completed_tasks":     len(finishedTasks),
		"generated_outputs":   countGeneratedOutputs(finishedTasks),
		"authorizations_used": countAuthorizedTasks(unfinishedTasks, finishedTasks),
		"exceptions":          countExceptionTasks(unfinishedTasks, finishedTasks),
		"plugin_runtime":      pluginSummary,
	}
	highlights := buildDashboardModuleHighlightsWithAudit(unfinishedTasks, finishedTasks, pendingTotal, latestAudit)
	if module == "tasks" {
		summary = s.buildDashboardTaskModuleSummary(unfinishedTasks, finishedTasks, summary)
		highlights = s.buildDashboardTaskModuleHighlights(unfinishedTasks, finishedTasks, pendingTotal, latestAudit)
	}
	return map[string]any{
		"module":     module,
		"tab":        tab,
		"summary":    summary,
		"highlights": highlights,
	}, nil
}

// buildDashboardTaskModuleSummary keeps the generic dashboard module summary
// while exposing one task-focused runtime summary for the current focus task.
func (s *Service) buildDashboardTaskModuleSummary(unfinishedTasks, finishedTasks []runengine.TaskRecord, baseSummary map[string]any) map[string]any {
	summary := cloneMap(baseSummary)
	summary["processing_tasks"] = countTasksWithStatus(unfinishedTasks, "processing")
	summary["waiting_auth_tasks"] = countTasksWithStatus(unfinishedTasks, "waiting_auth")
	summary["blocked_tasks"] = countTasksWithStatus(unfinishedTasks, "blocked", "failed", "ended_unfinished", "paused")
	focusTask, ok := focusTaskForOverview(unfinishedTasks, finishedTasks)
	if !ok {
		return summary
	}
	summary["focus_task_id"] = focusTask.TaskID
	summary["focus_runtime_summary"] = s.buildDashboardFocusRuntimeSummary(focusTask)
	return summary
}

// buildDashboardTaskModuleHighlights turns the current focus task runtime into
// human-readable dashboard hints without adding a new protocol method.
func (s *Service) buildDashboardTaskModuleHighlights(unfinishedTasks, finishedTasks []runengine.TaskRecord, pendingTotal int, latestAudit map[string]any) []string {
	highlights := make([]string, 0, 6)
	focusTask, ok := focusTaskForOverview(unfinishedTasks, finishedTasks)
	if ok {
		runtimeSummary := s.buildDashboardFocusRuntimeSummary(focusTask)
		if focusTask.Status == "waiting_auth" {
			highlights = append(highlights, "焦点任务当前正在等待授权确认。")
		} else if focusTask.Status == "processing" {
			highlights = append(highlights, fmt.Sprintf("焦点任务仍在执行中，当前步骤为 %s。", firstNonEmptyString(focusTask.CurrentStep, "generate_output")))
		} else if focusTask.Status == "blocked" || focusTask.Status == "failed" || focusTask.Status == "paused" || focusTask.Status == "ended_unfinished" {
			highlights = append(highlights, fmt.Sprintf("焦点任务当前状态为 %s。", focusTask.Status))
		}
		if stopReason := strings.TrimSpace(stringValue(runtimeSummary, "loop_stop_reason", "")); stopReason != "" {
			highlights = append(highlights, fmt.Sprintf("最近停止原因：%s。", stopReason))
		}
		if latestEventType := strings.TrimSpace(stringValue(runtimeSummary, "latest_event_type", "")); latestEventType != "" {
			highlights = append(highlights, fmt.Sprintf("最近运行事件：%s。", latestEventType))
		}
		if steeringCount := intValue(runtimeSummary, "active_steering_count", 0); steeringCount > 0 {
			highlights = append(highlights, fmt.Sprintf("当前仍有 %d 条追加要求待消费。", steeringCount))
		}
	}
	highlights = append(highlights, buildDashboardModuleHighlightsWithAudit(unfinishedTasks, finishedTasks, pendingTotal, latestAudit)...)
	return dedupeStringSlice(highlights)
}

// buildDashboardFocusRuntimeSummary reuses the task detail runtime summary but
// allows dashboard cards to fall back to the latest in-memory runtime event
// when persistence has not yet flushed a loop event row.
func (s *Service) buildDashboardFocusRuntimeSummary(task runengine.TaskRecord) map[string]any {
	summary := s.buildTaskRuntimeSummary(task)
	if strings.TrimSpace(stringValue(summary, "latest_event_type", "")) != "" {
		return summary
	}
	latestEventType := strings.TrimSpace(stringValue(task.LatestEvent, "type", ""))
	if strings.HasPrefix(latestEventType, "loop.") || latestEventType == "task.steered" {
		summary["latest_event_type"] = latestEventType
	}
	return summary
}

// MirrorOverviewGet handles `agent.mirror.overview.get`.
func (s *Service) MirrorOverviewGet(params map[string]any) (map[string]any, error) {
	_ = params
	finishedTasks := newTaskQueryViews(s).tasks("finished", "finished_at", "desc")
	memoryReferences := collectMirrorReferences(finishedTasks)
	return map[string]any{
		"history_summary": buildMirrorHistorySummary(finishedTasks, memoryReferences),
		"daily_summary": map[string]any{
			"date":              time.Now().Format("2006-01-02"),
			"completed_tasks":   len(finishedTasks),
			"generated_outputs": countGeneratedOutputs(finishedTasks),
		},
		"profile":           buildMirrorProfile(finishedTasks),
		"memory_references": memoryReferences,
	}, nil
}

// SecuritySummaryGet handles `agent.security.summary.get`.
func (s *Service) SecuritySummaryGet() (map[string]any, error) {
	_, runtimePendingTotal := s.runEngine.PendingApprovalRequests(20, 0)
	queryViews := newTaskQueryViews(s)
	unfinishedTasks := queryViews.tasks("unfinished", "updated_at", "desc")
	finishedTasks := queryViews.tasks("finished", "finished_at", "desc")
	pendingTotal := mergedPendingApprovalTotal(unfinishedTasks, runtimePendingTotal)
	allTasks := append(append([]runengine.TaskRecord{}, unfinishedTasks...), finishedTasks...)
	modelCredentials := modelCredentialSettings(s.runEngine.Settings())
	latestRestorePoint := latestRestorePointFromTasks(allTasks)
	if latestRestorePoint == nil {
		latestRestorePoint = s.latestRestorePointFromStorage("")
	}
	return map[string]any{
		"summary": map[string]any{
			"security_status":        aggregateSecurityStatus(allTasks, pendingTotal),
			"pending_authorizations": pendingTotal,
			"latest_restore_point":   latestRestorePoint,
			"token_cost_summary":     aggregateTokenCostSummary(unfinishedTasks, finishedTasks, boolValue(modelCredentials, "budget_auto_downgrade", true)),
		},
	}, nil
}
