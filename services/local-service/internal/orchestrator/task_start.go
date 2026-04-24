package orchestrator

import (
	"strings"

	"github.com/cialloclaw/cialloclaw/services/local-service/internal/runengine"
)

// StartTask handles agent.task.start and creates the task/run mapping from an
// explicit or inferred intent.
func (s *Service) StartTask(params map[string]any) (map[string]any, error) {
	snapshot := s.context.Capture(params)
	explicitIntent := mapValue(params, "intent")
	if response, handled, resolvedSessionID, err := s.maybeContinueExistingTask(params, snapshot, explicitIntent); err != nil {
		return nil, err
	} else if handled {
		return response, nil
	} else if strings.TrimSpace(resolvedSessionID) != "" {
		params = withResolvedSessionID(params, resolvedSessionID)
	}
	if handledResponse, handled, err := s.handleScreenAnalyzeStart(params, snapshot, explicitIntent); err != nil {
		return nil, err
	} else if handled {
		return handledResponse, nil
	}
	suggestion := s.intent.Suggest(snapshot, explicitIntent, len(explicitIntent) == 0)
	suggestion = s.normalizeSuggestedIntentForAvailability(snapshot, suggestion, false)
	if handledResponse, handled, err := s.handleScreenAnalyzeSuggestion(params, snapshot, suggestion); err != nil {
		return nil, err
	} else if handled {
		return handledResponse, nil
	}
	preferredDelivery, fallbackDelivery := deliveryPreferenceFromStart(params)
	if len(explicitIntent) == 0 && !suggestion.RequiresConfirm {
		preferredDelivery, fallbackDelivery = mergeSuggestedDeliveryPreference(preferredDelivery, fallbackDelivery, suggestion.DirectDeliveryType)
	}

	task := s.runEngine.CreateTask(runengine.CreateTaskInput{
		SessionID:         stringValue(params, "session_id", ""),
		RequestSource:     stringValue(params, "source", ""),
		RequestTrigger:    stringValue(params, "trigger", ""),
		Title:             suggestion.TaskTitle,
		SourceType:        suggestion.TaskSourceType,
		Status:            taskStatusForSuggestion(suggestion.RequiresConfirm),
		Intent:            suggestion.Intent,
		PreferredDelivery: preferredDelivery,
		FallbackDelivery:  fallbackDelivery,
		CurrentStep:       currentStepForSuggestion(suggestion.RequiresConfirm, suggestion.Intent),
		RiskLevel:         s.risk.DefaultLevel(),
		Timeline:          initialTimeline(taskStatusForSuggestion(suggestion.RequiresConfirm), currentStepForSuggestion(suggestion.RequiresConfirm, suggestion.Intent)),
		Snapshot:          snapshot,
	})
	s.publishTaskStart(task.TaskID, task.SessionID, requestTraceID(params))
	s.attachMemoryReadPlans(task.TaskID, task.RunID, snapshot, suggestion.Intent)

	bubble := s.delivery.BuildBubbleMessage(task.TaskID, bubbleTypeForSuggestion(suggestion.RequiresConfirm), bubbleTextForStart(suggestion), task.StartedAt.Format(dateTimeLayout))
	response := map[string]any{
		"task":            taskMap(task),
		"bubble_message":  bubble,
		"delivery_result": nil,
	}

	if suggestion.RequiresConfirm {
		if _, ok := s.runEngine.SetPresentation(task.TaskID, bubble, nil, nil); ok {
			task, _ = s.runEngine.GetTask(task.TaskID)
			response["task"] = taskMap(task)
		}
		return response, nil
	}

	if queuedTask, queueBubble, queued, queueErr := s.queueTaskIfSessionBusy(task); queueErr != nil {
		return nil, queueErr
	} else if queued {
		response["task"] = taskMap(queuedTask)
		response["bubble_message"] = queueBubble
		return response, nil
	}

	governedTask, governedResponse, handled, governanceErr := s.handleTaskGovernanceDecision(task, suggestion.Intent)
	if governanceErr != nil {
		return nil, governanceErr
	}
	if handled {
		return governedResponse, nil
	}
	task = governedTask

	deliveryResult := map[string]any(nil)
	var execErr error
	task, bubble, deliveryResult, _, execErr = s.executeTask(task, snapshot, suggestion.Intent)
	if execErr != nil {
		return nil, execErr
	}
	response["task"] = taskMap(task)
	response["bubble_message"] = bubble
	response["delivery_result"] = deliveryResult
	return response, nil
}
