# lumen

`lumen` 是 Codex 的 standalone terminal frontend。它透過 local
`codex app-server` 保留 Codex 的 model、sandbox、skills、MCP、approval 與
session harness，再把日常操作改成 full-screen app shell。frontend 會對自己啟動的
app-server process 套用 warning suppression，不修改 `CODEX_HOME/config.toml`。

## 安裝

在這個 repo 根目錄執行：

```bash
bash install.sh
```

需要已安裝的官方 `codex` 與 Go。若 `codex` 不在 `PATH`，可以指定：

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
```

預設 interactive TTY 會進入 alternate screen：上方固定顯示 branch、cwd 與
context usage，中間是可捲動的 transcript，下方固定顯示 model、effort、composer、
turn state 與 shortcuts。已完成的內容在離開後不會污染 shell scrollback。

`--minimal` 與 `--no-alt-screen` 會切回 inline scrollback mode；`--fullscreen`
仍可明確指定 full-screen mode。

- `Enter` 送出 prompt
- `Alt+Enter` 插入 newline
- `↑` / `↓` 在多行 prompt 移動，單行 prompt 切換 history
- `Ctrl+Enter` 在 turn 進行中 cancel-and-send；普通 `Enter` 會排隊
- `←` / `→`、`Home` / `End` 與 Backspace 編輯 prompt
- 在 composer 內 click 可直接把游標放到指定位置
- `Ctrl-C` interrupt 目前 turn，再按一次離開
- `Ctrl+X` 開啟 shortcuts overlay，`Esc` 關閉
- `Ctrl+\` 或 `/dashboard` 開啟 session supervisor；`↑↓` 選擇、`1–9`/`Enter`
  attach、`n` 開新 session、`r` refresh
- `PageUp` / `PageDown` 翻閱 transcript；新輸出會回到底部
- alternate screen 接收 mouse wheel；滑動時會暫停在歷史位置
- hover transcript 的 event、tool、plan 或 status row，footer 會顯示 compact detail
- tool output 預設折疊，`Ctrl+O` 展開 / 收合
- assistant Markdown 支援 headings、lists、quotes、inline code 與 fenced code
- `mermaid` fenced block 會轉成安全的 terminal flowchart preview，不執行內容
- `/help`、`/status`、`/quit` 是 frontend commands
- approval request 只在 server 明確提供的 choices 中選擇；沒有自動允許
- tool user-input request 會在 composer 逐題收集答案；`isSecret` question 只顯示
  masking 字元
- permission request 目前採 fail-closed：回傳空 grant，並在 transcript 顯示拒絕

這個 UI 的主軸是 Lumen dark dock：conversation 以 card 呈現，event/tool/plan/status
走較輕的 event rail；tool output 預設是可展開 drawer。`Ctrl+\` 的 Dashboard 是同一個
pager 內的 top-level session supervisor，不會另開 chat tab。這些是從本機 Grok
executable 的 `--help` 與靜態 module-string anchors 逆向出的 interaction patterns；沒有
複製 Grok binary、source 或品牌資產。

目前尚未納入完整 Grok parity 的項目：跨 session background task pane、plan code-review
approval、`/btw` 獨立 aside turn、`/doctor`/`grok wrap`、worktree fork、media/voice。

官方 `codex` 命令、設定與 session store 維持原樣，更新官方 CLI 也不會覆蓋這個
frontend。

## Rollback

只移除 custom frontend：

```bash
bash uninstall.sh
```

這不會移除 `codex`、Codex 設定或 Codex sessions。frontend source、spec 與
installer 都在這個 standalone repo。

## Protocol boundary

frontend 使用目前安裝的 `codex app-server` protocol。若官方 CLI 更新造成
protocol 不相容，保留 `codex` 直接使用，並先更新這個 frontend；不要修改官方
binary。

參考：[Codex App Server](https://learn.chatgpt.com/docs/app-server)。
