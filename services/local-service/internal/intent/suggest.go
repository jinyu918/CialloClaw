package intent

import (
	"strings"
	"unicode/utf8"

	contextsvc "github.com/cialloclaw/cialloclaw/services/local-service/internal/context"
)

// Suggest derives a task suggestion from normalized context and an optional
// explicit intent payload.
func (s *Service) Suggest(snapshot contextsvc.TaskContextSnapshot, explicitIntent map[string]any, confirmRequired bool) Suggestion {
	intent := explicitIntent
	if len(intent) == 0 {
		intent = s.defaultIntent(snapshot)
	}

	intentName := stringValue(intent, "name")
	intentConfirmed := intentName != ""
	sourceType := sourceTypeFromSnapshot(snapshot)
	requiresConfirm := confirmRequired
	if !intentConfirmed {
		requiresConfirm = true
	}
	if intentName == "screen_analyze" {
		requiresConfirm = false
	}
	if !requiresConfirm && len(explicitIntent) == 0 {
		requiresConfirm = requiresConfirmation(snapshot, intentName)
	}

	directDeliveryType := directDeliveryTypeForSnapshot(snapshot, intentName)
	resultPreview := previewForDeliveryType(directDeliveryType)

	return Suggestion{
		Intent:             intent,
		IntentConfirmed:    intentConfirmed,
		TaskTitle:          s.buildTaskTitle(snapshot, intentName),
		TaskSourceType:     sourceType,
		RequiresConfirm:    requiresConfirm,
		DirectDeliveryType: directDeliveryType,
		ResultPreview:      resultPreview,
		ResultTitle:        s.buildResultTitle(intentName),
		ResultBubbleText:   s.buildResultBubbleText(intentName),
	}
}

// defaultIntent chooses the minimum default route when the client does not provide
// an explicit intent payload. The current correction path no longer classifies
// free-form requests into summarize / translate / explain via keyword matching.
// Instead, non-trivial inputs fall back to the generic agent loop path.
func (s *Service) defaultIntent(snapshot contextsvc.TaskContextSnapshot) map[string]any {
	if screenIntent, ok := screenAnalyzeIntent(snapshot); ok {
		return screenIntent
	}
	if shouldConfirmTextGoal(snapshot) {
		return map[string]any{}
	}

	return intentPayload(defaultAgentLoopIntent)
}

// sourceTypeFromSnapshot maps trigger-level input semantics into the stable
// task_source_type enum recorded by runengine.
func sourceTypeFromSnapshot(snapshot contextsvc.TaskContextSnapshot) string {
	switch snapshot.Trigger {
	case "voice_commit":
		return "voice"
	case "hover_text_input":
		return "hover_input"
	case "text_selected_click":
		return "selected_text"
	case "file_drop":
		return "dragged_file"
	case "error_detected":
		return "error_signal"
	case "recommendation_click":
		return "hover_input"
	default:
		if len(snapshot.Files) > 0 || snapshot.InputType == "file" {
			return "dragged_file"
		}
		if snapshot.ErrorText != "" || snapshot.InputType == "error" {
			return "error_signal"
		}
		if snapshot.SelectionText != "" || snapshot.InputType == "text_selection" {
			return "selected_text"
		}
		return "hover_input"
	}
}

func requiresConfirmation(snapshot contextsvc.TaskContextSnapshot, intentName string) bool {
	switch {
	case intentName == "":
		return true
	case intentName == defaultAgentLoopIntent:
		return false
	case snapshot.InputType == "file":
		return true
	case snapshot.InputType == "text_selection":
		return intentName != "translate"
	case isLongContent(snapshot.Text):
		return intentName == "summarize" || intentName == "rewrite"
	default:
		return false
	}
}

func shouldConfirmTextGoal(snapshot contextsvc.TaskContextSnapshot) bool {
	if snapshot.InputType != "text" {
		return false
	}
	trimmed := strings.TrimSpace(snapshot.Text)
	if trimmed == "" {
		return false
	}
	if isLongContent(trimmed) || isQuestionText(trimmed) {
		return false
	}
	return utf8.RuneCountInString(trimmed) <= 4
}
