package intent

import (
	"strings"

	contextsvc "github.com/cialloclaw/cialloclaw/services/local-service/internal/context"
)

func screenAnalyzeIntent(snapshot contextsvc.TaskContextSnapshot) (map[string]any, bool) {
	if !shouldUseScreenAnalyze(snapshot) {
		return nil, false
	}
	intent := intentPayload("screen_analyze")
	arguments := map[string]any{
		"language":      "eng",
		"evidence_role": screenEvidenceRole(snapshot),
	}
	if strings.TrimSpace(snapshot.PageTitle) != "" {
		arguments["page_title"] = snapshot.PageTitle
	}
	if strings.TrimSpace(snapshot.WindowTitle) != "" {
		arguments["window_title"] = snapshot.WindowTitle
	}
	if strings.TrimSpace(snapshot.VisibleText) != "" {
		arguments["visible_text"] = snapshot.VisibleText
	}
	if strings.TrimSpace(snapshot.ScreenSummary) != "" {
		arguments["screen_summary"] = snapshot.ScreenSummary
	}
	intent["arguments"] = arguments
	return intent, true
}

func shouldUseScreenAnalyze(snapshot contextsvc.TaskContextSnapshot) bool {
	if snapshot.InputType != "text" {
		return false
	}
	text := strings.TrimSpace(strings.ToLower(snapshot.Text))
	if text == "" {
		return false
	}
	hasVisualTarget := strings.TrimSpace(snapshot.PageTitle) != "" || strings.TrimSpace(snapshot.WindowTitle) != "" || strings.TrimSpace(snapshot.VisibleText) != "" || strings.TrimSpace(snapshot.ScreenSummary) != ""
	if !hasVisualTarget {
		return false
	}
	visualIntentMarkers := []string{"screen", "page", "window", "ui", "screenshot", "页面", "屏幕", "界面", "窗口", "报错", "错误"}
	analysisMarkers := []string{"look", "see", "check", "analyze", "inspect", "review", "看看", "查看", "分析", "检查", "识别", "定位", "解释"}
	return containsAny(text, visualIntentMarkers...) && containsAny(text, analysisMarkers...)
}

func screenEvidenceRole(snapshot contextsvc.TaskContextSnapshot) string {
	combined := strings.ToLower(strings.Join([]string{snapshot.Text, snapshot.ErrorText, snapshot.VisibleText, snapshot.ScreenSummary}, " "))
	if containsAny(combined, "error", "warning", "exception", "报错", "错误", "异常", "warning") {
		return "error_evidence"
	}
	return "page_context"
}
