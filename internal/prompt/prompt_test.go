package prompt

import (
	"strings"
	"testing"

	"github.com/qwe7002-ai/ci-webhook-codex-agent/internal/config"
	"github.com/qwe7002-ai/ci-webhook-codex-agent/internal/github"
)

func cfg() *config.Config {
	return &config.Config{
		GitLab: config.GitLabConfig{Project: "team/incidents"},
	}
}

// The prompts are thin: they name the AGENTS.md playbook and pass parameters.
// The procedure itself lives in AGENTS.md, so we assert on the playbook name and
// the injected values, not on the step-by-step commands.

func TestRender_Triage(t *testing.T) {
	inc := github.Incident{Playbook: github.PlaybookTriageIssue, EventType: "workflow_run", Repo: "o/r", Title: "boom",
		Extra: map[string]string{"conclusion": "failure"}}
	out, err := Render(inc, cfg())
	if err != nil {
		t.Fatal(err)
	}
	// Names the playbook, resolves <PROJECT> from projects.toml, and keeps the
	// configured project as the fallback.
	for _, want := range []string{"triage_issue", "projects.toml", "team/incidents"} {
		if !strings.Contains(out, want) {
			t.Fatalf("triage prompt missing %q:\n%s", want, out)
		}
	}
	// A CI event has no GitHub issue, so no reply-back parameter.
	if strings.Contains(out, "GITHUB_COMMENT") {
		t.Fatalf("non-issue triage should not mention a GitHub reply:\n%s", out)
	}
}

func TestRender_TriageIssueRepliesBack(t *testing.T) {
	inc := github.Incident{Playbook: github.PlaybookTriageIssue, EventType: "issues", Repo: "o/r",
		Title: "[GitHub issue #5] boom", URL: "https://github.com/o/r/issues/5"}
	out, err := Render(inc, cfg())
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"triage_issue", "GITHUB_COMMENT", "https://github.com/o/r/issues/5"} {
		if !strings.Contains(out, want) {
			t.Fatalf("issue triage prompt missing %q:\n%s", want, out)
		}
	}
}

func TestRender_PRReview(t *testing.T) {
	inc := github.Incident{
		Playbook: github.PlaybookPRReview, Repo: "o/r", Title: "add feature",
		URL: "https://gh/pr/7",
		PR:  &github.PRInfo{Number: 7, HeadRef: "feat", BaseRef: "main", HeadRepoURL: "https://github.com/o/r.git"},
	}
	out, err := Render(inc, cfg())
	if err != nil {
		t.Fatal(err)
	}
	// Names the playbook and passes the source repo + mirror branch; Codex
	// resolves the mirror project/URL/target branch from projects.toml.
	for _, want := range []string{"pr_review", "AGENTS.md", "gh-pr-7", "o/r"} {
		if !strings.Contains(out, want) {
			t.Fatalf("pr_review prompt missing %q:\n%s", want, out)
		}
	}
}

func TestRender_PRMergeSync(t *testing.T) {
	inc := github.Incident{
		Playbook: github.PlaybookPRMergeSync, Repo: "o/r", URL: "https://gh/pr/7",
		PR: &github.PRInfo{Number: 7, Merged: true, MergeSHA: "abc", HeadRef: "feat", BaseRef: "main"},
	}
	out, err := Render(inc, cfg())
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"pr_merge_sync", "gh-pr-7", "o/r", "abc"} {
		if !strings.Contains(out, want) {
			t.Fatalf("pr_merge prompt missing %q:\n%s", want, out)
		}
	}
}
