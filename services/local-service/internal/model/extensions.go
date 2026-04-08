// 该文件负责模型接入层的结构或实现。
package model

import "context"

// GenerateTextStreamRequest 描述当前模块请求结构。
type GenerateTextStreamRequest struct {
	TaskID string
	RunID  string
	Input  string
}

type StreamEventType string

const (
	StreamEventDelta StreamEventType = "delta"
	StreamEventDone  StreamEventType = "done"
	StreamEventError StreamEventType = "error"
)

// GenerateTextStreamEvent 定义当前模块的数据结构。
type GenerateTextStreamEvent struct {
	Type      StreamEventType
	DeltaText string
	Error     string
}

// StreamClient 定义当前模块的接口约束。
type StreamClient interface {
	GenerateTextStream(ctx context.Context, request GenerateTextStreamRequest) (<-chan GenerateTextStreamEvent, error)
}

// ToolDefinition 定义当前模块的数据结构。
type ToolDefinition struct {
	Name        string
	Description string
	InputSchema map[string]any
}

// ToolCallRequest 描述当前模块请求结构。
type ToolCallRequest struct {
	TaskID string
	RunID  string
	Input  string
	Tools  []ToolDefinition
}

// ToolCallResult 描述当前模块结果结构。
type ToolCallResult struct {
	RequestID  string
	Provider   string
	ModelID    string
	OutputText string
	ToolCalls  []ToolInvocation
	Usage      TokenUsage
	LatencyMS  int64
}

// ToolInvocation 定义当前模块的数据结构。
type ToolInvocation struct {
	Name      string
	Arguments map[string]any
}

// ToolCallingClient 定义当前模块的接口约束。
type ToolCallingClient interface {
	GenerateToolCalls(ctx context.Context, request ToolCallRequest) (ToolCallResult, error)
}
