package orchestrator

import (
	"strings"

	"github.com/cialloclaw/cialloclaw/services/local-service/internal/intent"
)

// taskStatusForSuggestion derives the initial task_status from the suggestion
// confirmation requirement.
func taskStatusForSuggestion(requiresConfirm bool) string {
	if requiresConfirm {
		return "confirming_intent"
	}
	return "processing"
}

// currentStepForSuggestion derives the initial current_step from the suggested
// intent.
func currentStepForSuggestion(requiresConfirm bool, taskIntent map[string]any) string {
	if requiresConfirm {
		return "intent_confirmation"
	}
	if stringValue(taskIntent, "name", "") == "agent_loop" {
		return "agent_loop"
	}
	return "generate_output"
}

// bubbleTypeForSuggestion selects the outward-facing bubble type for the
// suggestion result.
func bubbleTypeForSuggestion(requiresConfirm bool) string {
	if requiresConfirm {
		return "intent_confirm"
	}
	return "result"
}

// bubbleTextForInput returns the bubble text for agent.input.submit flows.
func bubbleTextForInput(suggestion intent.Suggestion) string {
	if suggestion.RequiresConfirm {
		if !suggestion.IntentConfirmed {
			return "我还不确定你想如何处理这段内容，请确认目标。"
		}
		return confirmIntentText(suggestion.Intent)
	}
	return suggestion.ResultBubbleText
}

// bubbleTextForStart returns the bubble text for agent.task.start flows.
func bubbleTextForStart(suggestion intent.Suggestion) string {
	if suggestion.RequiresConfirm {
		if !suggestion.IntentConfirmed {
			return "我还不确定你想如何处理当前对象，请先确认。"
		}
		return confirmIntentText(suggestion.Intent)
	}
	return suggestion.ResultBubbleText
}

func confirmIntentText(taskIntent map[string]any) string {
	switch stringValue(taskIntent, "name", "") {
	case "translate":
		return "你是想翻译这段内容吗？"
	case "rewrite":
		return "你是想改写这段内容吗？"
	case "explain":
		return "你是想解释这段内容吗？"
	case "summarize":
		return "你是想总结这段内容吗？"
	case "write_file":
		return "你是想把结果整理成文档吗？"
	default:
		return "请确认你希望我如何处理当前内容。"
	}
}

// deliveryPreferenceFromSubmit reads delivery preferences from
// agent.input.submit. Submit uses options.* while agent.task.start uses a
// dedicated delivery object, so the orchestrator keeps both decoders separate
// and normalizes them before any execution or approval plan is built.
func deliveryPreferenceFromSubmit(params map[string]any) (string, string) {
	options := mapValue(params, "options")
	return stringValue(options, "preferred_delivery", ""), ""
}

func deliveryPreferenceFromStart(params map[string]any) (string, string) {
	deliveryOptions := mapValue(params, "delivery")
	return stringValue(deliveryOptions, "preferred", ""), stringValue(deliveryOptions, "fallback", "")
}

// mergeSuggestedDeliveryPreference preserves explicit caller preferences and only
// falls back to the intent layer's suggested delivery when the caller left the
// preferred delivery unset.
func mergeSuggestedDeliveryPreference(preferredDelivery, fallbackDelivery, suggestedDelivery string) (string, string) {
	if strings.TrimSpace(preferredDelivery) == "" && strings.TrimSpace(suggestedDelivery) != "" {
		preferredDelivery = suggestedDelivery
	}
	return preferredDelivery, fallbackDelivery
}
