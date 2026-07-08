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
const triageTmpl = `你是一個 CI / 研發事故的初步分診 (triage) agent。以下是一則來自 GitHub 的 webhook 事件。

## 你的任務
1. 閱讀事件內容,做出「初步評判 (preliminary evaluation)」,至少涵蓋:
   - 類別 category: 從 [bug, ci-failure, feature, question, security, infra, flaky-test] 擇一或多個
   - 嚴重度 severity: S1(critical) / S2(high) / S3(medium) / S4(low)
   - 建議優先級 priority: P0 / P1 / P2 / P3
   - 可能原因 likely-cause: 你的初步推測
   - 影響範圍 impact: 誰/什麼會受影響
   - 下一步建議 next-steps: 條列 2~4 點
   注意這是「初步」評判,允許不確定;不確定時明說並給出信心水準 (low/medium/high)。

2. 在內網 GitLab 專案 ` + "`{{.GitLabProject}}`" + ` 建立一個 issue,使用 glab CLI(已設定好 GITLAB_HOST 與 GITLAB_TOKEN,無需登入):

   glab issue create \
     --repo "{{.GitLabProject}}" \
     --title "<簡潔標題,含來源 repo>" \
     --description "<見下方內文格式>" \
     --label "triage,<category>,severity::<S1..S4>,priority::<P0..P3>" \
     --yes

   issue 內文請包含:初步評判(條列)、下一步建議、原始事件(GitHub 連結與關鍵欄位),
   並在結尾註明「此 issue 由自動化 triage agent 建立,評判僅供參考,需人工確認」。

3. 建立成功後,輸出兩行:
   ISSUE_URL: <glab 回傳的 issue 網址>
   SUMMARY: <一句話總結你的評判>
   失敗則輸出 ERROR: <原因>,不要重試超過一次。

## 事件資訊
- 事件類型 event: {{.EventType}}{{if .Action}} / {{.Action}}{{end}}
- 來源 repo: {{.Repo}}
- 標題 title: {{.Title}}
- 觸發者 actor: {{.Actor}}
- GitHub 連結: {{.URL}}
- delivery id: {{.DeliveryID}}
{{- range .ExtraLines}}
- {{.}}
{{- end}}

## 事件內容 (body)
{{if .Summary}}{{.Summary}}{{else}}(無內文){{end}}
`

// prReviewTmpl: review a GitHub PR (gh) and mirror it as a GitLab MR (git + glab).
const prReviewTmpl = `你是一個 PR 審查 + 鏡像 agent。以下是一則 GitHub pull_request 事件(action={{.Action}})。
環境已設定 GH_TOKEN(供 gh)、GITLAB_HOST / GITLAB_TOKEN(供 glab),git 可用。

## 任務 A:審查 GitHub PR 並以 comment 留下審查
1. 取得內容與 diff:
     gh pr view "{{.URL}}" --json title,body,author,files,additions,deletions
     gh pr diff "{{.URL}}"
2. 做初步程式碼審查,聚焦:正確性、可能的 bug、測試涵蓋、風險、可讀性。條列重點與建議。
3. 以「comment」模式張貼(不要 approve、不要 request-changes):
     gh pr review "{{.URL}}" --comment --body "<你的審查內容,Markdown>"
   審查結尾註明「由自動化 agent 產生,僅供參考」。

## 任務 B:把這個 PR 鏡像成內網 GitLab MR
使用穩定分支名 ` + "`{{.MirrorBranch}}`" + `(讓後續 merge 同步能對應到同一個 MR)。
1. 取得 PR 的 head 內容(來源分支 {{.PR.HeadRef}},head repo: {{.PR.HeadRepoURL}}),
   例如在一個乾淨工作目錄:
     gh pr checkout {{.PR.Number}} --repo {{.Repo}}   # 或 git fetch head repo 的 {{.PR.HeadRef}}
     git branch -f {{.MirrorBranch}} HEAD
2. 推送到內網 GitLab mirror repo(用 token 認證,勿把 token 寫進設定或 log):
     git remote add gitlab "$(printf '%s' '{{.MirrorRepoURL}}' | sed -E 's#^https://#https://oauth2:'"$GITLAB_TOKEN"'@#')" 2>/dev/null || true
     git push -f gitlab {{.MirrorBranch}}
3. 用 glab 建立對應 MR(target 分支 {{.TargetBranch}});若同來源分支的 MR 已存在則略過建立:
     glab mr create --repo "{{.PRProject}}" \
       --source-branch "{{.MirrorBranch}}" --target-branch "{{.TargetBranch}}" \
       --title "[mirror] {{.Title}}" \
       --description "鏡像自 GitHub PR {{.URL}}(#{{.PR.Number}})。含上方審查摘要。由自動化 agent 建立。" \
       --yes
   (先用 ` + "`glab mr list --repo \"{{.PRProject}}\" --source-branch \"{{.MirrorBranch}}\"`" + ` 檢查是否已存在。)

## 輸出(最後)
   REVIEW_POSTED: yes|no
   MR_URL: <GitLab MR 網址,若有>
   SUMMARY: <一句話總結審查與鏡像結果>
   任一步驟失敗輸出 ERROR: <原因>,不要重試超過一次。

## PR 資訊
- repo: {{.Repo}}  PR: #{{.PR.Number}}  連結: {{.URL}}
- head(來源): {{.PR.HeadRef}} @ {{.PR.HeadSHA}}  ->  base(目標): {{.PR.BaseRef}}
- head repo: {{.PR.HeadRepoURL}}
- 標題: {{.Title}}
- 說明:
{{if .Summary}}{{.Summary}}{{else}}(無){{end}}
`

// prMergeTmpl: the GitHub PR was merged; merge the mirrored GitLab MR.
const prMergeTmpl = `你是一個 PR 合併同步 agent。GitHub PR #{{.PR.Number}}({{.URL}})已被 **merge**
(merge commit {{.PR.MergeSHA}})。請把這個合併同步到內網 GitLab。
環境已設定 GH_TOKEN、GITLAB_HOST / GITLAB_TOKEN,git 可用。鏡像分支名為 ` + "`{{.MirrorBranch}}`" + `。

## 任務
1. 確保 GitLab mirror 的來源分支是最新(把合併後的 head 內容推到 mirror repo):
     gh pr checkout {{.PR.Number}} --repo {{.Repo}}
     git branch -f {{.MirrorBranch}} HEAD
     git remote add gitlab "$(printf '%s' '{{.MirrorRepoURL}}' | sed -E 's#^https://#https://oauth2:'"$GITLAB_TOKEN"'@#')" 2>/dev/null || true
     git push -f gitlab {{.MirrorBranch}}
2. 找到對應的 GitLab MR 並合併(來源分支 {{.MirrorBranch}},目標 {{.TargetBranch}}):
     glab mr list --repo "{{.PRProject}}" --source-branch "{{.MirrorBranch}}"
     glab mr merge <iid> --repo "{{.PRProject}}" --yes
   若對應 MR 不存在,先用 glab mr create 建立(source {{.MirrorBranch}} / target {{.TargetBranch}})再合併。
   若 GitLab 端因衝突無法自動合併,不要強推,改為輸出 ERROR 說明。

## 輸出(最後)
   MR_URL: <被合併的 GitLab MR 網址>
   SUMMARY: <一句話總結同步結果>
   失敗輸出 ERROR: <原因>,不要重試超過一次。
`
