// Package prompt renders the instruction sent to Codex, choosing a template per
// incident playbook. Codex runs non-interactively with `glab`, `gh`, and `git`
// available and GITLAB_HOST / GITLAB_TOKEN / GH_TOKEN set in its environment.
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

// triageTmpl: evaluate the event and open a GitLab issue (issues/CI/push/...).
const triageTmpl = `You are a preliminary triage agent for CI / engineering incidents. Below is a webhook event from GitHub.

## Your task
1. Read the event and make a "preliminary evaluation" covering at least:
   - category: one or more of [bug, ci-failure, feature, question, security, infra, flaky-test]
   - severity: S1 (critical) / S2 (high) / S3 (medium) / S4 (low)
   - suggested priority: P0 / P1 / P2 / P3
   - likely-cause: your initial guess
   - impact: who / what is affected
   - next-steps: 2-4 bullet points
   Note this is a "preliminary" evaluation; uncertainty is allowed — when unsure, say so and give a confidence level (low/medium/high).

2. Create an issue in the internal GitLab project ` + "`{{.GitLabProject}}`" + ` using the glab CLI (GITLAB_HOST and GITLAB_TOKEN are already set, no login needed):

   glab issue create \
     --repo "{{.GitLabProject}}" \
     --title "<concise title, including the source repo>" \
     --description "<see body format below>" \
     --label "triage,<category>,severity::<S1..S4>,priority::<P0..P3>" \
     --yes

   The issue body should include: the preliminary evaluation (bulleted), the next-steps, and the
   original event (GitHub link and key fields), with a note at the end that "this issue was created by
   an automated triage agent; the evaluation is advisory only and needs human confirmation".
{{- if .IsGitHubIssue}}

3. Reply on the original GitHub issue so the reporter knows it was forwarded and triaged. Post one
   comment with gh (GH_TOKEN is set), linking the internal GitLab issue you just created:
     gh issue comment "{{.URL}}" --body "<short note: forwarded to the internal tracker and triaged; include the GitLab issue URL from step 2 and the one-line assessment; note it is automated>"
   If the comment fails, do not abort the whole run — still report the ISSUE_URL below and set
   GITHUB_COMMENT to "failed".

4. On success, print:
   ISSUE_URL: <issue URL returned by glab>
   GITHUB_COMMENT: <URL of the GitHub issue comment, or "failed">
   SUMMARY: <one-sentence summary of your evaluation>
   On failure, print ERROR: <reason>, and do not retry more than once.
{{- else}}

3. On success, print two lines:
   ISSUE_URL: <issue URL returned by glab>
   SUMMARY: <one-sentence summary of your evaluation>
   On failure, print ERROR: <reason>, and do not retry more than once.
{{- end}}

## Event info
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
const prReviewTmpl = `You are a PR review + mirror agent. Below is a GitHub pull_request event (action={{.Action}}).
The environment has GH_TOKEN (for gh) and GITLAB_HOST / GITLAB_TOKEN (for glab) set; git and the helper script gh-pr-mirror.sh are available.
The authoritative spec for this flow is AGENTS.md in the working directory — always follow its safety rules (reviews always comment mode, only push the mirror branch, never force-push, never leak tokens).

## Task A: review the GitHub PR and leave the review as a comment
1. Fetch content and diff:
     gh pr view "{{.URL}}" --json title,body,author,files,additions,deletions
     gh pr diff "{{.URL}}"
2. Do a preliminary code review focused on: correctness, possible bugs, test coverage, risk, readability. Bullet the key points and suggestions.
3. Post it in "comment" mode (do not approve, do not request-changes):
     gh pr review "{{.URL}}" --comment --body "<your review, in Markdown>"
   Note at the end of the review that it is "auto-generated by an agent and advisory only".

## Task B: mirror this PR as an internal GitLab MR
Use the stable branch name ` + "`{{.MirrorBranch}}`" + ` (so later merge-sync can match the same MR).
1. Push the mirror branch with the helper (automatic shallow checkout + safe token handling; do not assemble the token/URL yourself):
     gh-pr-mirror.sh {{.PR.Number}} {{.Repo}} "{{.MirrorRepoURL}}" {{.MirrorBranch}}
   (On success it prints MIRROR_PUSHED: ok; on failure it prints ERROR:, in which case just report ERROR and finish.)
2. Idempotently create/reuse the corresponding MR (target branch {{.TargetBranch}}); check first whether it exists, and if so reuse its URL:
     glab mr list   --repo "{{.PRProject}}" --source-branch "{{.MirrorBranch}}"
     glab mr create --repo "{{.PRProject}}" \
       --source-branch "{{.MirrorBranch}}" --target-branch "{{.TargetBranch}}" \
       --title "[mirror] {{.Title}}" \
       --description "Mirrored from GitHub PR {{.URL}} (#{{.PR.Number}}). Includes the review summary above. Created by an automated agent." \
       --yes

## Output (final)
   REVIEW_POSTED: yes|no
   MR_URL: <GitLab MR URL, if any>
   SUMMARY: <one-sentence summary of the review and mirror result>
   If any step fails, print ERROR: <reason>, and do not retry more than once.

## PR info
- repo: {{.Repo}}  PR: #{{.PR.Number}}  link: {{.URL}}
- head (source): {{.PR.HeadRef}} @ {{.PR.HeadSHA}}  ->  base (target): {{.PR.BaseRef}}
- head repo: {{.PR.HeadRepoURL}}
- title: {{.Title}}
- description:
{{if .Summary}}{{.Summary}}{{else}}(none){{end}}
`

// prMergeTmpl: the GitHub PR was merged; merge the mirrored GitLab MR.
const prMergeTmpl = `You are a PR merge-sync agent. GitHub PR #{{.PR.Number}} ({{.URL}}) has been **merged**
(merge commit {{.PR.MergeSHA}}). Sync this merge to the internal GitLab.
The environment has GH_TOKEN, GITLAB_HOST / GITLAB_TOKEN set; git and the helper script gh-pr-mirror.sh are available. The mirror branch name is ` + "`{{.MirrorBranch}}`" + `.
The authoritative spec for this flow is AGENTS.md in the working directory — always follow its safety rules (do not force-push on conflict; report ERROR instead).

## Task
1. Push the merged head to the mirror branch with the helper (safe token handling):
     gh-pr-mirror.sh {{.PR.Number}} {{.Repo}} "{{.MirrorRepoURL}}" {{.MirrorBranch}}
2. Find the corresponding GitLab MR and merge it (source branch {{.MirrorBranch}}, target {{.TargetBranch}}):
     glab mr list --repo "{{.PRProject}}" --source-branch "{{.MirrorBranch}}"
     glab mr merge <iid> --repo "{{.PRProject}}" --yes
   If no corresponding MR exists, create it first with glab mr create (source {{.MirrorBranch}} / target {{.TargetBranch}}), then merge.
   If GitLab cannot auto-merge due to a conflict, do not force-push — print ERROR with an explanation instead.

## Output (final)
   MR_URL: <URL of the merged GitLab MR>
   SUMMARY: <one-sentence summary of the sync result>
   On failure, print ERROR: <reason>, and do not retry more than once.
`
