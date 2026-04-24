package intent

import (
	"strings"

	contextsvc "github.com/cialloclaw/cialloclaw/services/local-service/internal/context"
)

// Analyze performs the coarsest possible input gate for the main flow.
// The current pipeline only distinguishes missing input from actionable input.
func (s *Service) Analyze(input string) string {
	if strings.TrimSpace(input) == "" {
		return "waiting_input"
	}

	return "confirming_intent"
}

func (s *Service) AnalyzeSnapshot(snapshot contextsvc.TaskContextSnapshot) string {
	if strings.TrimSpace(snapshot.Text) == "" &&
		strings.TrimSpace(snapshot.SelectionText) == "" &&
		strings.TrimSpace(snapshot.ErrorText) == "" &&
		len(snapshot.Files) == 0 {
		return "waiting_input"
	}

	return "confirming_intent"
}
