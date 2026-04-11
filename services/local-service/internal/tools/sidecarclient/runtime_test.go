package sidecarclient

import (
	"testing"

	"github.com/cialloclaw/cialloclaw/services/local-service/internal/plugin"
)

func TestPlaywrightSidecarRuntimeAvailability(t *testing.T) {
	runtime := NewPlaywrightSidecarRuntime(plugin.NewService())
	if runtime.Name() != "playwright_sidecar" {
		t.Fatalf("unexpected runtime name: %q", runtime.Name())
	}
	if !runtime.Available() {
		t.Fatal("expected runtime to be available when plugin declares sidecar")
	}
}

func TestPlaywrightSidecarRuntimeUnavailableWithoutPlugin(t *testing.T) {
	runtime := NewPlaywrightSidecarRuntime(nil)
	if runtime.Available() {
		t.Fatal("expected runtime to be unavailable without plugin service")
	}
}
