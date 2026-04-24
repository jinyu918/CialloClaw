package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// SecurityPendingList handles `agent.security.pending.list` and keeps the
// pending-authorization list aligned with the merged task-centric read model.
func (s *Service) SecurityPendingList(params map[string]any) (map[string]any, error) {
	limit := clampListLimit(intValue(params, "limit", 20))
	offset := clampListOffset(intValue(params, "offset", 0))
	unfinishedTasks := newTaskQueryViews(s).tasks("unfinished", "updated_at", "desc")
	items := pendingApprovalsFromTasks(unfinishedTasks)
	total := len(items)

	// Keep the legacy runtime response as a safety net when runtime approval
	// requests exist but the task snapshots do not expose a structured payload.
	if total == 0 {
		if s.storage != nil {
			storedRecords, storedTotal, err := s.storage.ApprovalRequestStore().ListPendingApprovalRequests(context.Background(), limit, offset)
			if err == nil && storedTotal > 0 {
				items = approvalRequestRecordsToItems(storedRecords)
				total = storedTotal
			} else {
				runtimeItems, runtimeTotal := s.runEngine.PendingApprovalRequests(limit, offset)
				items = runtimeItems
				total = runtimeTotal
			}
		} else {
			runtimeItems, runtimeTotal := s.runEngine.PendingApprovalRequests(limit, offset)
			items = runtimeItems
			total = runtimeTotal
		}
	} else if offset >= total {
		items = []map[string]any{}
	} else {
		end := offset + limit
		if end > total {
			end = total
		}
		items = items[offset:end]
	}

	return map[string]any{
		"items": items,
		"page":  pageMap(limit, offset, total),
	}, nil
}

// SecurityAuditList handles agent.security.audit.list.
func (s *Service) SecurityAuditList(params map[string]any) (map[string]any, error) {
	limit := clampListLimit(intValue(params, "limit", 20))
	offset := clampListOffset(intValue(params, "offset", 0))
	taskID := stringValue(params, "task_id", "")
	if strings.TrimSpace(taskID) == "" {
		return nil, errors.New("task_id is required")
	}
	if s.storage == nil {
		return map[string]any{"items": []map[string]any{}, "page": pageMap(limit, offset, 0)}, nil
	}
	records, total, err := s.storage.AuditStore().ListAuditRecords(context.Background(), taskID, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrStorageQueryFailed, err)
	}
	items := make([]map[string]any, 0, len(records))
	for _, record := range records {
		items = append(items, record.Map())
	}
	return map[string]any{
		"items": items,
		"page":  pageMap(limit, offset, total),
	}, nil
}

// SecurityRestorePointsList handles agent.security.restore_points.list.
func (s *Service) SecurityRestorePointsList(params map[string]any) (map[string]any, error) {
	limit := clampListLimit(intValue(params, "limit", 20))
	offset := clampListOffset(intValue(params, "offset", 0))
	taskID := stringValue(params, "task_id", "")
	if s.storage == nil {
		return map[string]any{"items": []map[string]any{}, "page": pageMap(limit, offset, 0)}, nil
	}
	points, total, err := s.storage.RecoveryPointStore().ListRecoveryPoints(context.Background(), taskID, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrStorageQueryFailed, err)
	}
	items := make([]map[string]any, 0, len(points))
	for _, point := range points {
		items = append(items, map[string]any{
			"recovery_point_id": point.RecoveryPointID,
			"task_id":           point.TaskID,
			"summary":           point.Summary,
			"created_at":        point.CreatedAt,
			"objects":           append([]string(nil), point.Objects...),
		})
	}
	return map[string]any{
		"items": items,
		"page":  pageMap(limit, offset, total),
	}, nil
}
