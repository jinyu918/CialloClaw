package rpc

import "strings"

// handleAgentInputSubmit handles agent.input.submit.
func (s *Server) handleAgentInputSubmit(params map[string]any) (any, *rpcError) {
	data, err := s.orchestrator.SubmitInput(params)
	return wrapOrchestratorResult(data, err)
}

// handleAgentTaskStart handles agent.task.start.
func (s *Server) handleAgentTaskStart(params map[string]any) (any, *rpcError) {
	data, err := s.orchestrator.StartTask(sanitizeTaskStartParams(params))
	return wrapOrchestratorResult(data, err)
}

// handleAgentTaskConfirm handles agent.task.confirm.
func (s *Server) handleAgentTaskConfirm(params map[string]any) (any, *rpcError) {
	data, err := s.orchestrator.ConfirmTask(params)
	return wrapOrchestratorResult(data, err)
}

// sanitizeTaskStartParams strips any client-supplied intent payload so
// agent.task.start continues to flow through the authoritative suggestion path.
func sanitizeTaskStartParams(params map[string]any) map[string]any {
	if len(params) == 0 {
		return nil
	}

	sanitized := make(map[string]any, len(params))
	for key, value := range params {
		if strings.TrimSpace(key) == "intent" {
			continue
		}
		sanitized[key] = value
	}
	return sanitized
}
