# lumen

`lumen` 是 coding agent 的 standalone terminal frontend，backend 可插拔：預設透過
ACP (Agent Client Protocol) 接 `claude`，也可以切到 `gemini` 或原生的 `codex`
app-server。frontend 負責 full-screen app shell、approval、session 與 transcript；model、
sandbox、skills、MCP 和認證仍由各 backend 自己管理。frontend 只會對自己啟動的
process 套用 warning suppression；`CODEX_HOME/config.toml` 與 `~/.claude/settings.json`
不會被改動。

> **Breaking change**：預設 backend 由 `codex` 改為 `claude`。需要維持舊行為時，設
> `LUMEN_BACKEND=codex` 或在 `backends.json` 設 `"default": "codex"`。

## 安裝

在 repo 根目錄執行：

```bash
bash install.sh
```

`install.sh` 會直接使用目前 checkout 的 source 建置，包含尚未 commit 的修改；它不會
自行 checkout 或安裝指定 tag。要安裝某個 tag，請先在乾淨的 checkout 切到該 tag，再
執行 `bash install.sh`。

需要 Go，以及你要使用的 backend 對應的 CLI：

| backend | 需要安裝 |
| --- | --- |
| `claude`（預設） | `npm i -g @agentclientprotocol/claude-agent-acp@0.90.0`，並完成 Claude 認證 |
| `gemini` | `gemini` CLI（以 `gemini --acp` 啟動） |
| `codex` | 官方 `codex`；只有使用 codex backend 時才需要 |

Lumen 不會自動下載或用 `npx` 執行 adapter；找不到 `claude-agent-acp` 時，會以 exit 1
結束並印出上面的安裝指令。若 `codex` 不在 `PATH`，可以另外指定：

```bash
LUMEN_CODEX_BIN=/path/to/codex bash install.sh
```

## 使用

```bash
lumen
lumen --continue
lumen --resume THREAD_ID
lumen "用一句話說明目前 repo 狀態"
lumen --minimal
lumen --backend gemini
LUMEN_BACKEND=codex lumen

# direct access to the official Codex CLI
lumen exec --help
lumen codex mcp list
```

在 interactive TTY 中，預設會進入 alternate screen。上方是 branch、cwd、thread
metadata 與 context usage，中間是可捲動的 conversation workspace；輸入框固定在
transcript 下方；最底下是獨立的 `coralline` dock：若本機有 renderer，第一列顯示 Coralline
statusline，第二列顯示 Lumen 的 turn state、attention、queue、connection 與 shortcuts。
Dock 不會重複 user、assistant 或 tool transcript；離開後，已完成的內容不會留在 shell
scrollback 裡。

Lumen 會使用 [Nanako0129/coralline](https://github.com/Nanako0129/coralline) 的
statusline renderer contract。若存在 `~/.claude/coralline/statusline.sh`，就把目前的
cwd、model、effort 與 context usage 以 stdin JSON 傳入，將 renderer 的 ANSI output
投影到 dock；Lumen 不會複製 renderer source，也不會改寫 `~/.claude/settings.json` 或
`coralline.conf`。找不到 renderer、依賴缺失或 renderer timeout 時，保留 native dock。
可用 `LUMEN_CORALLINE_STATUSLINE=/path/to/statusline.sh` 指定路徑，
或設為 `off` 停用。

每個 `you`、`assistant`、`tool`、`plan` 和 `status` content block 都有獨立的 inline
label；文字 label 會直接標出來源，user / assistant 也會用 cyan / green 區分。外層
turn card 保留邊界，hover 或 selected 時才反白目前 row 並顯示 detail；自然語句維持
原本的 Markdown 與換行，不會硬切成單句。

app-server 發生 EOF 或 transport error 時，fullscreen session 會保留 transcript 和
composer，顯示 `reconnecting` 並以 bounded backoff 重啟 app-server；已有內容會 resume
原 thread，並把 replay history 去重接回現有 turn。尚未送出 turn 的空白 thread 則在同一個
cwd 建立可繼續使用的新 thread。

新的 thread 還沒送出 prompt 時，中間會顯示 compact welcome card；輸入第一個
prompt 或執行 `/help` 後，就會回到一般 transcript。`--minimal` 仍維持原本的 inline
scrollback 行為。

`--minimal` 和 `--no-alt-screen` 會回到 inline scrollback mode；需要時也可以用
`--fullscreen` 明確指定 full-screen mode。

- `Enter` 送出 prompt
- `Alt+Enter` 或 `Shift+Enter` 插入 newline
- `↑` / `↓` 先跳到目前 prompt 的開頭 / 結尾，再進入 history
- `Ctrl+Enter` 在 turn 進行中會 cancel-and-send；一般 `Enter` 則會排隊
- `←` / `→`、`Home` / `End` 與 Backspace 編輯 prompt
- 在 composer 裡 click，可直接把游標放到指定位置
- `Ctrl-C` 第一次 interrupt 目前 turn 並顯示退出確認，2 秒內再按一次才離開
- `Ctrl+U` 開啟跨專案的 resume picker；`↑↓` 選擇、`1–9`/`Enter` 載入舊 session，
  `Esc` 關閉
- 輸入 `exit` 可直接離開，不需要斜線指令
- `Ctrl+X` 開啟 shortcuts overlay，`Esc` 關閉
- 輸入 `/` 會開啟官方 Codex slash-command popup；`↑` / `↓` 選擇、`Tab` 補全、
  `Enter` 執行、`Esc` 收起提示。Popup 會顯示 command description，不會把已開始輸入
  arguments 的內容誤當成 command。
- `Ctrl+\` 或 `/dashboard` 開啟跨專案 session supervisor；`↑↓` 選擇、`1–9`/`Enter`
  resume、`n` 開新 session、`r` refresh。列表會顯示 session 的 cwd。
- `PageUp` / `PageDown` 翻閱 transcript；新輸出會回到底部，滾到頂端或底端後不再
  移動
- alternate screen 支援 mouse wheel；滑動時會停在歷史位置
- transcript 可用滑鼠拖曳反白，放開後複製到系統 clipboard
- click turn card 內容會把 composer 指向該 turn 追問；card 內的 `[ask]` 也可使用
- click card 內的 `[fork]` 會從該 turn 建立 branch，保留該 turn 並捨棄後續 turns
- hover transcript 裡的 event、tool、plan 或 status row，footer 會顯示 compact detail
- tool use 在完成後預設隱藏 command、output 與 completion status；同一段 tool
  activity 只留一行 muted marker，`Ctrl+O` 展開 / 收合
- reasoning 只顯示一次 animated thinking 狀態；成功 `completed exit 0` 不顯示
- assistant Markdown 支援 headings、lists、quotes、inline code 與 fenced code
- `mermaid` fenced block 會轉成安全的 terminal flowchart preview，內容不會被執行
- `/help` 會顯示與官方 Codex stable TUI 對齊的 command catalog；`/status`、`/quit`、
  `/dashboard` 是 Lumen 自己的 shell controls，其餘 command 會沿用原生名稱與 alias。
- `/model`、`/permissions`、`/skills`、`/hooks`、`/review`、`/rename`、`/new`、
  `/resume`（不帶 id 開 picker，帶 `THREAD_ID` 直接載入）、`/fork`、`/archive`、
  `/compact`、`/goal`、`/copy`、`/diff`、`/pwd`、
  `/usage`、`/statusline`、`/mcp`、`/apps`、`/plugins`、`/clear`、`/stop` 與
  `/debug-config` 已接到 session-local state、local filesystem 或 app-server；
  `/delete` 必須明確輸入 `/delete confirm`。catalog 內仍有部分官方 UI-only commands，
  Lumen 會顯示明確的未支援提示，不會把它們偷偷送成普通 prompt。
- `lumen <native-subcommand> ...` 會把官方 CLI subcommand、stdin/stdout/stderr 與 exit
  code 原樣交給 `codex`；也可用 `lumen codex ...` 作為明確前綴。`lumen --help`、
  `lumen --version` 仍保留 Lumen 自己的入口資訊。
- Full-screen 底部 status label 固定包含 `coralline`，並繼續顯示 model、effort、
  approval 或 turn state。
- 若本機存在 Coralline Bash renderer，full-screen dock 第一列會顯示它的實際 theme /
  segments；renderer 失敗時回到 native fallback，不影響 session。
- approval request 只會使用 server 明確提供的 choices；不會自動允許
- tool user-input request 會在 composer 裡逐題收集答案；options 可用 `↑/↓` 或 `1–9`
  選取，`isOther` 可直接輸入額外答案；`isSecret` question 只會顯示 masking 字元
- permission request 目前採 fail-closed：回傳空 grant，並在 transcript 裡顯示拒絕

這個 UI 的主軸是 Lumen dark dock：每個 chat turn 都有自己的 card，card 內的 user
input 與 assistant output 仍是獨立 transcript row，只用 user cyan / assistant green
顏色區分；card 內的 event/tool/plan/status 走較輕的 event rail，tool activity 預設收在
可展開的 drawer 裡。`Ctrl+\` 的 Dashboard 留在同一個 pager 內，作為 top-level session
supervisor，不會另開 chat tab。這些
interaction patterns 是從本機 Grok executable 的 `--help` 和靜態 module-string anchors
逆向整理出來的；沒有複製 Grok binary、source 或品牌資產。

目前尚未納入完整 UI parity 的項目：跨 session background task pane、部分 Codex UI-only
slash actions、`/btw` 獨立 aside turn、worktree fork UI、media/voice。

官方 `codex`、`claude`、`gemini` 的命令、設定和 session store 都維持原樣；更新官方 CLI
也不會覆蓋這個 frontend。

## Backend

選擇順序：`--backend` > `LUMEN_BACKEND` > `backends.json` 的 `default` > `claude`。
不認得的名稱、設定檔錯誤、或在非 codex backend 使用 codex 專屬旗標，都會在啟動任何
backend 之前以 exit 2 結束。

| 名稱 | 類型 | 啟動方式 |
| --- | --- | --- |
| `claude` | `acp` | `claude-agent-acp`（預設） |
| `gemini` | `acp` | `gemini --acp` |
| `codex` | `codex` | `codex app-server`；保留 `/fork`、`/goal`、`/review`、`/compact` 與 per-turn `[fork]` |

`--model`、`--reasoning-effort`、`--sandbox`、`--approval-policy`（`--ask-for-approval`）
是 codex 專屬旗標，搭配 `claude` / `gemini` 會明確報錯。`lumen <native-subcommand>` 與
`lumen codex ...` 仍直接交給官方 `codex`。

執行中輸入 `/backend` 會開啟 picker（inline 模式列出清單），`/backend <name>` 直接切換。
切換一律開新 session，不會把 transcript 帶過去；重連中不允許切換。

### `backends.json`

位置：`${XDG_CONFIG_HOME:-~/.config}/lumen/backends.json`（相對的 `XDG_CONFIG_HOME` 視為
無效）。同名 entry 整筆取代內建，新名稱則新增 backend：

```json
{
  "default": "claude",
  "backends": {
    "gemini-pro": { "type": "acp", "command": "gemini", "args": ["--acp"] },
    "codex-work": { "type": "codex" }
  }
}
```

- 只接受 `default` 與 `backends.<name>.{type,command,args}`；`type` 為 `acp` 或 `codex`。
  `acp` 必須有 `command`，`codex` 不接受 `command` / `args`。
- 不支援 `env`：設定檔不能放 environment variable，也不要把 secret 放進 `args`。
  認證由各 CLI 自己的登入機制處理。
- 檔案必須屬於目前使用者，且不可 group- / world-writable（例如 `chmod 600`）；否則拒絕載入。

### Permission 行為

- approval 只使用 agent 明確提供的 option；Lumen 不會自動允許，逾時也不會。
- agent 的 permission mode 不是 `default` 時，status bar 會持續顯示警示，例如
  `permissions: auto (from agent settings)`。Claude adapter 會讀取 `~/.claude/settings.json`
  的 `permissions.defaultMode`，若設成 `auto`，session 一開始就會是該 mode；Lumen 只會
  把 `bypassPermissions` 從可選 mode 移除，不會改寫你的 settings。
- approval card 出現後 400ms 內忽略數字鍵，避免先前的 typeahead 誤選 allow；取消鍵不受限。

### Capability fallback

不同 backend 支援的功能不同，缺少時 Lumen 會明確顯示，而不是默默失敗：

- 沒有 session list 的 backend：dashboard / `Ctrl+U` 顯示
  `listing sessions is not supported by backend X`；沒有 session resume 時 `--resume`、
  `--continue` 與 `/resume` 會報 `backend X cannot ...`。
- `[fork]` 只在 backend 支援從指定 turn 分支時顯示。
- `/help` 與 slash popup 依 backend 即時組成：Lumen 本地指令加上 agent 的
  `availableCommands`；codex 專屬指令在其他 backend 顯示 `/<cmd> is not supported by backend <name>`，
  不會當成 prompt 送出。
- 連線中斷後若無法接回原 session，會顯示 `agent context lost · started a new session`。

## Rollback

只移除 custom frontend：

```bash
bash uninstall.sh
```

這不會移除 `codex`、`claude-agent-acp`、各 backend 設定或 sessions；frontend source、spec 和
installer 都在這個 standalone repo。

入口在 `cmd/lumen/`，frontend implementation 和 tests 在 `internal/lumen/`。

## Protocol boundary

codex backend 直接使用目前安裝的 `codex app-server` protocol；claude / gemini 走 ACP
over stdio。若官方 CLI 更新後出現 protocol 不相容，先保留該 CLI 直接使用，再更新這個
frontend；不要去改官方 binary。設計與驗收見
[`docs/specs/backend-adapter/`](docs/specs/backend-adapter/SPEC.md)。

參考：[Codex App Server](https://learn.chatgpt.com/docs/app-server)、
[Agent Client Protocol](https://agentclientprotocol.com)。
