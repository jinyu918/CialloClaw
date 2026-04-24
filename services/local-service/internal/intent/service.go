// Package intent derives lightweight task suggestions from normalized context.
package intent

const defaultAgentLoopIntent = "agent_loop"

// Suggestion is the minimum intent output required to create or continue a
// task in the main pipeline.
type Suggestion struct {
	Intent             map[string]any
	IntentConfirmed    bool
	TaskTitle          string
	TaskSourceType     string
	RequiresConfirm    bool
	DirectDeliveryType string
	ResultPreview      string
	ResultTitle        string
	ResultBubbleText   string
}

// Service maps context snapshots to lightweight intent suggestions.
// It stays read-only and leaves task mutation to the orchestrator/runengine.
type Service struct{}

// NewService constructs an intent suggestion service.
func NewService() *Service {
	return &Service{}
}
