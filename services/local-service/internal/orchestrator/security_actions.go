package orchestrator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/cialloclaw/cialloclaw/services/local-service/internal/checkpoint"
	"github.com/cialloclaw/cialloclaw/services/local-service/internal/runengine"
	"github.com/cialloclaw/cialloclaw/services/local-service/internal/storage"
)

func (s *Service) persistApprovalRequestState(taskID string, approvalRequest map[string]any, impactScope map[string]any) error {
	if s.storage == nil {
		return nil
	}
	if err := s.persistApprovalRequest(taskID, approvalRequest, impactScope); err != nil {
		return fmt.Errorf("%w: %v", ErrStorageQueryFailed, err)
	}
	return nil
}

func (s *Service) persistAuthorizationState(taskID string, authorizationRecord map[string]any) error {
	if s.storage == nil {
		return nil
	}
	if err := s.persistAuthorizationDecision(taskID, authorizationRecord); err != nil {
		return fmt.Errorf("%w: %v", ErrStorageQueryFailed, err)
	}
	return nil
}

func (s *Service) persistApprovalRequest(taskID string, approvalRequest map[string]any, impactScope map[string]any) error {
	if s == nil || s.storage == nil || len(approvalRequest) == 0 {
		return nil
	}
	impactScopeJSON := ""
	if len(impactScope) > 0 {
		if encoded, err := json.Marshal(impactScope); err == nil {
			impactScopeJSON = string(encoded)
		}
	}
	record := storage.ApprovalRequestRecord{
		ApprovalID:      stringValue(approvalRequest, "approval_id", ""),
		TaskID:          firstNonEmptyString(stringValue(approvalRequest, "task_id", ""), taskID),
		OperationName:   stringValue(approvalRequest, "operation_name", ""),
		RiskLevel:       stringValue(approvalRequest, "risk_level", ""),
		TargetObject:    stringValue(approvalRequest, "target_object", ""),
		Reason:          stringValue(approvalRequest, "reason", ""),
		Status:          stringValue(approvalRequest, "status", "pending"),
		ImpactScopeJSON: impactScopeJSON,
		CreatedAt:       stringValue(approvalRequest, "created_at", time.Now().Format(dateTimeLayout)),
		UpdatedAt:       firstNonEmptyString(stringValue(approvalRequest, "updated_at", ""), stringValue(approvalRequest, "created_at", time.Now().Format(dateTimeLayout))),
	}
	return s.storage.ApprovalRequestStore().WriteApprovalRequest(context.Background(), record)
}

func (s *Service) persistAuthorizationDecision(taskID string, authorizationRecord map[string]any) error {
	if s == nil || s.storage == nil || len(authorizationRecord) == 0 {
		return nil
	}
	approvalID := stringValue(authorizationRecord, "approval_id", "")
	recordID := stringValue(authorizationRecord, "authorization_record_id", "")
	if approvalID != "" {
		recordID = fmt.Sprintf("auth_%s_%d", approvalID, time.Now().UnixNano())
	}
	createdAt := stringValue(authorizationRecord, "created_at", time.Now().Format(dateTimeLayout))
	record := storage.AuthorizationRecordRecord{
		AuthorizationRecordID: recordID,
		TaskID:                firstNonEmptyString(stringValue(authorizationRecord, "task_id", ""), taskID),
		ApprovalID:            approvalID,
		Decision:              stringValue(authorizationRecord, "decision", ""),
		Operator:              stringValue(authorizationRecord, "operator", "user"),
		RememberRule:          boolValue(authorizationRecord, "remember_rule", false),
		CreatedAt:             createdAt,
	}
	decision := record.Decision
	status := "resolved"
	if decision == "deny_once" || decision == "deny_always" {
		status = "denied"
	} else if decision == "allow_once" || decision == "allow_always" {
		status = "approved"
	}
	return s.storage.AuthorizationRecordStore().WriteAuthorizationDecision(context.Background(), record, status, createdAt)
}

func (s *Service) activeApprovalIDForTask(task runengine.TaskRecord) (string, bool) {
	if task.Status != "waiting_auth" || task.CurrentStep != "waiting_authorization" {
		return "", false
	}
	approvalID := strings.TrimSpace(stringValue(task.ApprovalRequest, "approval_id", ""))
	if approvalID == "" {
		return "", false
	}
	return approvalID, true
}

// SecurityRestoreApply handles agent.security.restore.apply.
func (s *Service) SecurityRestoreApply(params map[string]any) (map[string]any, error) {
	recoveryPointID := stringValue(params, "recovery_point_id", "")
	if strings.TrimSpace(recoveryPointID) == "" {
		return nil, errors.New("recovery_point_id is required")
	}
	taskID := stringValue(params, "task_id", "")
	point, err := s.findRecoveryPointFromStorage(taskID, recoveryPointID)
	if err != nil {
		return nil, err
	}
	resolvedTaskID := firstNonEmptyString(strings.TrimSpace(taskID), point.TaskID)
	task, ok := s.runEngine.GetTask(resolvedTaskID)
	if !ok {
		persistedTask, found := s.taskDetailFromStorage(resolvedTaskID)
		if !found {
			return nil, ErrTaskNotFound
		}
		task = s.runEngine.HydrateTaskFromStorage(persistedTask)
	}

	recoveryPoint := recoveryPointMap(point)
	assessment := restoreApplyAssessment(point)
	pendingExecution := buildRestoreApplyPendingExecution(point, assessment)
	approvalRequest := buildApprovalRequest(task.TaskID, task.Intent, assessment)
	bubble := s.delivery.BuildBubbleMessage(task.TaskID, "status", "恢复点回滚属于高风险操作，请先确认授权。", time.Now().Format(dateTimeLayout))
	updatedTask, ok := s.runEngine.MarkWaitingApprovalWithPlan(task.TaskID, approvalRequest, pendingExecution, bubble)
	if !ok {
		return nil, ErrTaskNotFound
	}
	if err := s.persistApprovalRequestState(updatedTask.TaskID, approvalRequest, assessment.ImpactScope); err != nil {
		return nil, err
	}
	return map[string]any{
		"applied":        false,
		"task":           taskMap(updatedTask),
		"recovery_point": recoveryPoint,
		"audit_record":   nil,
		"bubble_message": bubble,
	}, nil
}

func (s *Service) applyRestoreAfterApproval(task runengine.TaskRecord, point checkpoint.RecoveryPoint) (runengine.TaskRecord, map[string]any, map[string]any, error) {
	recoveryPoint := recoveryPointMap(point)
	applied := false
	securityStatus := "recovered"
	finalStatus := "completed"
	bubbleText := fmt.Sprintf("已根据恢复点 %s 恢复 %d 个对象。", point.RecoveryPointID, len(point.Objects))
	if s.executor == nil {
		securityStatus = "execution_error"
		finalStatus = "failed"
		bubbleText = "恢复失败：执行后端不可用。"
	} else if applyResult, err := s.executor.ApplyRecoveryPoint(context.Background(), point); err != nil {
		securityStatus = "execution_error"
		finalStatus = "failed"
		bubbleText = "恢复失败：恢复点内容不可用或恢复执行失败。"
	} else {
		applied = true
		if len(applyResult.RestoredObjects) > 0 {
			bubbleText = fmt.Sprintf("已根据恢复点 %s 恢复 %d 个对象。", point.RecoveryPointID, len(applyResult.RestoredObjects))
		}
	}

	bubble := s.delivery.BuildBubbleMessage(task.TaskID, "status", bubbleText, time.Now().Format(dateTimeLayout))
	updatedTask, ok := s.runEngine.ApplyRecoveryOutcome(task.TaskID, finalStatus, securityStatus, recoveryPoint, bubble)
	if !ok {
		return runengine.TaskRecord{}, nil, nil, ErrTaskNotFound
	}
	auditRecord := s.writeRestoreAuditRecord(updatedTask.TaskID, point, applied, bubbleText)
	updatedTask = s.appendAuditData(updatedTask, compactAuditRecords(auditRecord), nil)
	return updatedTask, bubble, map[string]any{
		"applied":        applied,
		"task":           taskMap(updatedTask),
		"recovery_point": recoveryPoint,
		"audit_record":   auditRecord,
		"bubble_message": bubble,
	}, nil
}

// SecurityRespond handles agent.security.respond. It is the single resume
// entrypoint for risk-gated tasks, so it must translate allow/deny decisions
// into runtime state changes, delivery continuation, impact scope reporting,
// and audit data in one place instead of letting transports or callers stitch
// those pieces together inconsistently.
func (s *Service) SecurityRespond(params map[string]any) (map[string]any, error) {
	taskID := stringValue(params, "task_id", "")
	task, ok := s.runEngine.GetTask(taskID)
	if !ok {
		return nil, ErrTaskNotFound
	}
	approvalID, ok := s.activeApprovalIDForTask(task)
	if !ok {
		return nil, ErrTaskStatusInvalid
	}

	decision := stringValue(params, "decision", "allow_once")
	rememberRule := boolValue(params, "remember_rule", false)
	authorizationRecord := map[string]any{
		"authorization_record_id": fmt.Sprintf("auth_%s_%d", task.TaskID, time.Now().UnixNano()),
		"task_id":                 task.TaskID,
		"approval_id":             approvalID,
		"decision":                decision,
		"remember_rule":           rememberRule,
		"operator":                "user",
		"created_at":              time.Now().Format(dateTimeLayout),
	}
	if err := s.persistAuthorizationState(task.TaskID, authorizationRecord); err != nil {
		return nil, err
	}
	pendingExecution, ok := s.runEngine.PendingExecutionPlan(task.TaskID)
	if !ok {
		pendingExecution = s.buildPendingExecution(task, task.Intent)
	}
	pendingExecution = s.applyResolvedDeliveryToPlan(task, pendingExecution, task.Intent)
	impactScope := s.buildImpactScope(task, pendingExecution)
	operationName := stringValue(pendingExecution, "operation_name", "")
	if decision == "deny_once" {
		bubble := s.delivery.BuildBubbleMessage(task.TaskID, "status", "已拒绝本次操作，任务已取消。", task.UpdatedAt.Format(dateTimeLayout))
		updatedTask, ok := s.runEngine.DenyAfterApproval(task.TaskID, authorizationRecord, impactScope, bubble)
		if !ok {
			return nil, ErrTaskNotFound
		}
		updatedTask = s.appendAuditData(updatedTask, compactAuditRecords(s.audit.BuildAuthorizationAudit(updatedTask.TaskID, updatedTask.RunID, decision, impactScope)), nil)
		if queueErr := s.drainSessionQueue(updatedTask.SessionID); queueErr != nil {
			return nil, queueErr
		}
		return map[string]any{
			"authorization_record": authorizationRecord,
			"task":                 taskMap(updatedTask),
			"bubble_message":       bubble,
			"impact_scope":         impactScope,
		}, nil
	}

	resumeBubble := s.delivery.BuildBubbleMessage(task.TaskID, "status", "已允许本次操作，任务继续执行。", task.UpdatedAt.Format(dateTimeLayout))
	processingTask, ok := s.runEngine.ResumeAfterApproval(task.TaskID, authorizationRecord, impactScope, resumeBubble)
	if !ok {
		return nil, ErrTaskNotFound
	}
	processingTask = s.appendAuditData(processingTask, compactAuditRecords(s.audit.BuildAuthorizationAudit(processingTask.TaskID, processingTask.RunID, decision, impactScope)), nil)
	if operationName == "restore_apply" {
		recoveryPointID := stringValue(pendingExecution, "recovery_point_id", "")
		point, err := s.findRecoveryPointFromStorage(task.TaskID, recoveryPointID)
		if err != nil {
			return nil, err
		}
		updatedTask, _, response, err := s.applyRestoreAfterApproval(processingTask, point)
		if err != nil {
			return nil, err
		}
		return map[string]any{
			"authorization_record": authorizationRecord,
			"task":                 taskMap(updatedTask),
			"bubble_message":       response["bubble_message"],
			"impact_scope":         impactScope,
			"delivery_result":      nil,
			"recovery_point":       response["recovery_point"],
			"audit_record":         response["audit_record"],
			"applied":              response["applied"],
		}, nil
	}
	if stringValue(pendingExecution, "kind", "") == "screen_analysis" {
		updatedTask, bubble, deliveryResult, err := s.executeScreenAnalysisAfterApproval(processingTask, pendingExecution)
		if err != nil {
			return nil, err
		}
		if updatedTask.Status == "completed" {
			updatedTask, _ = s.runEngine.ResolveAuthorization(task.TaskID, authorizationRecord, impactScope)
		}
		if taskIsTerminal(updatedTask.Status) {
			if queueErr := s.drainSessionQueue(updatedTask.SessionID); queueErr != nil {
				return nil, queueErr
			}
		}
		return map[string]any{
			"authorization_record": authorizationRecord,
			"task":                 taskMap(updatedTask),
			"bubble_message":       bubble,
			"impact_scope":         impactScope,
			"delivery_result":      deliveryResult,
		}, nil
	}

	resultTitle := stringValue(pendingExecution, "result_title", "处理结果")
	resultPreview := stringValue(pendingExecution, "preview_text", "已为你写入文档并打开")
	resultBubbleText := stringValue(pendingExecution, "result_bubble_text", "结果已经生成，可直接查看。")
	deliveryType := stringValue(pendingExecution, "delivery_type", deliveryTypeFromIntent(task.Intent))
	deliveryType = resolveTaskDeliveryType(task, task.Intent)
	resultPreview = previewTextForDeliveryType(deliveryType)
	_, _, _, _ = resultTitle, resultPreview, resultBubbleText, deliveryType
	updatedTask, resultBubble, deliveryResult, _, err := s.executeTask(processingTask, snapshotFromTask(processingTask), processingTask.Intent)
	if err != nil {
		return nil, err
	}
	if updatedTask.Status == "completed" {
		updatedTask, _ = s.runEngine.ResolveAuthorization(task.TaskID, authorizationRecord, impactScope)
	}
	if updatedTask.Status == "failed" {
		deliveryResult = nil
	}
	if taskIsTerminal(updatedTask.Status) {
		if queueErr := s.drainSessionQueue(updatedTask.SessionID); queueErr != nil {
			return nil, queueErr
		}
	}

	response := map[string]any{
		"authorization_record": authorizationRecord,
		"task":                 taskMap(updatedTask),
		"bubble_message":       resultBubble,
		"impact_scope":         impactScope,
	}
	if len(deliveryResult) > 0 {
		response["delivery_result"] = deliveryResult
	} else {
		response["delivery_result"] = nil
	}
	return response, nil
}
