// Package prompt renders the instruction sent to Codex. Codex is expected to run
// in a non-interactive session with the `glab` CLI available and GITLAB_HOST /
// GITLAB_TOKEN set in its environment.
package prompt

import (
	"bytes"
	"sort"
	"text/template"

	"github.com/qwe7002-ai/ci-webhook-codex-agent/internal/github"
)

type view struct {
	github.Incident
	GitLabProject string
	ExtraLines    []string
}

// The template is Traditional-Chinese-facing (matching the operators) but the
// glab command and label names stay in ASCII for tooling compatibility.
var tmpl = template.Must(template.New("triage").Parse(`你是一個 CI / 研發事故的初步分診 (triage) agent。以下是一則來自 GitHub 的 webhook 事件。

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

   issue 內文請使用以下 Markdown 結構:
   - 一段「初步評判」表格或條列(category / severity / priority / likely-cause / impact / confidence)
   - 一段「下一步建議」
   - 一段「原始事件」:附上 GitHub 連結與關鍵欄位
   - 結尾註明:此 issue 由自動化 triage agent 建立,評判僅供參考,需人工確認。

3. 建立成功後,輸出兩行:
   ISSUE_URL: <glab 回傳的 issue 網址>
   SUMMARY: <一句話總結你的評判>

若建立失敗,輸出 ERROR: <原因>,不要重試超過一次。

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
`))

// Render builds the Codex prompt for one incident.
func Render(inc github.Incident, gitlabProject string) (string, error) {
	extra := make([]string, 0, len(inc.Extra))
	keys := make([]string, 0, len(inc.Extra))
	for k := range inc.Extra {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		extra = append(extra, k+": "+inc.Extra[k])
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, view{Incident: inc, GitLabProject: gitlabProject, ExtraLines: extra}); err != nil {
		return "", err
	}
	return buf.String(), nil
}
