package orchestrator

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/cialloclaw/cialloclaw/services/local-service/internal/runengine"
)

// TaskSteer handles agent.task.steer by persisting one follow-up instruction for
// a still-active task so later execution or resume paths can consume it.
func (s *Service) TaskSteer(params map[string]any) (map[string]any, error) {
	taskID := stringValue(params, "task_id", "")
	message := stringValue(params, "message", "")
	if strings.TrimSpace(taskID) == "" {
		return nil, errors.New("task_id is required")
	}
	if strings.TrimSpace(message) == "" {
		return nil, errors.New("message is required")
	}
	task, ok := s.runEngine.GetTask(taskID)
	if !ok {
		return nil, ErrTaskNotFound
	}
	bubble := s.delivery.BuildBubbleMessage(task.TaskID, "status", "已记录新的补充要求，后续执行会纳入该指令。", time.Now().Format(dateTimeLayout))
	updatedTask, changed := s.runEngine.AppendSteeringMessage(task.TaskID, message, bubble)
	if !changed {
		return nil, ErrTaskStatusInvalid
	}
	return map[string]any{
		"task":           taskMap(updatedTask),
		"bubble_message": bubble,
	}, nil
}

// TaskControl handles agent.task.control and converts user actions into runtime
// state-machine transitions. The orchestration layer owns error translation and
// post-transition follow-up such as human-loop resume handling and queue drain,
// because those behaviors depend on task-centric semantics rather than the raw
// runtime mutation alone.
func (s *Service) TaskControl(params map[string]any) (map[string]any, error) {
	taskID := stringValue(params, "task_id", "")
	if strings.TrimSpace(taskID) == "" {
		return nil, errors.New("task_id is required")
	}
	action := stringValue(params, "action", "")
	if strings.TrimSpace(action) == "" {
		return nil, errors.New("action is required")
	}
	if !isSupportedTaskControlAction(action) {
		return nil, fmt.Errorf("unsupported task control action: %s", action)
	}
	wasHumanLoop := false
	var reviewDecision map[string]any
	arguments := mapValue(params, "arguments")
	if action == "resume" {
		if existingTask, ok := s.runEngine.GetTask(taskID); ok {
			wasHumanLoop = taskIsBlockedHumanLoop(existingTask)
		}
		if wasHumanLoop {
			decision, decisionErr := humanReviewDecisionFromParams(arguments)
			if decisionErr != nil {
				return nil, decisionErr
			}
			reviewDecision = decision
		}
	}
	bubble := s.delivery.BuildBubbleMessage(taskID, "status", controlBubbleText(action), currentTimeFromTask(s.runEngine, taskID))
	updatedTask, err := s.runEngine.ControlTask(taskID, action, bubble)
	if err != nil {
		switch {
		case errors.Is(err, runengine.ErrTaskNotFound):
			return nil, ErrTaskNotFound
		case errors.Is(err, runengine.ErrTaskStatusInvalid):
			return nil, ErrTaskStatusInvalid
		case errors.Is(err, runengine.ErrTaskAlreadyFinished):
			return nil, ErrTaskAlreadyFinished
		default:
			return nil, err
		}
	}
	if action == "resume" && wasHumanLoop {
		if traceResumedTask, traceBubble, _, resumed, resumeErr := s.resumeHumanLoopTask(updatedTask, reviewDecision); resumeErr != nil {
			return nil, resumeErr
		} else if resumed {
			updatedTask = traceResumedTask
			bubble = traceBubble
		}
	}
	if taskIsTerminal(updatedTask.Status) {
		if queueErr := s.drainSessionQueue(updatedTask.SessionID); queueErr != nil {
			return nil, queueErr
		}
	}

	return map[string]any{
		"task":           taskMap(updatedTask),
		"bubble_message": bubble,
	}, nil
}

// controlBubbleText returns the status bubble text for a task_control action.
func controlBubbleText(action string) string {
	switch action {
	case "pause":
		return "任务已暂停"
	case "resume":
		return "任务已继续执行"
	case "cancel":
		return "任务已取消"
	case "restart":
		return "任务已重新开始"
	default:
		return "任务状态已更新"
	}
}

func isSupportedTaskControlAction(action string) bool {
	switch action {
	case "pause", "resume", "cancel", "restart":
		return true
	default:
		return false
	}
}

// currentTimeFromTask returns the latest task update time formatted for bubble
// payloads.
func currentTimeFromTask(engine *runengine.Engine, taskID string) string {
	task, ok := engine.GetTask(taskID)
	if !ok {
		return ""
	}
	return task.UpdatedAt.Format(dateTimeLayout)
}

func humanReviewDecisionFromParams(arguments map[string]any) (map[string]any, error) {
	decision := mapValue(arguments, "review")
	if len(decision) == 0 {
		decision = mapValue(arguments, "human_review")
	}
	if len(decision) == 0 {
		return nil, fmt.Errorf("review decision is required to resume a human review task")
	}
	if strings.TrimSpace(stringValue(decision, "decision", "")) == "" {
		return nil, fmt.Errorf("review.decision is required to resume a human review task")
	}
	decisionValue := strings.TrimSpace(stringValue(decision, "decision", ""))
	if decisionValue != "approve" && decisionValue != "replan" {
		return nil, fmt.Errorf("unsupported review decision: %s", decisionValue)
	}
	if decisionValue == "replan" {
		if correctedIntent := mapValue(decision, "corrected_intent"); len(correctedIntent) == 0 {
			return nil, fmt.Errorf("review.corrected_intent is required when decision is replan")
		}
	}
	return cloneMap(decision), nil
}

func taskIsBlockedHumanLoop(task runengine.TaskRecord) bool {
	if task.Status != "blocked" || task.CurrentStep != "human_in_loop" {
		return false
	}
	return stringValue(task.PendingExecution, "kind", "") == "human_in_loop"
}
