package recommendation

import (
	"strings"
	"testing"
	"time"

	"github.com/cialloclaw/cialloclaw/services/local-service/internal/runengine"
)

func TestServiceGetBuildsRuntimeRecommendations(t *testing.T) {
	service := NewService()
	service.now = func() time.Time { return time.Date(2026, 4, 10, 9, 0, 0, 0, time.UTC) }

	result := service.Get(GenerateInput{
		Source:        "floating_ball",
		Scene:         "selected_text",
		PageTitle:     "Weekly Notes",
		AppName:       "editor",
		SelectionText: "This is a long block of selected text that should be summarized before any deeper editing happens.",
	})

	if result.CooldownHit {
		t.Fatal("expected first recommendation request to skip cooldown")
	}
	if len(result.Items) != 2 {
		t.Fatalf("expected two recommendation items, got %d", len(result.Items))
	}
	if !strings.HasPrefix(result.Items[0]["recommendation_id"].(string), "rec_") {
		t.Fatalf("expected runtime recommendation id, got %+v", result.Items[0]["recommendation_id"])
	}
	if result.Items[0]["intent"].(map[string]any)["name"] != "summarize" {
		t.Fatalf("expected long selection to prefer summarize, got %+v", result.Items[0]["intent"])
	}
}

func TestServiceSubmitFeedbackEnforcesCooldown(t *testing.T) {
	service := NewService()
	now := time.Date(2026, 4, 10, 9, 0, 0, 0, time.UTC)
	service.now = func() time.Time { return now }

	input := GenerateInput{
		Source:        "floating_ball",
		Scene:         "selected_text",
		PageTitle:     "Design Doc",
		AppName:       "editor",
		SelectionText: "Translate this paragraph into English for the external summary.",
		UnfinishedTasks: []runengine.TaskRecord{
			{
				TaskID: "task_001",
				Title:  "Active runtime task",
				Status: "processing",
				Intent: map[string]any{"name": "rewrite"},
			},
		},
	}

	first := service.Get(input)
	recommendationID := first.Items[0]["recommendation_id"].(string)
	if !service.SubmitFeedback(recommendationID, "negative") {
		t.Fatal("expected negative feedback to be applied")
	}

	second := service.Get(input)
	if !second.CooldownHit {
		t.Fatal("expected repeated request after negative feedback to hit cooldown")
	}
	if len(second.Items) != 0 {
		t.Fatalf("expected cooldown request to suppress items, got %+v", second.Items)
	}
}
