package orchestrator

import (
	"errors"
	"strings"
)

// TaskArtifactList handles `agent.task.artifact.list` and returns protocol-ready
// artifact items.
func (s *Service) TaskArtifactList(params map[string]any) (map[string]any, error) {
	limit := clampListLimit(intValue(params, "limit", 20))
	offset := clampListOffset(intValue(params, "offset", 0))
	taskID := stringValue(params, "task_id", "")
	if strings.TrimSpace(taskID) == "" {
		return nil, errors.New("task_id is required")
	}
	items, total, err := s.listArtifactsPage(taskID, limit, offset)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"items": protocolArtifactList(items),
		"page":  pageMap(limit, offset, total),
	}, nil
}

// TaskArtifactOpen handles `agent.task.artifact.open` and keeps the open
// resolution metadata while exposing a formal Artifact payload.
func (s *Service) TaskArtifactOpen(params map[string]any) (map[string]any, error) {
	taskID := stringValue(params, "task_id", "")
	artifactID := stringValue(params, "artifact_id", "")
	if strings.TrimSpace(taskID) == "" {
		return nil, errors.New("task_id is required")
	}
	if strings.TrimSpace(artifactID) == "" {
		return nil, errors.New("artifact_id is required")
	}
	artifact, err := s.findArtifactForTask(taskID, artifactID)
	if err != nil {
		return nil, err
	}
	openResult := buildDeliveryOpenResult(cloneMap(artifact), nil, taskID)
	openResult["artifact"] = protocolArtifactMap(artifact)
	return openResult, nil
}

// DeliveryOpen handles `agent.delivery.open` and resolves the final open action.
func (s *Service) DeliveryOpen(params map[string]any) (map[string]any, error) {
	taskID := stringValue(params, "task_id", "")
	if strings.TrimSpace(taskID) == "" {
		return nil, errors.New("task_id is required")
	}
	artifactID := stringValue(params, "artifact_id", "")
	if strings.TrimSpace(artifactID) != "" {
		artifact, err := s.findArtifactForTask(taskID, artifactID)
		if err != nil {
			return nil, err
		}
		result := buildDeliveryOpenResult(cloneMap(artifact), nil, taskID)
		result["artifact"] = protocolArtifactMap(artifact)
		return result, nil
	}
	task, ok := s.runEngine.GetTask(taskID)
	if !ok {
		task, ok = s.taskDetailFromStorage(taskID)
	}
	if !ok {
		return nil, ErrTaskNotFound
	}
	return buildDeliveryOpenResult(nil, cloneMap(task.DeliveryResult), taskID), nil
}

func inferArtifactDeliveryType(artifact map[string]any) string {
	if deliveryType := stringValue(artifact, "delivery_type", ""); deliveryType != "" {
		return deliveryType
	}
	if path := stringValue(artifact, "path", ""); path != "" {
		return "open_file"
	}
	return "task_detail"
}

// protocolTaskStepList guarantees that task detail timeline stays an array.
func protocolTaskStepList(steps []map[string]any) []map[string]any {
	if len(steps) == 0 {
		return []map[string]any{}
	}
	return cloneMapSlice(steps)
}

// protocolArtifactList trims artifact items to the declared protocol fields and
// keeps the collection non-null for RPC consumers.
func protocolArtifactList(artifacts []map[string]any) []map[string]any {
	if len(artifacts) == 0 {
		return []map[string]any{}
	}
	result := make([]map[string]any, 0, len(artifacts))
	for _, artifact := range artifacts {
		normalized := protocolArtifactMap(artifact)
		if normalized == nil {
			continue
		}
		result = append(result, normalized)
	}
	if len(result) == 0 {
		return []map[string]any{}
	}
	return result
}

func protocolCitationList(citations []map[string]any) []map[string]any {
	if len(citations) == 0 {
		return []map[string]any{}
	}
	result := make([]map[string]any, 0, len(citations))
	for _, citation := range citations {
		result = append(result, protocolCitationMap(citation))
	}
	return result
}

func protocolCitationMap(citation map[string]any) map[string]any {
	result := map[string]any{
		"citation_id": stringValue(citation, "citation_id", ""),
		"task_id":     stringValue(citation, "task_id", ""),
		"run_id":      stringValue(citation, "run_id", ""),
		"source_type": stringValue(citation, "source_type", "context"),
		"source_ref":  stringValue(citation, "source_ref", ""),
		"label":       stringValue(citation, "label", ""),
	}
	if artifactID := strings.TrimSpace(stringValue(citation, "artifact_id", "")); artifactID != "" {
		result["artifact_id"] = artifactID
	}
	if artifactType := strings.TrimSpace(stringValue(citation, "artifact_type", "")); artifactType != "" {
		result["artifact_type"] = artifactType
	}
	if evidenceRole := strings.TrimSpace(stringValue(citation, "evidence_role", "")); evidenceRole != "" {
		result["evidence_role"] = evidenceRole
	}
	if excerptText := strings.TrimSpace(stringValue(citation, "excerpt_text", "")); excerptText != "" {
		result["excerpt_text"] = excerptText
	}
	if screenSessionID := strings.TrimSpace(stringValue(citation, "screen_session_id", "")); screenSessionID != "" {
		result["screen_session_id"] = screenSessionID
	}
	return result
}

// protocolArtifactMap trims one artifact to the formal Artifact contract.
func protocolArtifactMap(artifact map[string]any) map[string]any {
	if len(artifact) == 0 {
		return nil
	}
	return map[string]any{
		"artifact_id":   stringValue(artifact, "artifact_id", ""),
		"task_id":       stringValue(artifact, "task_id", ""),
		"artifact_type": stringValue(artifact, "artifact_type", ""),
		"title":         stringValue(artifact, "title", ""),
		"path":          stringValue(artifact, "path", ""),
		"mime_type":     stringValue(artifact, "mime_type", ""),
	}
}

// protocolMirrorReferenceList trims mirror references to the declared protocol
// fields and keeps the collection non-null for RPC consumers.
func protocolMirrorReferenceList(references []map[string]any) []map[string]any {
	if len(references) == 0 {
		return []map[string]any{}
	}
	result := make([]map[string]any, 0, len(references))
	for _, reference := range references {
		if len(reference) == 0 {
			continue
		}
		result = append(result, map[string]any{
			"memory_id": stringValue(reference, "memory_id", ""),
			"reason":    stringValue(reference, "reason", ""),
			"summary":   stringValue(reference, "summary", ""),
		})
	}
	if len(result) == 0 {
		return []map[string]any{}
	}
	return result
}

func buildDeliveryOpenResult(artifact map[string]any, deliveryResult map[string]any, taskID string) map[string]any {
	resolvedDelivery := normalizeDeliveryOpenResult(artifact, deliveryResult, taskID)
	return map[string]any{
		"delivery_result":  resolvedDelivery,
		"open_action":      stringValue(resolvedDelivery, "type", "task_detail"),
		"resolved_payload": cloneMap(mapValue(resolvedDelivery, "payload")),
	}
}

func normalizeDeliveryOpenResult(artifact map[string]any, deliveryResult map[string]any, taskID string) map[string]any {
	if len(deliveryResult) == 0 {
		payload := cloneMap(mapValue(artifact, "delivery_payload"))
		if payload == nil {
			payload = map[string]any{}
		}
		pathValue := firstNonEmptyString(stringValue(artifact, "path", ""), stringValue(payload, "path", ""))
		if pathValue != "" {
			payload["path"] = pathValue
		}
		if payload["task_id"] == nil {
			payload["task_id"] = taskID
		}
		return map[string]any{
			"type":         firstNonEmptyString(stringValue(artifact, "delivery_type", ""), inferArtifactDeliveryType(artifact)),
			"title":        stringValue(artifact, "title", ""),
			"payload":      normalizeFormalDeliveryPayload(payload, taskID),
			"preview_text": stringValue(artifact, "title", ""),
		}
	}
	resolved := cloneMap(deliveryResult)
	payload := cloneMap(mapValue(resolved, "payload"))
	if payload == nil {
		payload = map[string]any{}
	}
	resolved["payload"] = normalizeFormalDeliveryPayload(payload, taskID)
	if stringValue(resolved, "type", "") == "" {
		resolved["type"] = "task_detail"
	}
	if stringValue(resolved, "title", "") == "" {
		resolved["title"] = "任务交付结果"
	}
	if stringValue(resolved, "preview_text", "") == "" {
		resolved["preview_text"] = stringValue(resolved, "title", "")
	}
	return resolved
}

// normalizeFormalDeliveryPayload keeps formal delivery payload keys stable for
// protocol consumers even when historical storage records omitted sparse fields.
func normalizeFormalDeliveryPayload(payload map[string]any, taskID string) map[string]any {
	normalized := cloneMap(payload)
	if normalized == nil {
		normalized = map[string]any{}
	}
	if normalized["path"] == nil {
		normalized["path"] = nil
	}
	if normalized["url"] == nil {
		normalized["url"] = nil
	}
	if normalized["task_id"] == nil {
		if strings.TrimSpace(taskID) == "" {
			normalized["task_id"] = nil
		} else {
			normalized["task_id"] = taskID
		}
	}
	return normalized
}

// normalizeTaskDetailDeliveryResult keeps task detail aligned with the formal
// delivery contract without forcing the dashboard to infer missing payload fields.
func normalizeTaskDetailDeliveryResult(taskID string, deliveryResult map[string]any) map[string]any {
	if len(deliveryResult) == 0 {
		return nil
	}
	return normalizeDeliveryOpenResult(nil, cloneMap(deliveryResult), taskID)
}
