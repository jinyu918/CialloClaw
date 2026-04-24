package orchestrator

import "github.com/cialloclaw/cialloclaw/services/local-service/internal/taskinspector"

// TaskInspectorConfigGet handles agent.task_inspector.config.get.
func (s *Service) TaskInspectorConfigGet() (map[string]any, error) {
	return s.runEngine.InspectorConfig(), nil
}

// TaskInspectorConfigUpdate handles agent.task_inspector.config.update.
func (s *Service) TaskInspectorConfigUpdate(params map[string]any) (map[string]any, error) {
	effective := s.runEngine.UpdateInspectorConfig(params)
	return map[string]any{
		"updated":          true,
		"effective_config": effective,
	}, nil
}

// TaskInspectorRun handles agent.task_inspector.run and returns the inspection
// summary plus suggestions.
func (s *Service) TaskInspectorRun(params map[string]any) (map[string]any, error) {
	config := s.runEngine.InspectorConfig()
	targetSources := stringSliceValue(params["target_sources"])
	notepadItems, _ := s.runEngine.NotepadItems("", 0, 0)
	unfinishedTasks, _ := s.runEngine.ListTasks("unfinished", "updated_at", "desc", 0, 0)
	finishedTasks, _ := s.runEngine.ListTasks("finished", "finished_at", "desc", 0, 0)

	result := s.inspector.Run(taskinspector.RunInput{
		Reason:          stringValue(params, "reason", ""),
		TargetSources:   targetSources,
		Config:          config,
		UnfinishedTasks: unfinishedTasks,
		FinishedTasks:   finishedTasks,
		NotepadItems:    notepadItems,
	})
	if result.SourceSynced {
		if err := s.runEngine.SyncNotepadItems(result.NotepadItems); err != nil {
			return nil, err
		}
	}

	return map[string]any{
		"inspection_id": result.InspectionID,
		"summary":       result.Summary,
		"suggestions":   append([]string(nil), result.Suggestions...),
	}, nil
}
