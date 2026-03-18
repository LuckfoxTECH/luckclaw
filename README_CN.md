<div align="center">
  <img src="assets/logo.png" alt="LuckClaw" width="256">

  <h1>🍀 LuckClaw</h1>
</div>

---

[English](./README.md) | 简体中文

luckclaw 是一个基于 [nanobot](https://github.com/HKUDS/nanobot) 用 Golang 重构的轻量个人 AI 助手，支持多通道接入（Discord、Telegram、Slack 等）、多 LLM 提供商支持、多交互方式、结果标记、Skills 扩展、子 Agent 管理、MCP 服务接入等能力，支持部署到 luckfox 各系列 linux 开发板和 x86\_64 平台电脑。

***

> ⚠️ **警告**：本软件目前处于**开发测试阶段**，安全性无法得到充分保障。**强烈建议不要将本软件部署在生产环境中。**

## 一、概述

### 1.1 运行环境要求

| <br /> | 要求                      |
| ------ | ----------------------- |
| **系统** | Linux                   |
| **架构** | arm32 / arm64 / x86\_64 |
| **内存** | ≥ 32MB                  |
| **存储** | ≥ 30MB                  |

### 1.2 子命令列表

| 命令                   | 说明                                       |
| -------------------- | ---------------------------------------- |
| `luckclaw onboard`   | 初始化配置与工作区                                |
| `luckclaw config`    | 交互式配置向导                                  |
| `luckclaw status`    | 查看状态                                     |
| `luckclaw agent`     | 命令行交互模式（直接调用 Agent）                      |
| `luckclaw tui`       | 终端 TUI 模式                                |
| `luckclaw gateway`   | 多通道网关（Bus、Channels、Cron、Heartbeat、WebUI） |
| `luckclaw models`    | 模型列表                                     |
| `luckclaw cron`      | 定时任务管理                                   |
| `luckclaw channels`  | 通道管理                                     |
| `luckclaw heartbeat` | 心跳管理                                     |
| `luckclaw skills`    | Skills 管理                                |
| `luckclaw clawhub`   | ClawHub 集成 (目前下载会被限制，主要用于提供skills建议)     |

### 1.3 运行模式

| 模式          | 命令                 | 说明                               |
| ----------- | ------------------ | -------------------------------- |
| **Agent**   | `luckclaw agent`   | 命令行交互，直接调用 Agent                 |
| **TUI**     | `luckclaw tui`     | 终端 UI 交互                         |
| **Gateway** | `luckclaw gateway` | 多通道网关：统一消息总线、各通道接入、定时任务、心跳、WebUI |

### 1.4 构建

| 目标                   | 命令                         | 输出               |
| -------------------- | -------------------------- | ---------------- |
| 默认构建                 | `make build`               | `luckclaw`       |
| 精简构建（不含浏览器工具）        | `make build-minimal`       | `luckclaw`       |
| 交叉编译 armv7（Linux）    | `make build-armv7`         | `luckclaw-armv7` |
| 交叉编译 armv7 精简（Linux） | `make build-armv7-minimal` | `luckclaw-armv7` |
| 交叉编译 arm64（Linux）    | `make build-arm64`         | `luckclaw-arm64` |
| 交叉编译 arm64 精简（Linux） | `make build-arm64-minimal` | `luckclaw-arm64` |

### 1.5 快速使用

1. **初始化**：运行 `luckclaw onboard` 可创建默认配置（包含所有可配置项及默认值）。
2. **配置**：根据需要修改 `~/.luckclaw/config.json`或运行 `luckclaw config` 交互式配置向导。
3. **交互**：根据运行模式选择合适的交互方式。
   - **Agent**：直接运行 `luckclaw agent` 即可开始交互。
   - **TUI**：运行 `luckclaw tui` 启动终端 UI 交互（暂时只支持 ssh 终端）。
   - **Gateway**：运行 `luckclaw gateway` 启动网关，可通过配置通道平台接入与 agent 交互。

***

## 二、配置文件

### 2.1 配置文件路径

| 来源                | 路径                                                                 |
| ----------------- | ------------------------------------------------------------------ |
| 默认                | `~/.luckclaw/config.json`                                          |
| `LUCKCLAW_CONFIG` | 直接指定 config.json 路径                                                |
| `LUCKCLAW_HOME`   | 数据根目录（默认 `~/.luckclaw`），ConfigPath = `{LUCKCLAW_HOME}/config.json` |

**初始化**：运行 `luckclaw onboard` 可创建默认配置（包含所有可配置项及默认值）。
**强制重置配置**：运行 `luckclaw onboard --force` 可强制将配置设置为默认值。
**参考模板**：`luckclaw config example` 列出参考配置文档。

### 2.2 顶层结构

```json
{
  "agents": { ... },
  "channels": { ... },
  "providers": { ... },
  "models": { ... },
  "gateway": { ... },
  "tools": { ... },
  "slashCommands": { ... }
}
```

***

## 四、Agent 配置

### 4.1 默认 Agent 参数（`agents.defaults`）

| 字段                       | 说明                     | 默认值                         |
| ------------------------ | ---------------------- | --------------------------- |
| `workspace`              | 工作区路径                  | `~/.luckclaw/workspace`     |
| `model`                  | 默认模型                   | `anthropic/claude-opus-4-5` |
| `provider`               | 提供商（`auto` 或具体名称）      | `auto`                      |
| `maxTokens`              | 最大 token               | 8192                        |
| `temperature`            | 温度                     | 0.1                         |
| `maxToolIterations`      | 工具迭代上限                 | 40                          |
| `memoryWindow`           | 记忆窗口                   | 20                          |
| `maxMessages`            | 最大消息数                  | 500                         |
| `consolidationTimeout`   | 记忆整合超时（秒）              | 30                          |
| `verboseDefault`         | 默认详细模式                 | true                        |
| `maxConcurrent`          | 全局并发数                  | 4                           |
| `debounceMs`             | 去重等待时间（ms）             | 1000                        |
| `streamingToolExecution` | 流式工具执行（LLM 未生成完即可执行）   | false                       |
| `parallelToolExecution`  | 并行执行同一轮多个工具            | false                       |
| `blockStreamingDefault`  | 块流式传输                  | false                       |
| `resourceConstrained`    | 资源受限模式（不自动下载软件包，仅提供建议） | false                       |
| `tokenBudget`            | Token 预算（精简 context）   | -                           |
| `routing`                | 按复杂度选模型                | -                           |

### 4.2 子 Agent 配置（`agents.subagents`）

| 字段                                   | 说明                 | 默认值    |
| ------------------------------------ | ------------------ | ------ |
| `enabled`                            | 启用子 Agent          | true   |
| `maxConcurrent`                      | 子 Agent 并发数        | 3      |
| `timeout`                            | 超时（ms）             | 120000 |
| `maxNestingDepth`                    | 嵌套深度（0=不嵌套）        | 2      |
| `model`                              | 子 Agent 专用模型（可更便宜） | -      |
| `inherit.tools`                      | 继承主 Agent 工具       | true   |
| `inherit.context`                    | 继承对话历史             | false  |
| `contextPassing.includeSystemPrompt` | 传递 system prompt   | true   |
| `contextPassing.includeConversation` | 传递对话               | false  |
| `contextPassing.includeSkills`       | 传递 skills          | true   |
| `toolPolicy.allowed`                 | 仅允许的工具列表           | -      |
| `toolPolicy.disabled`                | 禁用的工具列表            | -      |

### 4.3 路由配置（`agents.defaults.routing`）

按消息复杂度选择模型，简单任务用轻量模型以节省成本。

| 字段           | 说明                               |
| ------------ | -------------------------------- |
| `enabled`    | 启用路由                             |
| `lightModel` | 简单任务使用的模型（如 `groq/llama-3.1-8b`） |
| `threshold`  | 复杂度阈值 \[0,1]，≥ 阈值用主模型            |

### 4.4 Token 预算（`agents.defaults.tokenBudget`）

简单任务使用精简 context 以节省 token。

| 字段                | 说明                     |
| ----------------- | ---------------------- |
| `enabled`         | 启用                     |
| `simpleThreshold` | 0-1，低于此分数使用 compact 模式 |

### 4.5 资源受限模式（`agents.defaults.resourceConstrained`）

适用于资源受限环境（如嵌入式设备、低存储系统）。启用后，agent 不会自动下载或安装软件包，而是提供清晰的安装指令让用户手动执行。

**配置方式**：

- 配置文件：`agents.defaults.resourceConstrained: true`

**行为**：

- `clawhub_install` 工具：返回安装命令建议，不实际下载
- CLI 命令（`luckclaw clawhub install`）：仍可正常安装（用户明确执行）
- 若 ClawHub 返回 429/非 200（下载受限/网络问题），`clawhub_install` 会附带推荐 skills 与下一步命令（如 `onboard --skills`、`web_search/web_fetch`、MCP）。
- 系统提示：告知 agent 当前处于资源受限环境

**示例**：

```json
{
  "agents": {
    "defaults": {
      "resourceConstrained": true
    }
  }
}
```

***

## 五、LLM 提供商配置（`providers`）

### 5.1 支持的提供商

| 提供商         | 默认 API Base                                               |
| ----------- | --------------------------------------------------------- |
| openrouter  | <https://openrouter.ai/api/v1>                            |
| openai      | <https://api.openai.com/v1>                               |
| anthropic   | <https://api.anthropic.com/v1>                            |
| deepseek    | <https://api.deepseek.com>                                |
| groq        | <https://api.groq.com/openai/v1>                          |
| zhipu       | <https://open.bigmodel.cn/api/paas/v4>                    |
| dashscope   | <https://dashscope.aliyuncs.com/compatible-mode/v1>       |
| moonshot    | <https://api.moonshot.ai/v1>                              |
| aihubmix    | <https://aihubmix.com/v1>                                 |
| minimax     | <https://api.minimax.chat/v1>                             |
| volcengine  | <https://ark.cn-beijing.volces.com/api/v3>                |
| siliconflow | <https://api.siliconflow.cn/v1>                           |
| gemini      | <https://generativelanguage.googleapis.com/v1beta/openai> |
| vllm        | 自定义                                                       |
| ollama      | 自定义                                                       |
| custom      | 自定义                                                       |

**注意**: 如果平台没有安装 CA 证书，需要将 `apiBase` 配置为 `http://` 开头。

### 5.2 每个 Provider 的配置字段

| 字段             | 说明               |
| -------------- | ---------------- |
| `apiKey`       | API 密钥           |
| `apiBase`      | API 基础 URL（可选覆盖） |
| `extraHeaders` | 额外请求头            |

**开启方式**：在 `providers` 中配置对应 provider 的 `apiKey` 即可使用。`ollama` 和 `vllm` 为本地部署，仅需配置 `apiBase` 即可（无需 apiKey）。

***

## 六、通道配置（`channels`）

### 6.1 通道列表与启用方式

| 通道             | 启用字段                          | 必需配置                                                    |
| -------------- | ----------------------------- | ------------------------------------------------------- |
| **Telegram**   | `channels.telegram.enabled`   | `token`                                                 |
| **Discord**    | `channels.discord.enabled`    | `token`                                                 |
| **Feishu**     | `channels.feishu.enabled`     | `appId`, `appSecret`, `encryptKey`, `verificationToken` |
| **Slack**      | `channels.slack.enabled`      | `botToken`, `appToken`                                  |
| **DingTalk**   | `channels.dingtalk.enabled`   | `appKey`, `appSecret`, `robotCode`                      |
| **QQ**         | `channels.qq.enabled`         | `appId`, `secret`                                       |
| **WorkWeixin** | `channels.workweixin.enabled` | `botId`, `secret`                                       |
| **WebUI**      | 内置                            | 注册为 `webui` 通道                                          |

### 6.2 通用通道配置

| 字段          | 说明                       |
| ----------- | ------------------------ |
| `allowFrom` | 允许的用户 ID 列表，`["*"]` 表示全部 |

### 6.3 各通道特有配置

**Telegram**

| 字段               | 说明                                                           |
| ---------------- | ------------------------------------------------------------ |
| `proxy`          | 代理 URL                                                       |
| `sendProgress`   | 发送进度                                                         |
| `sendToolHints`  | 发送工具提示                                                       |
| `replyToMessage` | 回复原消息                                                        |
| `typing`         | 持续打字指示（直到回复发送）                                               |
| `placeholder`    | 占位消息：`{ "enabled": true, "text": "Thinking... 💭" }`，回复时原地编辑 |

**全局 UX 开关**（`ux`）：在 agent、tui、channels 中统一启用 typing/placeholder：

```json
{
  "ux": {
    "typing": true,
    "placeholder": { "enabled": true, "text": "Thinking... 💭" }
  }
}
```

**Discord**

| 字段             | 说明                                                   |
| -------------- | ---------------------------------------------------- |
| `gatewayUrl`   | WebSocket Gateway URL                                |
| `intents`      | Discord Intents                                      |
| `groupPolicy`  | 旧版：`mention` / `all`                                 |
| `groupTrigger` | 覆盖 groupPolicy                                       |
| `typing`       | 持续打字指示                                               |
| `placeholder`  | 占位消息：`{ "enabled": true, "text": "Thinking... 💭" }` |
| `proxy`        | 代理                                                   |

**Feishu**

| 字段               | 说明      |
| ---------------- | ------- |
| `blockStreaming` | 启用块流式发送 |

**Slack**

| 字段              | 说明           |
| --------------- | ------------ |
| `replyInThread` | 在 thread 中回复 |
| `reactionEmoji` | 反应表情         |

***

## 七、Gateway 配置（`gateway`）

| 字段                  | 说明      | 默认值     |
| ------------------- | ------- | ------- |
| `host`              | 监听地址    | 0.0.0.0 |
| `port`              | 端口      | 18790   |
| `inboundQueueCap`   | 入队容量    | 100     |
| `outboundQueueCap`  | 出队容量    | 100     |
| `heartbeatInterval` | 心跳间隔（秒） | 300     |
| `heartbeatChannel`  | 心跳发送通道  | -       |
| `heartbeatChatID`   | 心跳发送目标  | -       |

***

## 八、工具配置（`tools`）

### 8.1 内置工具开关

| 配置                          | 默认    | 说明                              |
| --------------------------- | ----- | ------------------------------- |
| `tools.restrictToWorkspace` | false | 文件操作限制在工作区内                     |
| `tools.agentBrowser`        | false | 启用浏览器自动化                        |
| `tools.agentMemory`         | true  | 长期记忆（MEMORY.md + consolidation） |
| `tools.selfImproving`       | true  | 错误学习与自我改进                       |
| `tools.clawdstrike`         | true  | 安全审计工具                          |
| `tools.evolver`             | false | 经验记录与自我进化                       |
| `tools.adaptiveReasoning`   | false | 自适应推理深度                         |

### 8.2 执行工具（`tools.exec`）

| 字段           | 说明      | 默认 |
| ------------ | ------- | -- |
| `timeout`    | 超时（秒）   | 60 |
| `pathAppend` | PATH 追加 | -  |

### 8.3 浏览器工具（`tools.browser`）

**启用条件**：`tools.agentBrowser` + `tools.browser.enabled` + `tools.browser.remoteUrl` 均配置

| 字段            | 说明                                                    |
| ------------- | ----------------------------------------------------- |
| `enabled`     | 启用                                                    |
| `remoteUrl`   | 远程浏览器 WebSocket URL（默认 `wss://chrome.browserless.io`） |
| `token`       | Browserless token，也可用 `BROWSERLESS_TOKEN` 环境变量        |
| `profile`     | 配置名（默认 `default`）                                     |
| `snapshotDir` | 截图目录                                                  |
| `debugPort`   | CDP 调试端口                                              |

### 8.4 Web 搜索（`tools.web.search`）

| Provider   | 配置                                                                 | 说明               |
| ---------- | ------------------------------------------------------------------ | ---------------- |
| Brave      | `brave.enabled`, `brave.apiKey`, `brave.maxResults`                | Brave Search API |
| Tavily     | `tavily.enabled`, `tavily.apiKey`, `tavily.maxResults`             | Tavily API       |
| DuckDuckGo | `duckduckgo.enabled`, `duckduckgo.maxResults`                      | 无需 API Key，默认启用  |
| Perplexity | `perplexity.enabled`, `perplexity.apiKey`, `perplexity.maxResults` | Perplexity API   |
| SearXNG    | `searxng.enabled`, `searxng.baseUrl`, `searxng.maxResults`         | 自建 SearXNG       |

**无 API Key 时**：默认启用 DuckDuckGo 作为回退。

### 8.5 Web 抓取（`tools.web.fetch`）

| 字段                 | 说明                |
| ------------------ | ----------------- |
| `firecrawl.apiKey` | Firecrawl API Key |

### 8.6 Web 代理（`tools.web`）

| 字段           | 说明       |
| ------------ | -------- |
| `httpProxy`  | HTTP 代理  |
| `httpsProxy` | HTTPS 代理 |
| `allProxy`   | 通用代理     |

### 8.7 MCP 服务器（`tools.mcpServers`）

支持三种传输方式：**stdio**、**sse**、**streamablehttp**。

#### 8.7.1 stdio（子进程，默认）

通过 `command` 启动子进程，经 stdin/stdout 通信。适用于本地 MCP 服务（如 npx 启动的 Node 服务）。

```json
{
  "tools": {
    "mcpServers": {
      "filesystem": {
        "type": "stdio",
        "command": "npx",
        "args": ["-y", "@modelcontextprotocol/server-filesystem", "/path/to/allowed/dir"],
        "env": { "NODE_ENV": "production" },
        "toolTimeout": 60
      }
    }
  }
}
```

- `type`: 可省略，有 `command` 时自动推断为 `stdio`
- `command`: 可执行路径（如 `npx`、`uvx`、`python`）
- `args`: 命令行参数
- `env`: 环境变量

#### 8.7.2 SSE（HTTP + Server-Sent Events）

连接 MCP 2024-11-05 规范的 SSE 端点。适用于远程 HTTP 服务。

```json
{
  "tools": {
    "mcpServers": {
      "remote-sse": {
        "type": "sse",
        "url": "https://mcp.example.com/sse",
        "headers": {
          "Authorization": "Bearer YOUR_TOKEN",
          "X-Custom-Header": "value"
        },
        "toolTimeout": 60
      }
    }
  }
}
```

- `type`: 必须为 `sse`（或 URL 以 `/sse` 结尾时自动推断）
- `url`: SSE 端点 URL
- `headers`: 可选，HTTP 请求头（如认证）

#### 8.7.3 streamable HTTP（HTTP 流式传输）

连接 MCP 2025-03-26 规范的 streamable HTTP 端点。适用于远程 HTTP 服务。

```json
{
  "tools": {
    "mcpServers": {
      "remote-http": {
        "type": "streamablehttp",
        "url": "https://mcp.example.com/mcp",
        "headers": {
          "Authorization": "Bearer YOUR_TOKEN"
        },
        "toolTimeout": 60
      }
    }
  }
}
```

- `type`: 必须为 `streamablehttp`（或 URL 不以 `/sse` 结尾时自动推断）
- `url`: MCP 端点 URL
- `headers`: 可选，HTTP 请求头

### 8.8 工具列表汇总

| 工具                                             | 配置/条件                                                                      |
| ---------------------------------------------- | -------------------------------------------------------------------------- |
| read\_file, write\_file, edit\_file, list\_dir | `tools.restrictToWorkspace` 限制目录                                           |
| exec                                           | `tools.exec`                                                               |
| web\_search                                    | `tools.web.search`                                                         |
| web\_fetch                                     | `tools.web.fetch.firecrawl.apiKey`                                         |
| tool\_search                                   | 内置                                                                         |
| clawhub\_search, clawhub\_install              | 工作区存在时                                                                     |
| browser                                        | `tools.agentBrowser` + `tools.browser.enabled` + `tools.browser.remoteUrl` |
| record\_correction                             | `tools.selfImproving`                                                      |
| clawdstrike                                    | `tools.clawdstrike`                                                        |
| cron                                           | Gateway 注入                                                                 |
| message                                        | Gateway 注入                                                                 |
| send\_file                                     | 内置                                                                         |
| spawn, subagents                               | 子 Agent 相关                                                                 |
| MCP 工具                                         | `tools.mcpServers`                                                         |

***

## 九、Skills 配置

### 9.1 发现方式

- **目录**：`{workspace}/skills/{name}/SKILL.md`
- **Frontmatter**：`description`、`metadata`（`luckclaw.requires.bins`、`luckclaw.requires.env`、`luckclaw.always`）

### 9.2 配置说明

- 无独立 config 开关，由工作区目录决定
- `always: true` 的 skill 自动注入 system prompt

### 9.3 CLI 命令

| 命令                          | 说明           |
| --------------------------- | ------------ |
| `luckclaw skills list`      | 列出已发现 skills |
| `luckclaw onboard --skills` | 仅添加默认 skills |

***

## 十、Slash 命令（`slashCommands`）

| 命令           | 说明                                                        |
| ------------ | --------------------------------------------------------- |
| `/help`      | 显示帮助信息                                                    |
| `/new`       | 开启新对话，并在开启前整合当前对话到长期记忆（MEMORY.md）                         |
| `/reset`     | 重置当前对话历史（不整合记忆）                                           |
| `/verbose`   | 切换详细模式（显示/隐藏工具调用过程）；支持 `/verbose on\|off`                 |
| `/summary`   | 自动总结当前对话的主要内容、决策与结果                                       |
| `/simple`    | 控制精简模式：`on\|off\|auto`。开启后使用紧凑上下文以节省 Token                |
| `/plan`      | 规划模式：`/plan <任务>`。Agent 会先输出步骤规划，再逐步执行                    |
| `/model`     | 查看或切换当前会话的模型：`/model <modelId>`                           |
| `/models`    | 列出当前所有可用的 AI 模型                                           |
| `/skill`     | 列出已发现的 Skills；`/skill <name>` 可直接运行特定 Skill               |
| `/subagents` | 管理子 Agent 任务：`list\|stop\|status\|kill\|info\|spawn`      |
| `/turn`      | 临时人格/视角切换：`reroll\|status\|on\|off\|save\|clear`          |
| `/luck`      | 记录成功经验到 `LUCK.md` 并更新自我改进库；支持 `list\|last\|<title>`       |
| `/badluck`   | 记录失败经验到 `BADLUCK.md` 避免下次犯错；支持 `list\|last\|<avoid_note>` |
| `/stop`      | 强制终止当前会话的所有运行任务                                           |
| `/mcp`       | 列出已连接的 MCP 服务器及其工具                                        |
| `/sessions`  | 管理并切换会话（仅限 TUI 模式）                                        |
| `/heartbeat` | 查看心跳状态（仅限网关模式）                                            |
| `/cmdname`   | 自定义斜杠命令，格式见配置文件说明                                         |

### 10.2 Luck & BadLuck 机制

Luckclaw 内置了基于经验的学习机制，通过手动反馈记录模型行为：

- **LUCK (幸运事件)**: 当模型完美完成一个复杂任务时，运行 `/luck <描述>`。这会将完整的工具调用链路存入 `LUCK.md`，并在下次类似任务时作为参考。
- **BADLUCK (糟糕事件)**: 当模型执行出错或不符合预期时，运行 `/badluck <改进建议>`。这会记录失败原因，并通过 `record_correction` 工具持久化到自我改进库中，防止模型再次犯错。

这些经验数据共同驱动了 Agent 的**自我改进 (Self-Improving)** 能力。

***

## 十一、配置向导

运行 `luckclaw config` 进入交互式 TUI，可配置：

1. Agent（workspace、model、provider）
2. Providers（API key、API base）
3. Channels（启用/禁用及各通道选项）
4. Gateway
5. Tools（exec、web、browser、built-in 等）
6. 保存并退出

***

## 十二、许可证

本项目遵循 MIT 许可证。
