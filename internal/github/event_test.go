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
				"pull_request": {Enabled: true, Actions: []string{"opened", "reopened", "closed"}},
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

func TestEvaluate_PullRequestPlaybooks(t *testing.T) {
	opened := []byte(`{"action":"opened","repository":{"full_name":"o/r"},"number":7,"pull_request":{"title":"add feature","html_url":"https://gh/pr/7","head":{"ref":"feat","sha":"deadbeef","repo":{"clone_url":"https://github.com/o/r.git"}},"base":{"ref":"main"}}}`)
	d, err := Evaluate(testCfg(), "pull_request", "p1", opened)
	if err != nil {
		t.Fatal(err)
	}
	if !d.Process || d.Incident.Playbook != PlaybookPRReview {
		t.Fatalf("expected pr_review playbook, got process=%v playbook=%q", d.Process, d.Incident.Playbook)
	}
	if d.Incident.PR == nil || d.Incident.PR.Number != 7 || d.Incident.PR.HeadRef != "feat" {
		t.Fatalf("PR info not populated: %+v", d.Incident.PR)
	}

	mergedClosed := []byte(`{"action":"closed","repository":{"full_name":"o/r"},"number":7,"pull_request":{"title":"add feature","html_url":"https://gh/pr/7","merged":true,"merge_commit_sha":"abc","head":{"ref":"feat"},"base":{"ref":"main"}}}`)
	d, err = Evaluate(testCfg(), "pull_request", "p2", mergedClosed)
	if err != nil {
		t.Fatal(err)
	}
	if !d.Process || d.Incident.Playbook != PlaybookPRMergeSync {
		t.Fatalf("expected pr_merge_sync, got process=%v playbook=%q", d.Process, d.Incident.Playbook)
	}

	unmergedClosed := []byte(`{"action":"closed","repository":{"full_name":"o/r"},"number":8,"pull_request":{"title":"abandoned","merged":false,"head":{"ref":"x"},"base":{"ref":"main"}}}`)
	d, err = Evaluate(testCfg(), "pull_request", "p3", unmergedClosed)
	if err != nil {
		t.Fatal(err)
	}
	if d.Process {
		t.Fatal("expected closed-without-merge PR to be skipped")
	}
}
