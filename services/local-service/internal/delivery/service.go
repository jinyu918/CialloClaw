// 该文件负责交付结果、气泡消息与 artifact 计划的组装。
package delivery

import (
	"fmt"
	"regexp"
	"strings"
)

// StorageWritePlan 定义当前模块的数据结构。
type StorageWritePlan struct {
	TaskID       string
	TargetPath   string
	MimeType     string
	DeliveryType string
	Source       string
}

// ArtifactPersistPlan 定义当前模块的数据结构。
type ArtifactPersistPlan struct {
	ArtifactID   string
	TaskID       string
	ArtifactType string
	Path         string
	MimeType     string
}

// Service 提供当前模块的服务能力。
type Service struct{}

// NewService 创建并返回Service。
func NewService() *Service {
	return &Service{}
}

// DefaultResultType 处理当前模块的相关逻辑。
func (s *Service) DefaultResultType() string {
	return "workspace_document"
}

// BuildBubbleMessage 构建BubbleMessage。
func (s *Service) BuildBubbleMessage(taskID, bubbleType, text, createdAt string) map[string]any {
	return map[string]any{
		"bubble_id":  fmt.Sprintf("bubble_%s", taskID),
		"task_id":    taskID,
		"type":       bubbleType,
		"text":       text,
		"pinned":     false,
		"hidden":     false,
		"created_at": createdAt,
	}
}

// BuildDeliveryResult 构建DeliveryResult。
func (s *Service) BuildDeliveryResult(taskID, deliveryType, title, previewText string) map[string]any {
	payload := map[string]any{
		"path":    nil,
		"url":     nil,
		"task_id": taskID,
	}

	if deliveryType == "workspace_document" {
		payload["path"] = fmt.Sprintf("D:/CialloClawWorkspace/%s.md", slugify(title, taskID))
	}

	return map[string]any{
		"type":         deliveryType,
		"title":        title,
		"payload":      payload,
		"preview_text": previewText,
	}
}

// BuildArtifact 构建Artifact。
func (s *Service) BuildArtifact(taskID, title string, deliveryResult map[string]any) []map[string]any {
	payload, ok := deliveryResult["payload"].(map[string]any)
	if !ok {
		return nil
	}

	path, _ := payload["path"].(string)
	if path == "" {
		return nil
	}

	return []map[string]any{
		{
			"artifact_id":   fmt.Sprintf("art_%s", taskID),
			"task_id":       taskID,
			"artifact_type": "generated_doc",
			"title":         fmt.Sprintf("%s.md", title),
			"path":          path,
			"mime_type":     "text/markdown",
		},
	}
}

// BuildStorageWritePlan 构建StorageWritePlan。
func (s *Service) BuildStorageWritePlan(taskID string, deliveryResult map[string]any) map[string]any {
	payload, ok := deliveryResult["payload"].(map[string]any)
	if !ok {
		return nil
	}

	path, _ := payload["path"].(string)
	if path == "" {
		return nil
	}

	return map[string]any{
		"task_id":       taskID,
		"target_path":   path,
		"mime_type":     "text/markdown",
		"delivery_type": deliveryResult["type"],
		"source":        "delivery_result",
	}
}

// BuildArtifactPersistPlans 构建ArtifactPersistPlans。
func (s *Service) BuildArtifactPersistPlans(taskID string, artifacts []map[string]any) []map[string]any {
	if len(artifacts) == 0 {
		return nil
	}

	result := make([]map[string]any, 0, len(artifacts))
	for _, artifact := range artifacts {
		result = append(result, map[string]any{
			"artifact_id":   artifact["artifact_id"],
			"task_id":       taskID,
			"artifact_type": artifact["artifact_type"],
			"path":          artifact["path"],
			"mime_type":     artifact["mime_type"],
		})
	}

	return result
}

// slugify 处理当前模块的相关逻辑。
func slugify(title, fallback string) string {
	trimmed := strings.TrimSpace(title)
	if trimmed == "" {
		return fallback
	}

	trimmed = strings.ReplaceAll(trimmed, " ", "-")
	cleaner := regexp.MustCompile(`[^\p{Han}A-Za-z0-9_-]+`)
	trimmed = cleaner.ReplaceAllString(trimmed, "")
	trimmed = strings.Trim(trimmed, "-")
	if trimmed == "" {
		return fallback
	}

	return trimmed
}
