# ci-webhook-codex-agent

接收 GitHub webhook、交給 **Codex** 做初步評判 (triage)，並透過 **glab** 在
**內網 GitLab** 建立 issue 的自動化 agent。

```
GitHub ──webhook──▶  Go webhook server
                       1. 驗證 HMAC 簽章 (X-Hub-Signature-256)
                       2. 依設定過濾 / 正規化事件
                       3. 立刻回 202,丟進 worker queue
                              │
                              ▼
                       worker pool ──MCP client──▶ codex mcp (stdio JSON-RPC)
                                          │  tools/call codex(prompt=正規化事件)
                                          │  Codex 推理 + 初步評判
                                          ▼
                                       glab CLI ──▶ 內網 GitLab:建立 issue
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
3. **正規化**：把不同事件攤平成統一的 `Incident`（repo / 標題 / 內文 / 連結 /
   觸發者 / 事件專屬欄位），下游一律吃這個結構。
4. **入列**：去重（依 `X-GitHub-Delivery`，防止 GitHub 重送造成重複 issue）後
   丟進 bounded queue，回 `202`。佇列滿了回 `503`，讓 GitHub 之後重送。
5. **評判 + 建立 issue**：worker 把 `Incident` 套進 prompt，透過 MCP 呼叫 Codex
   的 `codex` 工具。Codex 讀事件、產出初步評判（類別 / 嚴重度 / 優先級 / 可能原因 /
   影響 / 下一步），再用 `glab issue create` 在內網 GitLab 開 issue，最後在最終訊息
   印出 `ISSUE_URL:` 與 `SUMMARY:` 兩行讓 server 解析並記錄。執行期間 Codex 串流的
   MCP 通知(進度事件)會被 client 記錄後略過,直到工具回傳最終結果。

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

---

## 專案結構

```
cmd/server/main.go        進入點:載入設定、啟動 worker pool 與 HTTP server、優雅關閉
internal/config           設定載入 (YAML + ${ENV} 展開) 與驗證
internal/github/verify.go webhook 簽章驗證 (HMAC-SHA256)
internal/github/event.go  事件過濾 + 正規化成 Incident
internal/prompt           Codex prompt 模板 (含 glab 指令與評判規格)
internal/mcp              精簡 MCP stdio client (initialize + tools/call)
internal/codex            啟動 codex mcp、注入 GITLAB_* 環境變數、呼叫工具、解析輸出
internal/worker           bounded queue + worker pool + 去重
internal/server           HTTP 路由與 webhook handler
scripts/setup-glab.sh     (選用) 手動驗證 glab 能連到內網 GitLab
```

---

## 設定

複製範本並依需求調整：

```bash
cp config.example.yaml config.yaml
cp .env.example .env        # 填入 secret
```

- 設定檔內的 `${VAR}` 會在載入時從環境變數展開，**secret 只放在環境變數**，
  不落地到設定檔。
- 開關事件：在 `github.events` 底下增減條目、切 `enabled`、調整 `actions` /
  `conclusions`。沒列到或 `enabled: false` 的事件一律忽略。

關鍵環境變數（見 `.env.example`）：

| 變數 | 用途 |
|------|------|
| `GITHUB_WEBHOOK_SECRET` | 驗證 webhook 簽章，需與 GitHub 上設定一致 |
| `GITLAB_HOST` | 內網 GitLab base URL，會注入 Codex 子行程供 glab 使用 |
| `GITLAB_TOKEN` | 具 `api` scope 的 PAT/專案 token，供 glab 建 issue |
| `OPENAI_API_KEY` | （或你的 codex 安裝所需的認證）供 Codex 推理 |

---

## 執行

### 本機

前置：安裝 [`codex`](https://github.com/openai/codex) 與
[`glab`](https://gitlab.com/gitlab-org/cli)，並確認機器能連到內網 GitLab。

```bash
make build
set -a; source .env; set +a
./bin/server -config config.yaml
```

在 GitHub repo → Settings → Webhooks 新增：
- Payload URL：`https://<你的服務>/webhook`
- Content type：`application/json`
- Secret：與 `GITHUB_WEBHOOK_SECRET` 相同
- Events：依需求勾選（例如 Workflow runs / Issues）

### Docker

`Dockerfile` 會把 Go server、`codex`（npm）與 `glab`（GitLab release）打進同一
個 image。

```bash
docker compose up --build
```

> 若內網 GitLab 只在私有網路，請在 `docker-compose.yml` 把 agent 接到那個
> network（範例已保留註解）。

---

## 安全性

- **簽章必驗**：所有 webhook 都先驗 `X-Hub-Signature-256` 才處理內容；raw body
  在任何再編碼前先驗，用 constant-time 比較。
- **Secret 不落地**：token 走環境變數，設定檔只留 `${VAR}` 佔位。
- **Codex sandbox**：Codex 需要能跑 shell（`glab`）並連到內網 GitLab，範例把
  `codex.mcp.arguments` 設為 `sandbox: danger-full-access` + `approval-policy:
  never`（透過 MCP 工具參數傳入）。這代表 Codex 在該容器內可執行任意指令，請務必：
  - 跑在**隔離、最小權限**的容器/主機，只給它到 GitLab 與 model API 的網路；
  - `GITLAB_TOKEN` 只給必要的專案與 `api` scope；
  - 有需要可改成 `workspace-write` + 限制網路，並在 prompt 白名單化只允許 `glab`。
- **MCP 交握**：本 agent 不實作 server→client 的請求（sampling / elicitation
  等),收到就回 `method not supported` 拒絕。搭配 `approval-policy: never`,正常
  流程不會走到這條路;若你改了 approval 政策而 Codex 需要人工核准,工具呼叫會因為
  被拒絕而失敗——這是刻意的(自動化流程不該卡在互動核准)。
- **工具參數相容性**:`codex` 工具的參數名稱(如 `sandbox` / `approval-policy`)
  會隨 Codex 版本的 tool schema 變動。它們放在 `config.yaml` 的
  `codex.mcp.arguments`,可直接改設定對齊你的版本,不需動程式。
- **外部輸入**：payload 內文（issue/PR/commit 訊息）屬不可信輸入，會被放進
  prompt。Codex 被指示只做「建立 issue」這一件事並最多重試一次；請勿放寬到讓它
  執行 payload 內文中要求的其他動作。
- **去重**：以 `X-GitHub-Delivery` 去重，避免 GitHub 重送造成重複 issue（目前為
  記憶體內、近 1024 筆的視窗；需跨重啟持久化可再接 Redis 等）。

---

## 開發

```bash
make test    # 單元測試 (簽章驗證 + 事件過濾/正規化 + MCP client 交握/工具呼叫)
make vet
```

擴充新的 GitHub 事件：在 `internal/github/event.go` 的 `Evaluate` switch 增加
一個 `enrich<Event>` 函式填 `Incident` 欄位，並在 `config.yaml` 開啟該事件即可，
prompt 與後續流程不需更動。
```
