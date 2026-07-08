// Package prompt renders the instruction sent to Codex, choosing a template per
// incident playbook. Codex runs non-interactively with `glab`, `gh`, and `git`
// available; gh/glab authenticate from their own CLI config and GITLAB_HOST is
// set in the environment so glab targets the internal instance.
package prompt

import (
	"bytes"
	"fmt"
	"sort"
	"text/template"

	"github.com/qwe7002-ai/ci-webhook-codex-agent/internal/config"
	"github.com/qwe7002-ai/ci-webhook-codex-agent/internal/github"
)

type view struct {
	github.Incident
	GitLabProject string   // issue project (triage) — from gitlab.project
	PRProject     string   // MR project (pr playbooks) — from pr.gitlab_project
	MirrorRepoURL string   // git push target for mirrored PR branches
	TargetBranch  string   // MR base branch
	ReviewMode    string   // gh review mode (comment)
	MirrorBranch  string   // stable GitLab source branch for this PR
	ExtraLines    []string // formatted Extra map, for triage
	IsGitHubIssue bool     // event is a GitHub issue → we can reply back on it
}

// Render builds the Codex prompt for one incident based on its playbook.
func Render(inc github.Incident, cfg *config.Config) (string, error) {
	v := view{
		Incident:      inc,
		GitLabProject: cfg.GitLab.Project,
		PRProject:     cfg.PR.GitLabProject,
		MirrorRepoURL: cfg.PR.GitLabRepoURL,
		TargetBranch:  cfg.PR.TargetBranch,
		ReviewMode:    cfg.PR.ReviewMode,
		ExtraLines:    formatExtra(inc.Extra),
		IsGitHubIssue: inc.EventType == "issues",
	}
	if inc.PR != nil {
		// Stable, matchable source branch so merge-sync can find the same MR.
		v.MirrorBranch = fmt.Sprintf("gh-pr-%d", inc.PR.Number)
	}

	tmpl, ok := templates[inc.Playbook]
	if !ok {
		tmpl = templates[github.PlaybookTriageIssue]
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, v); err != nil {
		return "", err
	}
	return buf.String(), nil
}

func formatExtra(extra map[string]string) []string {
	keys := make([]string, 0, len(extra))
	for k := range extra {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	lines := make([]string, 0, len(keys))
	for _, k := range keys {
		lines = append(lines, k+": "+extra[k])
	}
	return lines
}

var templates = map[string]*template.Template{
	github.PlaybookTriageIssue: template.Must(template.New("triage").Parse(triageTmpl)),
	github.PlaybookPRReview:    template.Must(template.New("pr_review").Parse(prReviewTmpl)),
	github.PlaybookPRMergeSync: template.Must(template.New("pr_merge").Parse(prMergeTmpl)),
}

// The per-call prompts are deliberately thin: the full procedure, safety rules,
// projects.toml resolution, and output contract live once in AGENTS.md, which is
// passed to Codex as the call's system instructions (the global prompt). Each
// prompt only names the playbook and supplies the event data and runtime params.

// triageTmpl: evaluate the event and open a GitLab issue (issues/CI/push/...).
const triageTmpl = `Run the triage_issue playbook from AGENTS.md for the GitHub event below.

## Parameters
- Resolve <PROJECT> from projects.toml for the source repo (see playbook).
- issue project fallback if projects.toml is unreadable: {{.GitLabProject}}
{{- if .IsGitHubIssue}}
- Source is a GitHub issue: after filing, reply on it and report GITHUB_COMMENT (see playbook).
{{- end}}

## Event
- event: {{.EventType}}{{if .Action}} / {{.Action}}{{end}}
- source repo: {{.Repo}}
- title: {{.Title}}
- actor: {{.Actor}}
- GitHub link: {{.URL}}
- delivery id: {{.DeliveryID}}
{{- range .ExtraLines}}
- {{.}}
{{- end}}

## Event body
{{if .Summary}}{{.Summary}}{{else}}(no body){{end}}
`

// prReviewTmpl: review a GitHub PR (gh) and mirror it as a GitLab MR (git + glab).
const prReviewTmpl = `Run the pr_review playbook from AGENTS.md for the GitHub pull_request event below (action={{.Action}}).

## Parameters
- source repo: {{.Repo}}
- PR number: {{.PR.Number}}
- PR URL: {{.URL}}
- PR title: {{.Title}}
- mirror branch: {{.MirrorBranch}}
- MR target branch (<TARGET>): {{.TargetBranch}}
- projects.toml fallback if unreadable — MIRROR_PROJECT: {{.PRProject}}, mirror git URL: {{.MirrorRepoURL}}

## PR info
- head (source): {{.PR.HeadRef}} @ {{.PR.HeadSHA}}  ->  base (target): {{.PR.BaseRef}}
- head repo: {{.PR.HeadRepoURL}}

## PR description
{{if .Summary}}{{.Summary}}{{else}}(none){{end}}
`

// prMergeTmpl: the GitHub PR was merged; merge the mirrored GitLab MR.
const prMergeTmpl = `Run the pr_merge_sync playbook from AGENTS.md: GitHub PR #{{.PR.Number}} ({{.URL}}) has been merged (merge commit {{.PR.MergeSHA}}). Sync it to the internal GitLab.

## Parameters
- source repo: {{.Repo}}
- PR number: {{.PR.Number}}
- mirror branch: {{.MirrorBranch}}
- MR target branch: {{.TargetBranch}}
- projects.toml fallback if unreadable — MIRROR_PROJECT: {{.PRProject}}, mirror git URL: {{.MirrorRepoURL}}
`
