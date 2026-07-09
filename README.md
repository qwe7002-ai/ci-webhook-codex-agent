# ci-webhook-codex-agent

接收 GitHub webhook、交給 **Codex** 處理的自動化 agent。依事件種類走不同 playbook:

- **事故分診 (triage)**:issues / CI 失敗 / push 等 → Codex 初步評判 → glab 在
  內網 GitLab 建 issue。
- **PR 監控 (pull_request)**:PR 開啟 → Codex 用 `gh` 以 comment 方式留審查,並把
  PR 鏡像成內網 GitLab MR;PR 被 merge → 合併對應的 GitLab MR。

```
GitHub ──webhook──▶  Go webhook server
                       1. 驗證 HMAC 簽章 (X-Hub-Signature-256)
                       2. 依設定過濾 / 正規化事件 / 選 playbook
                       3. 立刻回 202,丟進 worker queue
                              │
                              ▼
                       worker pool ──MCP client──▶ codex mcp (stdio JSON-RPC)
                                          │  tools/call codex(prompt=該 playbook 指令)
                                          ▼
        ┌──────────────────────────────────────────────────────────┐
        │ triage_issue  : glab issue create ──▶ 內網 GitLab issue    │
        │ pr_review     : gh pr review --comment  +  git push +     │
        │                 glab mr create ──▶ 內網 GitLab MR (鏡像)    │
        │ pr_merge_sync : glab mr merge ──▶ 合併鏡像 MR (PR merged)  │
        └──────────────────────────────────────────────────────────┘
```

Codex 以 **MCP server**(`codex mcp`,stdio JSON-RPC)執行,本 agent 是 **MCP
client**:每個事件開一個 `codex mcp` 子行程,完成 `initialize` 交握後呼叫
`tools/call` 的 `codex` 工具,把 prompt 當參數送進去,再解析工具回傳的最終訊息。

為什麼要「立刻回 202 再非同步處理」:GitHub 對 webhook 回應有約 10 秒的逾時，
而一次 Codex 推理可能要數十秒到數分鐘。因此 server 只做「驗證 + 入列」，實際
的評判與建立 issue 都在背景 worker 執行。

---

## 運作流程

1. **驗證**：對 raw body 以 webhook secret 做 HMAC-SHA256，比對
   `X-Hub-Signature-256`（constant-time 比較）。失敗回 `401`。
2. **過濾**：依 `config.yaml` 的 `github.events` 判斷這個事件要不要處理。
   - 用 `X-GitHub-Event` 對應事件種類（`issues` / `pull_request` / `push` /
     `workflow_run` / `check_run` …）。
   - `actions` 過濾 payload 的 `action`；`conclusions` 過濾 CI 的結論
     （把泛用的 CI webhook 收斂成「只對失敗反應」）。
3. **正規化 + 選 playbook**：把不同事件攤平成統一的 `Incident`（repo / 標題 /
   內文 / 連結 / 觸發者 / 事件專屬欄位），並依事件決定 playbook:
   - `pull_request` opened/reopened → `pr_review`
   - `pull_request` closed 且 `merged=true` → `pr_merge_sync`(其餘 closed 略過)
   - 其他事件 → `triage_issue`(預設)
4. **入列**：去重（依 `X-GitHub-Delivery`，防止 GitHub 重送造成重複動作）後
   丟進 bounded queue，回 `202`。佇列滿了回 `503`，讓 GitHub 之後重送。
5. **執行 playbook**：worker 把 `Incident` 依 playbook 套進對應 prompt，透過 MCP
   呼叫 Codex 的 `codex` 工具。Codex 用 glab / gh / git 完成動作,最後在最終訊息印出
   結果行(`ISSUE_URL:` / `MR_URL:` / `SUMMARY:`,失敗則 `ERROR:`)讓 server 解析並
   記錄。執行期間 Codex 串流的 MCP 通知(進度事件)會被 client 記錄後略過,直到工具
   回傳最終結果。

### 初步評判 (preliminary evaluation) 內容

Codex 對每個事件至少輸出：

| 欄位 | 說明 |
|------|------|
| category | bug / ci-failure / feature / question / security / infra / flaky-test |
| severity | S1(critical) ~ S4(low) |
| priority | P0 ~ P3 |
| likely-cause | 初步推測的原因 |
| impact | 影響範圍 |
| next-steps | 2~4 點後續建議 |
| confidence | low / medium / high（初步評判允許不確定） |

issue 內文會標註「由自動化 triage agent 建立，評判僅供參考，需人工確認」。

### PR 監控 (pull_request)

啟用 `github.events.pull_request` 後,PR 事件走專屬 playbook。鏡像目標(GitLab 專案、
git 網址、MR 目標分支)全部**依來源 repo 從 `projects.toml` 解析**,不在 config 裡設:

- **`pr_review`(opened / reopened)**
  1. Codex 用 `gh pr view` / `gh pr diff` 取得 PR 內容與 diff。
  2. 做初步程式碼審查,並以 **comment** 模式張貼回 GitHub PR:
     `gh pr review <url> --comment --body ...`(不 approve、不 request-changes)。
  3. 把 PR 鏡像成內網 GitLab MR:以穩定分支名 `gh-pr-<number>` 把 head 內容
     `git push` 到鏡像 git 網址,再用 `glab mr create` 在對應 GitLab 專案
     建立/更新 MR(target 分支:拿 PR 的 base 分支查 `projects.toml` 的分支對照,
     沒列到就同名)。
- **`pr_merge_sync`(closed 且 merged)**
  - 把合併後的內容推到鏡像分支,並用 `glab mr merge` 合併對應的 GitLab MR。
    GitLab 端若有衝突不會強推,改回報 `ERROR`。

> 分支名 `gh-pr-<number>` 是刻意固定的:review 時建立、merge 時據此找到同一個 MR。
> Review 模式固定為 comment(寫死在 AGENTS.md 安全規則);gh / glab 各自用**自己的**登入
> 憑證(`gh auth login` / `glab auth login`),本服務不管理任何 token;git push 到 GitLab
> 走 glab 的 git credential helper,憑證不進 URL、不落地。

---

## 專案結構

```
cmd/server/main.go        進入點 (urfave/cli):預設啟動 server;setup-webhook 子指令註冊 webhook
internal/config           設定載入 (YAML + ${ENV} 展開)、驗證,以及 webhook secret 自動產生/持久化
internal/webhook          用 gh 把 webhook 註冊到 repo(冪等),沿用自動管理的 secret
internal/github/verify.go webhook 簽章驗證 (HMAC-SHA256)
internal/github/event.go  事件過濾 + 正規化成 Incident + 選 playbook
internal/prompt           各 playbook 的 Codex prompt 模板 (triage / pr_review / pr_merge)
internal/mcp              精簡 MCP stdio client (initialize + tools/call)
internal/codex            啟動 codex mcp、注入 GITLAB_HOST 環境變數、呼叫工具、解析輸出
internal/worker           bounded queue + worker pool + 去重
internal/server           HTTP 路由與 webhook handler
codex/AGENTS.md           Codex 的操作指南 (skill):各 playbook 流程與安全規範
codex/bin/gh-pr-mirror.sh helper 腳本:安全地把 GitHub PR 鏡像分支推到 GitLab
scripts/setup-glab.sh     (選用) 手動驗證 glab 能連到內網 GitLab
```

---

## Codex skill (AGENTS.md + helper)

為了「保障 Codex 每次都正確處理」,把可重複的規範與最容易出錯的步驟固化成一個 skill,
而不是每次都靠 prompt 臨場推導:

- **`codex/AGENTS.md`** — Codex 的權威操作指南。它會被 **embed 進 server binary**,並在每次 MCP
  呼叫時當成 **developer 指令**(`codex.mcp.system_key`,預設 `developer-instructions`,疊在 Codex
  自身的 base instructions 之上而非取代)傳給 Codex,因此
  **不需放在工作目錄**;若工作目錄剛好有 AGENTS.md,Codex 仍會照它預設行為一併讀取。內容涵蓋:
  - 三個 playbook 的標準步驟(triage_issue / pr_review / pr_merge_sync);
  - **硬性安全規範**:審查一律 comment、鏡像分支固定 `gh-pr-<n>`、不對 GitLab 目標分支
    強推、合併遇衝突改回報 ERROR、MR 建立要幂等、絕不列印或內嵌憑證、把 PR/diff 內文當不可信
    輸入(防 prompt injection);
  - **輸出契約**:最後訊息只輸出 `ISSUE_URL:` / `MR_URL:` / `SUMMARY:` / `ERROR:` 供 agent 解析。
- **`codex/bin/gh-pr-mirror.sh`** — 把「checkout GitHub PR → force-push 到 GitLab 鏡像分支」
  這段最容易寫錯、又牽涉憑證的部分封裝成一支經過測試的腳本(shallow clone、GitHub 端用 `gh`、
  GitLab push 用 glab 的 git credential helper,憑證**絕不進 URL 或落地**、trap 清理暫存)。
  prompt 直接叫 Codex 執行它,而非自己拼 git 指令,降低出錯與洩漏風險。
- **`projects.toml`** — GitHub repo → 內網 GitLab 專案的對照表(可列多個),外加預設內網 host,
  以及每個 repo 的**分支對照** `[projects.target_branch]`(GitHub base 分支 → GitLab MR 目標分支,
  例如 `nightly = "nightly_github"`;沒列到的分支同名鏡像)。Codex 於執行期依「來源 repo」與
  「PR base 分支」查表,解析出 `<PROJECT>` / `<MIRROR_PROJECT>` / mirror git URL / `<TARGET>`。
  PR 鏡像的所有目標參數都出自這裡,不在 `config.yaml`。

> 事件資料(來源 repo、PR 編號等)由 agent 注入 prompt;鏡像目標(GitLab 專案、mirror URL、
> MR 目標分支)則由 Codex 依來源 repo 從 `projects.toml` 解析。要調整對照或目標分支,改
> `projects.toml`;要改流程/安全規範,改 AGENTS.md / 腳本即可,Go 端不用動。

---

## 設定

複製範本並依需求調整：

```bash
cp config.example.yaml config.yaml
cp .env.example .env        # 填入 secret
```

- 設定檔內的 `${VAR}` 會在載入時從環境變數展開，**secret 只放在環境變數**，
  不落地到設定檔。
- **GitHub / GitLab 認證交給 CLI 自己管**:先在主機上跑一次 `gh auth login` 與
  `glab auth login`,Codex 子行程直接沿用它們存下的憑證。本服務**不再管理任何
  GitHub/GitLab token**,設定檔與環境變數裡也沒有。
- 開關事件：在 `github.events` 底下增減條目、切 `enabled`、調整 `actions` /
  `conclusions`。沒列到或 `enabled: false` 的事件一律忽略。

關鍵環境變數（見 `.env.example`）：

| 變數 | 用途 |
|------|------|
| `GITHUB_WEBHOOK_SECRET` | 驗證 webhook 簽章用的密鑰。**可留空**:留空時服務會自動生成並存在 `<codex.workdir>/.webhook-secret`,只有想指定特定值時才需要設 |
| `PUBLIC_URL` | 本服務對外可達的 base URL(scheme + host,例如 `https://ci.example.com`),供 `setup-webhook` 當 webhook 目標(會自動接上 `server.path`) |
| `CI_WEBHOOK_CONFIG` | (選用)config 檔路徑,等同 `--config`;設了就不必每次帶 |
| `GITHUB_REPO` | (選用)`setup-webhook` 的目標 `owner/repo`,等同 `--repo` |
| `GITLAB_HOST` | 內網 GitLab base URL，會注入 Codex 子行程,讓 glab 指向正確的 instance |
| `OPENAI_API_KEY` | （或你的 codex 安裝所需的認證）供 Codex 推理 |

> gh / glab 的登入憑證**不在**上表:它們由 `gh auth login` / `glab auth login`
> 存進各自的 CLI 設定(`~/.config/gh`、`~/.config/glab-cli`),不經過本服務。

### Webhook secret 自動管理

`GITHUB_WEBHOOK_SECRET` 留空時,服務在載入設定時會自動產生一把隨機密鑰(32 bytes,
hex),寫進 `<codex.workdir>/.webhook-secret`(權限 `0600`),之後每次啟動都沿用同一把。
你不必自己想、也不必手動同步——用內建指令把它註冊到 GitHub 即可(見下)。要指定特定值
(例如對接一個手動建立的 webhook)才需設 `GITHUB_WEBHOOK_SECRET`,此時以環境變數為準。

---

## 執行

### 本機

前置：安裝 [`codex`](https://github.com/openai/codex)、
[`glab`](https://gitlab.com/gitlab-org/cli)、[`gh`](https://cli.github.com/) 與
`git`，並確認機器能連到內網 GitLab(以及 GitHub,供 PR 鏡像)。接著登入兩個 CLI
(只需一次,憑證存在各自的設定,本服務會沿用):

```bash
gh auth login                                   # GitHub CLI
glab auth login --hostname gitlab.internal.corp # 內網 GitLab CLI(用你的 GITLAB_HOST)

make build
set -a; source .env; set +a
# AGENTS.md 已 embed 進 binary 並當 system 指令傳入,workdir 不必放它。
# 讓 helper 上 PATH,並把 workdir 指到含 projects.toml 的目錄(本機可用 ./.reallsys)。
export PATH="$PWD/codex/bin:$PATH"    # gh-pr-mirror.sh
# 並在 config.yaml 設 codex.workdir: ./.reallsys(讓 Codex 讀得到 projects.toml)
./bin/server                          # 預設讀 ./config.yaml
```

> CLI 用 [urfave/cli](https://github.com/urfave/cli) 建構。`--config`(別名 `-c`)預設
> `config.yaml`,也可用環境變數 `CI_WEBHOOK_CONFIG` 指定,所以平常不必每次都帶 `--config`。
> 直接跑 `./bin/server` 啟動服務;`./bin/server setup-webhook ...` 註冊 webhook;
> `./bin/server --help` 看全部指令。

**註冊 webhook(建議用內建指令)**:服務會用已登入的 `gh` 把 webhook 建到 repo 上,
自動帶上正確的 payload URL、content type、要訂閱的事件(即 `config.yaml` 裡 `enabled`
的那些),以及上面那把自動管理的 secret。同一把 secret 兩邊自動對齊,不需手動填:

webhook 的目標網址從 `server.public_url`(即 `${PUBLIC_URL}`)+ `server.path` 組出來,
`--repo` 也吃 `GITHUB_REPO` 環境變數,所以設好環境變數後指令可以精簡到:

```bash
./bin/server setup-webhook --repo qwe7002-ai/ci-webhook-codex-agent
# 若已設 GITHUB_REPO,連 --repo 都免:  ./bin/server setup-webhook
```

- 冪等:同一個 URL 已有 webhook 就**就地更新**,不會重複建立。
- 需要 `gh` 對該 repo 有 admin 權限(建立 webhook 的權限)。
- 事後改了 `github.events`,再跑一次同樣指令即可把訂閱事件同步過去。
- 想臨時指定別的網址,加 `--webhook-url https://.../webhook` 覆蓋 `public_url`。

> 想手動在 GitHub UI 建也可以(repo → Settings → Webhooks):Payload URL 填
> `${PUBLIC_URL}` + `server.path`、Content type 選 `application/json`、Secret 填
> `<codex.workdir>/.webhook-secret` 的內容(或你自訂的 `GITHUB_WEBHOOK_SECRET`)、
> Events 勾選對應項目。

### Docker

`Dockerfile` 會把 Go server、`codex`（npm）與 `glab`（GitLab release）打進同一
個 image。

```bash
docker compose up --build
```

> 若內網 GitLab 只在私有網路，請在 `docker-compose.yml` 把 agent 接到那個
> network（範例已保留註解）。

### apt / .deb 安裝

`release-deb` workflow 會建置 `.deb`(amd64 / arm64)並發佈成 apt repo 到
**GitHub Pages**。安裝後會帶一個 systemd service、預設設定於
`/etc/ci-webhook-codex-agent/`、`projects.toml` 於 `/var/lib/ci-webhook-codex-agent`、
helper 於 `/usr/bin`(AGENTS.md 已 embed 進 binary,不另外安裝)。

**發佈(維護者)**:
1. 一次性:repo → Settings → Pages → Source 選 **GitHub Actions**。
2. 打版本 tag 觸發:`git tag v0.1.0 && git push origin v0.1.0`(或手動跑
   `release-deb` 並填版本)。完成後開 `https://qwe7002-ai.github.io/ci-webhook-codex-agent/`
   會有安裝說明頁。

**安裝(使用者)**:
```bash
# 方式 A:加入 apt repo(可 apt upgrade)
echo "deb [trusted=yes] https://qwe7002-ai.github.io/ci-webhook-codex-agent/ ./" \
  | sudo tee /etc/apt/sources.list.d/ci-webhook-codex-agent.list
sudo apt-get update
sudo apt-get install ci-webhook-codex-agent

# 方式 B:直接裝單一 .deb
curl -fsSLO https://qwe7002-ai.github.io/ci-webhook-codex-agent/pool/ci-webhook-codex-agent_0.1.0_amd64.deb
sudo apt-get install ./ci-webhook-codex-agent_0.1.0_amd64.deb
```
裝完:
```bash
sudo nano /etc/ci-webhook-codex-agent/config.yaml   # events / gitlab / pr
sudo nano /etc/ci-webhook-codex-agent/agent.env     # secrets
sudo systemctl start ci-webhook-codex-agent
sudo systemctl status ci-webhook-codex-agent
```
> `.deb` 只含 server 本體 + skill + service;`codex` / `glab` / `gh` / `git`
> 需另外安裝(不在 apt 官方源)。apt line 用 `[trusted=yes]`(repo 未 GPG 簽章);
> 要簽章的話在 workflow 加 GPG 步驟並用 `signed-by` 取代。

---

## 安全性

- **簽章必驗**：所有 webhook 都先驗 `X-Hub-Signature-256` 才處理內容；raw body
  在任何再編碼前先驗，用 constant-time 比較。
- **Secret 管理**：設定檔只留 `${VAR}` 佔位。本服務自管的唯一 secret 是 webhook 簽章密鑰:
  由環境變數 `GITHUB_WEBHOOK_SECRET` 提供,或留空由服務自動產生並存到 `<codex.workdir>/.webhook-secret`
  (權限 `0600`,已列入 `.gitignore`,請確保該目錄權限受控)。GitHub / GitLab 憑證則交給
  `gh` / `glab` 各自保管,不經過本服務的設定檔或環境變數。
- **Codex sandbox**：Codex 需要能跑 shell（`glab` / `gh` / `git`）並連到內網
  GitLab 與 GitHub，範例把 `codex.mcp.arguments` 設為 `sandbox:
  danger-full-access` + `approval-policy: never`（透過 MCP 工具參數傳入）。這代表
  Codex 在該容器內可執行任意指令，請務必：
  - 跑在**隔離、最小權限**的容器/主機，只給它到 GitLab / GitHub / model API 的網路；
  - `gh` / `glab` 登入時用**最小權限**憑證:glab 只給必要專案與 `api` scope、
    gh 只給必要 repo 的最小 scope(PR review + 讀取);
  - 有需要可改成 `workspace-write` + 限制網路。
- **PR 鏡像**：`pr_review` / `pr_merge_sync` 會 `git push` 到內網 GitLab 並可能
  `glab mr merge`。請確認 mirror 專案是**專用鏡像 repo**(而非正式主幹),並讓 glab 用最小權限
  憑證登入,避免自動化誤動到生產分支。merge sync 遇 GitLab 端衝突時刻意不強推、改回報錯。
- **MCP 交握**：本 agent 不實作 server→client 的請求（sampling / elicitation
  等),收到就回 `method not supported` 拒絕。搭配 `approval-policy: never`,正常
  流程不會走到這條路;若你改了 approval 政策而 Codex 需要人工核准,工具呼叫會因為
  被拒絕而失敗——這是刻意的(自動化流程不該卡在互動核准)。
- **工具參數相容性**:`codex` 工具的參數名稱(如 `sandbox` / `approval-policy`)
  會隨 Codex 版本的 tool schema 變動。它們放在 `config.yaml` 的
  `codex.mcp.arguments`,可直接改設定對齊你的版本,不需動程式。
- **外部輸入**：payload 內文（issue/PR/commit 訊息、PR diff）屬不可信輸入，會被放進
  prompt。各 playbook 都把 Codex 限定在該做的動作(建 issue / 留 comment 審查 + 建 MR /
  合併 MR)並最多重試一次；請勿放寬到讓它執行 payload 或 diff 內文中要求的其他動作
  (prompt injection)。
- **去重**：以 `X-GitHub-Delivery` 去重，避免 GitHub 重送造成重複動作（目前為
  記憶體內、近 1024 筆的視窗；需跨重啟持久化可再接 Redis 等）。

---

## 開發

```bash
make test    # 簽章驗證 + 事件過濾/正規化/選 playbook + prompt 模板 + MCP client 交握/工具呼叫
make vet
```

擴充新的 GitHub 事件：在 `internal/github/event.go` 的 `Evaluate` switch 增加
一個 `enrich<Event>` 函式填 `Incident` 欄位，並在 `config.yaml` 開啟該事件即可，
prompt 與後續流程不需更動。
```
