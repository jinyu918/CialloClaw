package orchestrator

import (
	"context"
	"time"

	"github.com/cialloclaw/cialloclaw/services/local-service/internal/execution"
	"github.com/cialloclaw/cialloclaw/services/local-service/internal/runengine"
	"github.com/cialloclaw/cialloclaw/services/local-service/internal/tools"
)

func (s *Service) executeScreenAnalysisAfterApproval(task runengine.TaskRecord, pendingExecution map[string]any) (runengine.TaskRecord, map[string]any, map[string]any, error) {
	if s.executor == nil || s.executor.ScreenClient() == nil {
		failedTask, failureBubble := s.failExecutionTask(task, map[string]any{"name": "screen_analyze"}, execution.Result{}, tools.ErrScreenCaptureNotSupported)
		return failedTask, failureBubble, nil, nil
	}
	screenClient := s.executor.ScreenClient()
	cleanupExpiredScreenTemps(screenClient, "expired_session_scan", time.Now().UTC())
	captureMode := screenCaptureModeFromArguments(pendingExecution)
	source := firstNonEmptyString(stringValue(pendingExecution, "source", ""), "screen_capture")
	screenSession, err := screenClient.StartSession(context.Background(), tools.ScreenSessionStartInput{
		SessionID:   task.SessionID,
		TaskID:      task.TaskID,
		RunID:       task.RunID,
		Source:      source,
		CaptureMode: captureMode,
	})
	if err != nil {
		failedTask, failureBubble := s.failExecutionTask(task, map[string]any{"name": "screen_analyze"}, execution.Result{}, err)
		return failedTask, failureBubble, nil, nil
	}
	candidate, err := captureScreenCandidateAfterApproval(screenClient, screenSession.ScreenSessionID, task, pendingExecution, captureMode)
	if err != nil {
		expireAndCleanupScreenSession(screenClient, screenSession.ScreenSessionID, "capture_failed")
		failedTask, failureBubble := s.failExecutionTask(task, map[string]any{"name": "screen_analyze"}, execution.Result{}, err)
		return failedTask, failureBubble, nil, nil
	}
	execIntent := map[string]any{
		"name": "screen_analyze_candidate",
		"arguments": map[string]any{
			"task_id":           task.TaskID,
			"run_id":            task.RunID,
			"screen_session_id": screenSession.ScreenSessionID,
			"frame_id":          candidate.FrameID,
			"path":              candidate.Path,
			"capture_mode":      string(candidate.CaptureMode),
			"source":            candidate.Source,
			"captured_at":       candidate.CapturedAt.UTC().Format(time.RFC3339),
			"retention_policy":  string(candidate.RetentionPolicy),
			"language":          stringValue(pendingExecution, "language", "eng"),
			"evidence_role":     stringValue(pendingExecution, "evidence_role", "error_evidence"),
			"target_object":     stringValue(pendingExecution, "target_object", "current_screen"),
		},
	}
	updatedTask, bubble, deliveryResult, _, err := s.executeTask(task, snapshotFromTask(task), execIntent)
	if err != nil {
		expireAndCleanupScreenSession(screenClient, screenSession.ScreenSessionID, "analysis_failed")
		return runengine.TaskRecord{}, nil, nil, err
	}
	// Successful analyses stop the session so stale authorizations do not linger.
	// Failed terminal attempts still expire and clean temp session outputs because
	// no durable artifact handoff completed for that branch.
	if updatedTask.Status == "completed" {
		stopScreenSession(screenClient, screenSession.ScreenSessionID, "analysis_completed")
		cleanupSuccessfulScreenSession(screenClient, screenSession.ScreenSessionID, candidate.Path)
	} else if taskIsTerminal(updatedTask.Status) {
		expireAndCleanupScreenSession(screenClient, screenSession.ScreenSessionID, "analysis_failed")
	}
	return updatedTask, bubble, deliveryResult, nil
}

// captureScreenCandidateAfterApproval keeps the controlled screen entry on one
// orchestrator path while still selecting the owner-5 capture primitive that
// matches the approved screen analysis mode.
func captureScreenCandidateAfterApproval(screenClient tools.ScreenCaptureClient, screenSessionID string, task runengine.TaskRecord, pendingExecution map[string]any, captureMode tools.ScreenCaptureMode) (tools.ScreenFrameCandidate, error) {
	input := tools.ScreenCaptureInput{
		ScreenSessionID: screenSessionID,
		TaskID:          task.TaskID,
		RunID:           task.RunID,
		CaptureMode:     captureMode,
		Source:          firstNonEmptyString(stringValue(pendingExecution, "source", ""), "screen_capture"),
		SourcePath:      stringValue(pendingExecution, "source_path", ""),
	}
	switch captureMode {
	case tools.ScreenCaptureModeKeyframe:
		result, err := screenClient.CaptureKeyframe(context.Background(), input)
		if err != nil {
			return tools.ScreenFrameCandidate{}, err
		}
		return result.Candidate, nil
	default:
		return screenClient.CaptureScreenshot(context.Background(), input)
	}
}

func stopScreenSession(screenClient tools.ScreenCaptureClient, screenSessionID, reason string) {
	if screenClient == nil || screenSessionID == "" {
		return
	}
	_, _ = screenClient.StopSession(context.Background(), screenSessionID, reason)
}

func cleanupSuccessfulScreenSession(screenClient tools.ScreenCaptureClient, screenSessionID, capturePath string) {
	if screenClient == nil || screenSessionID == "" {
		return
	}
	_, _ = screenClient.CleanupSessionArtifacts(context.Background(), tools.ScreenCleanupInput{
		ScreenSessionID: screenSessionID,
		Reason:          "analysis_completed",
		Paths:           []string{capturePath},
	})
}

func expireAndCleanupScreenSession(screenClient tools.ScreenCaptureClient, screenSessionID, reason string) {
	if screenClient == nil || screenSessionID == "" {
		return
	}
	_, _ = screenClient.ExpireSession(context.Background(), screenSessionID, reason)
	_, _ = screenClient.CleanupSessionArtifacts(context.Background(), tools.ScreenCleanupInput{
		ScreenSessionID: screenSessionID,
		Reason:          reason,
	})
}

func cleanupExpiredScreenTemps(screenClient tools.ScreenCaptureClient, reason string, expiredBefore time.Time) {
	if screenClient == nil {
		return
	}
	if expiredBefore.IsZero() {
		expiredBefore = time.Now().UTC()
	}
	_, _ = screenClient.CleanupExpiredScreenTemps(context.Background(), tools.ScreenCleanupInput{
		Reason:        reason,
		ExpiredBefore: expiredBefore.UTC(),
	})
}
