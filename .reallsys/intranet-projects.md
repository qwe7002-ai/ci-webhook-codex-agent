# 內網項目路徑速查(intranet project paths)

本檔用於**快速找到內網 GitLab 項目的路徑**:webhook agent 會把 GitHub 事件鏡像/建立到
內網 GitLab,而各 playbook 用到的目標項目路徑分散在設定裡。這裡把它們集中列出並說明來源。

> 下列為對應設定欄位的**範例值 / 佔位符**(與 `config.example.yaml` 一致),請依實際
> 內網環境替換。真正生效的值以你的 `config.yaml` 為準,**切勿把 token/密鑰寫進本檔**。

## 一覽表

| 用途(playbook) | 內網項目路徑(project path) | 設定欄位 | 說明 |
| --- | --- | --- | --- |
| triage_issue | `team/incidents` | `gitlab.project` | 建立巡檢/告警 issue 的目標項目 |
| pr_review / pr_merge_sync | `team/web-mirror` | `pr.gitlab_project` | 鏡像 MR 所在的目標項目 |
| 鏡像 git push | `https://gitlab.internal.corp/team/web-mirror.git` | `pr.gitlab_repo_url` | 鏡像分支 `gh-pr-<n>` 的推送目標(執行期才注入 token) |
| 內網 GitLab 位址 | `https://gitlab.internal.corp` | `gitlab.host` | glab / git 對內網的 base URL |

「項目路徑(project path)」= GitLab 的 `<group>/<project>`(可含子群組,如 `group/sub/proj`),
即網址 `${gitlab.host}/<group>/<project>` 去掉 host 的部分。

## 從設定檔快速取路徑

```sh
# 目前實際生效的值(讀 config.yaml)
grep -E '^\s*(host|project|gitlab_project|gitlab_repo_url|target_branch):' config.yaml
```

## 用 glab 在內網查/確認路徑

```sh
# 前置:glab 已認證到內網(scripts/setup-glab.sh 會用 GITLAB_HOST / GITLAB_TOKEN 設定)
export GITLAB_HOST GITLAB_TOKEN

# 依關鍵字搜尋內網項目,取得完整 path_with_namespace
glab api "projects?search=mirror&simple=true" --paginate \
  | jq -r '.[] | "\(.path_with_namespace)\t\(.web_url)"'

# 已知路徑時,確認存在並看預設分支(URL 需 encode，斜線用 %2F)
glab api "projects/team%2Fweb-mirror" | jq -r '.path_with_namespace, .default_branch, .web_url'
```

## 與 AGENTS.md 佔位符的對應

`codex/AGENTS.md` 的 playbook 用到這些佔位符,對照如下:

- `<PROJECT>`         → `gitlab.project`(表格第 1 列)
- `<MIRROR_PROJECT>`  → `pr.gitlab_project`(表格第 2 列)
- `<GitLab mirror git URL>` → `pr.gitlab_repo_url`(表格第 3 列)
- `<TARGET>`          → `pr.target_branch`(預設 `main`)
