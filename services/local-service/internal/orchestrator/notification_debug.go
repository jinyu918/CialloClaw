package orchestrator

// PendingNotifications returns the buffered notification list for a task
// without consuming it. Debug transports use this read-only path when they need
// to inspect pending events but must not disturb the ordered replay pipeline.
func (s *Service) PendingNotifications(taskID string) ([]map[string]any, error) {
	notifications, ok := s.runEngine.PendingNotifications(taskID)
	if !ok {
		return nil, ErrTaskNotFound
	}

	items := make([]map[string]any, 0, len(notifications))
	for _, notification := range notifications {
		items = append(items, map[string]any{
			"method":     notification.Method,
			"params":     cloneMap(notification.Params),
			"created_at": notification.CreatedAt.Format(dateTimeLayout),
		})
	}

	return items, nil
}

// DrainNotifications returns and clears the buffered notification list for a
// task. The orchestrator exposes this explicit destructive read so transports
// can replay notifications exactly once instead of coupling queue semantics to
// ordinary task detail or list reads.
func (s *Service) DrainNotifications(taskID string) ([]map[string]any, error) {
	notifications, ok := s.runEngine.DrainNotifications(taskID)
	if !ok {
		return nil, ErrTaskNotFound
	}

	items := make([]map[string]any, 0, len(notifications))
	for _, notification := range notifications {
		items = append(items, map[string]any{
			"method":     notification.Method,
			"params":     cloneMap(notification.Params),
			"created_at": notification.CreatedAt.Format(dateTimeLayout),
		})
	}

	return items, nil
}
