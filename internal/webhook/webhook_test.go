package webhook

import (
	"reflect"
	"testing"

	"github.com/qwe7002-ai/ci-webhook-codex-agent/internal/config"
)

func TestEnabledEvents(t *testing.T) {
	cfg := &config.Config{
		GitHub: config.GitHubConfig{
			Events: map[string]config.EventFilter{
				"workflow_run": {Enabled: true},
				"issues":       {Enabled: true},
				"push":         {Enabled: false},
				"check_run":    {Enabled: false},
			},
		},
	}
	got := EnabledEvents(cfg)
	want := []string{"issues", "workflow_run"} // enabled only, sorted
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("EnabledEvents() = %v, want %v", got, want)
	}
}

func TestEnabledEventsEmpty(t *testing.T) {
	cfg := &config.Config{GitHub: config.GitHubConfig{Events: map[string]config.EventFilter{
		"push": {Enabled: false},
	}}}
	if got := EnabledEvents(cfg); len(got) != 0 {
		t.Fatalf("EnabledEvents() = %v, want empty", got)
	}
}
