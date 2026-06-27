# Shell 修复 & UI 美化设计

日期: 2026-06-27

## 问题

### Shell 不可用
用户在 Web 终端输入任何字符无响应，终端显示 `execvp(3) failed.: No such file or directory`。

**根因（两层）：**
1. Docker 构建时 `curl -fsSL https://claude.ai/install.sh | bash` 管道吞掉退出码，安装失败但构建静默通过。后续构建复用 CACHED 坏缓存，`claude` 二进制从未存在于容器中。
2. PTY 启动 `claude` 失败后终端变死区，无"会话已结束" UI 反馈（spec §8 要求但未实现）。

### 界面不够好看
使用 Claude 配色但视觉粗糙：无顶栏品牌条、指标只有数字无进度条、面板缺阴影层次、按钮无 hover 反馈、终端与面板视觉割裂。

---

## 修复设计

### S1 — Dockerfile 安装验证

**文件:** `Dockerfile`

```dockerfile
RUN curl -fsSL https://claude.ai/install.sh -o /tmp/install-claude.sh \
    && bash /tmp/install-claude.sh \
    && rm /tmp/install-claude.sh \
    && test -x /home/claude/.local/bin/claude \
    || { echo "ERROR: claude install failed"; exit 1; }
```

- curl 先下载到文件再执行，退出码不被管道吞掉
- `test -x` 验证二进制存在且可执行
- 失败时 `exit 1` 中断构建
- 首次部署需 `--no-cache` 重建清除旧坏缓存

### S2 — PATH 兜底 + 降级

**文件:** `server/src/server.js`, `server/src/pty-manager.js`

server.js `buildClaudeEnv`:
- 硬编码 `/home/claude/.local/bin` 为 PATH 前缀，不依赖 `process.env.HOME`
- 硬编码 `HOME: '/home/claude'`
- 启动时 `fs.existsSync` 检查 claude 是否可执行

pty-manager.js:
- 启动前检查 `claude` 是否可执行
- 找不到时降级为 `/bin/bash`，终端打印 `⚠ claude not found, falling back to bash`
- 用户至少能看到容器环境手动排查

### S3 — PTY 退出 UI

**文件:** `web/src/terminal.js`, `web/src/styles.css`

后端需要补充: `server.js` WebSocket `/ws/terminal` 连接处需监听 `pty.onExit` 并向前端发送 `{ type: "pty-exit", exitCode }` JSON 消息。当前后端只注册了 `onExit` callback 但未转发给 WebSocket 客户端。

前端:
- 监听 WebSocket `pty-exit` 事件
- PTY 退出时在终端容器叠加半透明覆盖层:
  - 显示 "会话已结束 (exit code: N)"
  - 「重启」按钮，点击后重新创建 PTY 会话
  - 覆盖层样式: 奶油底 + 赤陶色按钮，与 Claude 风格一致

---

## UI 美化设计

### U1 — CSS 打磨

**顶栏品牌条（固定顶部 48px）:**
- 深色底 `#2B2A27`，左侧 "Claude Code" 标识，右侧会话状态灯（绿=存活，灰=已结束）
- 侧栏折叠按钮

**面板升级:**
- `box-shadow: 0 1px 3px rgba(0,0,0,0.08)` 轻阴影
- 终端面板加标题栏: 深色 `#1E1B16`，左终端图标 + "Terminal"，右重启按钮

**指标区改进:**
- 数字保留，下方加进度条（CPU/内存用赤陶色渐变条）
- 网络 ↑↓ 箭头 + 速率文本

**按钮交互:**
- `transition: all 0.15s`，hover 加深，active 缩小
- 主操作按钮赤陶色实底，次要按钮浅色描边

**终端融合:**
- 终端深色区块与标题栏无缝衔接
- 自定义滚动条（细窄、半透明）

### U3 — xterm 主题

完整 Claude 风格 16 色板:

```
background:    #1E1B16    black:         #1E1B16
foreground:    #F4F1EA    red:           #C96442
cursor:        #D97757    green:         #7A9E7E
cursorAccent:  #1E1B16    yellow:        #D4A856
selection:     rgba(217,119,87,0.3)
                          blue:          #6B8FA3
                          magenta:       #A37E8C
                          cyan:          #7EA3A8
                          white:         #F4F1EA
brightBlack:   #6B6760    brightRed:     #D97757
brightGreen:   #9BBF9F    brightYellow:  #E4C07A
brightBlue:    #8BAFC3    brightMagenta: #BD9EAC
brightCyan:    #9EC3C8    brightWhite:   #FFFFFF
```

- fontFamily: `"JetBrains Mono, ui-monospace, monospace"`
