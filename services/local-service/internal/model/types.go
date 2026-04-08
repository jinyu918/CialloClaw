// 该文件负责模型接入层的结构或实现。
package model

import "context"

// GenerateTextRequest 描述当前模块请求结构。
type GenerateTextRequest struct {
	TaskID string
	RunID  string
	Input  string
}

// TokenUsage 定义当前模块的数据结构。
type TokenUsage struct {
	InputTokens  int
	OutputTokens int
	TotalTokens  int
}

// InvocationRecord 描述当前模块记录。
type InvocationRecord struct {
	TaskID    string
	RunID     string
	RequestID string
	Provider  string
	ModelID   string
	Usage     TokenUsage
	LatencyMS int64
}

// GenerateTextResponse 定义当前模块的数据结构。
type GenerateTextResponse struct {
	TaskID     string
	RunID      string
	RequestID  string
	Provider   string
	ModelID    string
	OutputText string
	Usage      TokenUsage
	LatencyMS  int64
}

// InvocationRecord 处理当前模块的相关逻辑。
func (r GenerateTextResponse) InvocationRecord() InvocationRecord {
	return InvocationRecord{
		TaskID:    r.TaskID,
		RunID:     r.RunID,
		RequestID: r.RequestID,
		Provider:  r.Provider,
		ModelID:   r.ModelID,
		Usage:     r.Usage,
		LatencyMS: r.LatencyMS,
	}
}

// Client 定义当前模块的接口约束。
type Client interface {
	GenerateText(ctx context.Context, request GenerateTextRequest) (GenerateTextResponse, error)
}
