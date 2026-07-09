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

func TestEndpointURL(t *testing.T) {
	cases := []struct {
		name, publicURL, path, want string
	}{
		{"base plus path", "https://ci.example.com", "/webhook", "https://ci.example.com/webhook"},
		{"trailing slash", "https://ci.example.com/", "/webhook", "https://ci.example.com/webhook"},
		{"already full", "https://ci.example.com/webhook", "/webhook", "https://ci.example.com/webhook"},
		{"empty public url", "", "/webhook", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &config.Config{Server: config.ServerConfig{PublicURL: tc.publicURL, Path: tc.path}}
			if got := EndpointURL(cfg); got != tc.want {
				t.Fatalf("EndpointURL() = %q, want %q", got, tc.want)
			}
		})
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
