package github

import (
	"testing"

	"github.com/qwe7002-ai/ci-webhook-codex-agent/internal/config"
)

func testCfg() *config.Config {
	return &config.Config{
		GitHub: config.GitHubConfig{
			WebhookSecret: "x",
			Events: map[string]config.EventFilter{
				"issues":       {Enabled: true, Actions: []string{"opened"}},
				"workflow_run": {Enabled: true, Actions: []string{"completed"}, Conclusions: []string{"failure"}},
				"push":         {Enabled: false},
			},
		},
	}
}

func TestEvaluate_DisabledEvent(t *testing.T) {
	d, err := Evaluate(testCfg(), "push", "d1", []byte(`{"ref":"refs/heads/main"}`))
	if err != nil {
		t.Fatal(err)
	}
	if d.Process {
		t.Fatalf("expected disabled event to be skipped, got %+v", d)
	}
}

func TestEvaluate_IssuesActionFilter(t *testing.T) {
	// "closed" is not in the allowed actions -> skip.
	body := []byte(`{"action":"closed","repository":{"full_name":"o/r"},"issue":{"number":3,"title":"t"}}`)
	d, err := Evaluate(testCfg(), "issues", "d2", body)
	if err != nil {
		t.Fatal(err)
	}
	if d.Process {
		t.Fatal("expected closed issue to be filtered out")
	}

	// "opened" passes and is normalized.
	body = []byte(`{"action":"opened","repository":{"full_name":"o/r"},"issue":{"number":3,"title":"boom","body":"details","html_url":"https://gh/i/3"}}`)
	d, err = Evaluate(testCfg(), "issues", "d3", body)
	if err != nil {
		t.Fatal(err)
	}
	if !d.Process {
		t.Fatal("expected opened issue to be processed")
	}
	if d.Incident.Repo != "o/r" || d.Incident.URL != "https://gh/i/3" {
		t.Fatalf("unexpected incident: %+v", d.Incident)
	}
}

func TestEvaluate_WorkflowRunConclusionFilter(t *testing.T) {
	pass := []byte(`{"action":"completed","repository":{"full_name":"o/r"},"workflow_run":{"name":"CI","conclusion":"failure","head_branch":"main","html_url":"https://gh/run/1"}}`)
	d, err := Evaluate(testCfg(), "workflow_run", "d4", pass)
	if err != nil {
		t.Fatal(err)
	}
	if !d.Process {
		t.Fatal("expected failing workflow_run to be processed")
	}
	if d.Incident.Extra["conclusion"] != "failure" {
		t.Fatalf("expected conclusion=failure, got %q", d.Incident.Extra["conclusion"])
	}

	success := []byte(`{"action":"completed","repository":{"full_name":"o/r"},"workflow_run":{"name":"CI","conclusion":"success"}}`)
	d, err = Evaluate(testCfg(), "workflow_run", "d5", success)
	if err != nil {
		t.Fatal(err)
	}
	if d.Process {
		t.Fatal("expected successful workflow_run to be filtered out")
	}
}
