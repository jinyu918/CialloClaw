package orchestrator

import (
	"strings"

	"github.com/cialloclaw/cialloclaw/services/local-service/internal/runengine"
)

// ConfirmTask handles agent.task.confirm.
// It only accepts tasks that are still waiting in the intent confirmation phase,
// then either keeps clarification open, applies a corrected intent, or confirms
// the stored intent before continuing through governance and delivery.
func (s *Service) ConfirmTask(params map[string]any) (map[string]any, error) {
	taskID := stringValue(params, "task_id", "")
	task, ok := s.runEngine.GetTask(taskID)
	if !ok {
		return nil, ErrTaskNotFound
	}
	if task.Status != "confirming_intent" {
		return nil, ErrTaskStatusInvalid
	}
	confirmed := boolValue(params, "confirmed", false)
	correctedIntent := mapValue(params, "corrected_intent")
	intentValue := cloneMap(task.Intent)
	if !confirmed && len(correctedIntent) > 0 {
		intentValue = correctedIntent
	}
	if !confirmed && len(correctedIntent) == 0 {
		updatedTask, err := s.revertTaskToIntentConfirmation(task)
		if err != nil {
			return nil, err
		}
		bubble := s.delivery.BuildBubbleMessage(task.TaskID, "status", "这不是我该做的处理方式。请重新说明你的目标，或给我一个更准确的处理意图。", updatedTask.UpdatedAt.Format(dateTimeLayout))
		if presentedTask, ok := s.runEngine.SetPresentation(task.TaskID, bubble, nil, nil); ok {
			updatedTask = presentedTask
		} else {
			return nil, ErrTaskNotFound
		}
		return map[string]any{
			"task":            taskMap(updatedTask),
			"bubble_message":  bubble,
			"delivery_result": nil,
		}, nil
	}
	if strings.TrimSpace(stringValue(intentValue, "name", "")) == "" {
		bubble := s.delivery.BuildBubbleMessage(task.TaskID, "status", "请先明确告诉我你希望执行的处理方式。", task.UpdatedAt.Format(dateTimeLayout))
		if updatedTask, ok := s.runEngine.SetPresentation(task.TaskID, bubble, nil, nil); ok {
			return map[string]any{
				"task":            taskMap(updatedTask),
				"bubble_message":  bubble,
				"delivery_result": nil,
			}, nil
		}
		return nil, ErrTaskNotFound
	}
	snapshot := snapshotFromTask(task)
	suggestion := s.intent.Suggest(snapshot, intentValue, false)
	suggestion = s.normalizeSuggestedIntentForAvailability(snapshot, suggestion, false)
	resolvedIntent := suggestion.Intent
	updatedTitle := suggestion.TaskTitle

	bubble := s.delivery.BuildBubbleMessage(task.TaskID, "status", "已按新的要求开始处理", task.UpdatedAt.Format(dateTimeLayout))
	updatedTask, ok := s.runEngine.UpdateIntent(task.TaskID, updatedTitle, resolvedIntent)
	if !ok {
		return nil, ErrTaskNotFound
	}
	s.attachMemoryReadPlans(updatedTask.TaskID, updatedTask.RunID, snapshot, resolvedIntent)
	if queuedTask, queueBubble, queued, queueErr := s.queueTaskIfSessionBusy(updatedTask); queueErr != nil {
		return nil, queueErr
	} else if queued {
		return map[string]any{
			"task":            taskMap(queuedTask),
			"bubble_message":  queueBubble,
			"delivery_result": nil,
		}, nil
	}
	if stringValue(resolvedIntent, "name", "") == "screen_analyze" {
		waitingTask, waitingBubble, err := s.markTaskWaitingScreenApproval(updatedTask)
		if err != nil {
			return nil, err
		}
		return taskScreenAnalyzeResponse(waitingTask, waitingBubble), nil
	}
	governedTask, governedResponse, handled, governanceErr := s.handleTaskGovernanceDecision(updatedTask, resolvedIntent)
	if governanceErr != nil {
		return nil, governanceErr
	}
	if handled {
		return governedResponse, nil
	}
	updatedTask = governedTask

	updatedTask, ok = s.runEngine.ConfirmTask(task.TaskID, updatedTitle, resolvedIntent, bubble)
	if !ok {
		return nil, ErrTaskNotFound
	}
	s.attachMemoryReadPlans(updatedTask.TaskID, updatedTask.RunID, snapshot, resolvedIntent)

	updatedTask, resultBubble, deliveryResult, _, err := s.executeTask(updatedTask, snapshot, resolvedIntent)
	if err != nil {
		return nil, err
	}

	return map[string]any{
		"task":            taskMap(updatedTask),
		"bubble_message":  resultBubble,
		"delivery_result": deliveryResult,
	}, nil
}

func (s *Service) revertTaskToIntentConfirmation(task runengine.TaskRecord) (runengine.TaskRecord, error) {
	updatedTask, ok := s.runEngine.UpdateIntent(task.TaskID, confirmationTitleFromTask(task), nil)
	if !ok {
		return runengine.TaskRecord{}, ErrTaskNotFound
	}
	return updatedTask, nil
}
