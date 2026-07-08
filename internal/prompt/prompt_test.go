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
		PR: config.PRConfig{
			ReviewMode:    "comment",
			GitLabProject: "team/mirror",
			GitLabRepoURL: "https://gitlab.internal/team/mirror.git",
			TargetBranch:  "main",
		},
	}
}

func TestRender_Triage(t *testing.T) {
	inc := github.Incident{Playbook: github.PlaybookTriageIssue, EventType: "workflow_run", Repo: "o/r", Title: "boom",
		Extra: map[string]string{"conclusion": "failure"}}
	out, err := Render(inc, cfg())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "glab issue create") || !strings.Contains(out, "team/incidents") {
		t.Fatalf("triage prompt missing expected content:\n%s", out)
	}
	// A CI event has no GitHub issue to reply to.
	if strings.Contains(out, "gh issue comment") {
		t.Fatalf("non-issue triage should not include a GitHub reply step:\n%s", out)
	}
}

func TestRender_TriageIssueRepliesBack(t *testing.T) {
	inc := github.Incident{Playbook: github.PlaybookTriageIssue, EventType: "issues", Repo: "o/r",
		Title: "[GitHub issue #5] boom", URL: "https://github.com/o/r/issues/5"}
	out, err := Render(inc, cfg())
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"gh issue comment", "https://github.com/o/r/issues/5", "GITHUB_COMMENT:"} {
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
	for _, want := range []string{"gh pr review", "--comment", "gh-pr-7", "team/mirror", "glab mr create", "gh-pr-mirror.sh", "AGENTS.md"} {
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
	for _, want := range []string{"glab mr merge", "gh-pr-7", "MR_URL:", "gh-pr-mirror.sh"} {
		if !strings.Contains(out, want) {
			t.Fatalf("pr_merge prompt missing %q:\n%s", want, out)
		}
	}
}
