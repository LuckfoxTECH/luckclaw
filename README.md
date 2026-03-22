<div align="center">
  <img src="assets/logo.png" alt="LuckClaw" width="256">

  <h1>🍀 LuckClaw</h1>
</div>

---

English | [简体中文](./README_CN.md)

luckclaw is a lightweight personal AI assistant rebuilt in Golang based on [nanobot](https://github.com/HKUDS/nanobot). It supports multi-channel access (Discord, Telegram, Slack, etc.), multiple LLM providers, multiple interaction modes, result marking, Skills extensions, sub-agent management, and MCP server integration. It can be deployed on Luckfox Linux development boards and x86_64 PCs.


***

> ⚠️ **Warning**: This software is currently in **development and testing phase**. Security cannot be fully guaranteed. **It is strongly recommended NOT to deploy this software in production environments.**

## I. Overview

### 1.1 Runtime requirements

| <br />           | Requirement             |
| ---------------- | ----------------------- |
| **OS**           | Linux                   |
| **Architecture** | arm32 / arm64 / x86\_64 |
| **Memory**       | ≥ 32MB                  |
| **Storage**      | ≥ 30MB                  |

### 1.2 Subcommands

| Command                | Description                                                                             |
| ---------------------- | --------------------------------------------------------------------------------------- |
| `luckclaw onboard`   | Initialize configuration and workspace                                                  |
| `luckclaw config`    | Interactive configuration wizard                                                        |
| `luckclaw status`    | Show status                                                                             |
| `luckclaw agent`     | CLI interactive mode (invoke Agent directly)                                            |
| `luckclaw tui`       | Terminal TUI mode                                                                       |
| `luckclaw gateway`   | Multi-channel gateway (Bus, Channels, Cron, Heartbeat, WebUI)                           |
| `luckclaw models`    | List models                                                                             |
| `luckclaw cron`      | Manage scheduled tasks                                                                  |
| `luckclaw channels`  | Manage channels                                                                         |
| `luckclaw heartbeat` | Manage heartbeat                                                                        |
| `luckclaw skills`    | Manage Skills                                                                           |
| `luckclaw clawhub`   | ClawHub integration (downloads may be rate-limited; mainly used for Skills suggestions) |

### 1.3 Run modes

| Mode        | Command              | Description                                                                          |
| ----------- | -------------------- | ------------------------------------------------------------------------------------ |
| **Agent**   | `luckclaw agent`   | CLI interaction, invoke Agent directly                                               |
| **TUI**     | `luckclaw tui`     | Terminal UI interaction                                                              |
| **Gateway** | `luckclaw gateway` | Multi-channel gateway: unified message bus, channel adapters, cron, heartbeat, WebUI |

### 1.4 Build

| Target                                | Command                    | Output             |
| ------------------------------------- | -------------------------- | ------------------ |
| Default build                         | `make build`               | `luckclaw`       |
| Minimal build (without browser tools) | `make build-minimal`       | `luckclaw`       |
| Cross-compile armv7 (Linux)           | `make build-armv7`         | `luckclaw-armv7` |
| Cross-compile armv7 minimal (Linux)   | `make build-armv7-minimal` | `luckclaw-armv7` |
| Cross-compile arm64 (Linux)           | `make build-arm64`         | `luckclaw-arm64` |
| Cross-compile arm64 minimal (Linux)   | `make build-arm64-minimal` | `luckclaw-arm64` |

### 1.5 Quick usage

1. **Initialize**: run `luckclaw onboard` to create a default config (includes all configurable items and default values).
2. **Configure**: edit `~/.luckclaw/config.json` as needed, or run `luckclaw config` to enter the interactive configuration wizard.
3. **Interact**: choose a run mode that fits your workflow.
   - **Agent**: run `luckclaw agent` to start an interactive session.
   - **TUI**: run `luckclaw tui` to start the terminal UI (currently supports ssh terminals only).
   - **Gateway**: run `luckclaw gateway` to start the gateway, then connect a channel platform to interact with the agent.
4. **Remote terminal control (optional)**: in any chat/session, use `/terminal` to bind an SSH target; when enabled, the `exec` tool runs on the remote host, while web tools still run locally.
   - Add: `/terminal add dev ssh user@1.2.3.4 --port 22 --identity ~/.ssh/id_rsa`
   - Password auth: `/terminal add dev ssh user@1.2.3.4 --password-env LUCKCLAW_SSH_PASS` (recommended) or `--password <pass>` (in-memory only)
   - Use: `/terminal use dev` (or `/terminal off` to go back to local)
   - Transfer: `/terminal upload ./file.txt /tmp/file.txt` or `/terminal download /tmp/file.txt ./file.txt`
5. **Remote skills execution (optional)**: when a remote terminal is active, `/skill <name>` runs in a remote workspace under the remote host home directory and is prevented from touching local files.

### 1.6 Entry point and CLI

| <br />            | Description                                             |
| ----------------- | ------------------------------------------------------- |
| **Entry**         | `cmd/luckclaw/main.go` → `cli.NewRootCmd().Execute()` |
| **CLI framework** | Cobra                                                   |
| **Root command**  | `luckclaw`                                            |

***

## II. Configuration file

### 2.1 Configuration file location

| Source              | Path                                                                              |
| ------------------- | --------------------------------------------------------------------------------- |
| Default             | `~/.luckclaw/config.json`                                                       |
| `LUCKCLAW_CONFIG` | Specify the path to config.json directly                                          |
| `LUCKCLAW_HOME`   | Data root (default `~/.luckclaw`), ConfigPath = `{LUCKCLAW_HOME}/config.json` |

**Initialize**: run `luckclaw onboard` to create a default config (includes all configurable items and default values).\
**Force reset config**: run `luckclaw onboard --force` to reset the config to defaults.\
**Reference template**: `luckclaw config example` prints an example configuration document.

### 2.2 Top-level structure

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

## IV. Agent configuration

### 4.1 Default Agent parameters (`agents.defaults`)

| Field                    | Description                                                     | Default                     |
| ------------------------ | --------------------------------------------------------------- | --------------------------- |
| `workspace`              | Workspace path                                                  | `~/.luckclaw/workspace`   |
| `model`                  | Default model                                                   | `anthropic/claude-opus-4-5` |
| `provider`               | Provider (`auto` or a specific name)                            | `auto`                      |
| `maxTokens`              | Max tokens                                                      | 8192                        |
| `temperature`            | Temperature                                                     | 0.1                         |
| `maxToolIterations`      | Max tool iterations                                             | 40                          |
| `memoryWindow`           | Message count threshold for memory window (fallback)            | 20                          |
| `memoryWindowTokens`     | Token threshold for memory window (preferred)                   | -                           |
| `maxContextTokens`       | Total context token truncation threshold                        | -                           |
| `maxMemoryInjectChars`   | Memory injection character limit                                | -                           |
| `maxMessages`            | Max messages                                                    | 500                         |
| `consolidationTimeout`   | Memory consolidation timeout (seconds)                          | 30                          |
| `verboseDefault`         | Verbose by default                                              | true                        |
| `maxConcurrent`          | Global concurrency                                              | 4                           |
| `debounceMs`             | Debounce wait (ms)                                              | 1000                        |
| `streamingToolExecution` | Execute tools before LLM finishes generating                    | false                       |
| `parallelToolExecution`  | Execute multiple tools in parallel per round                    | false                       |
| `blockStreamingDefault`  | Block-streaming                                                 | false                       |
| `resourceConstrained`    | Resource-constrained mode (no auto downloads; suggestions only) | false                       |
| `tokenBudget`            | Token budget (compact context)                                  | -                           |
| `routing`                | Select model by complexity                                      | -                           |

### 4.2 Sub-agent configuration (`agents.subagents`)

| Field                                | Description                                | Default |
| ------------------------------------ | ------------------------------------------ | ------- |
| `enabled`                            | Enable sub-agents                          | true    |
| `maxConcurrent`                      | Sub-agent concurrency                      | 3       |
| `timeout`                            | Timeout (ms)                               | 120000  |
| `maxNestingDepth`                    | Nesting depth (0 = no nesting)             | 2       |
| `model`                              | Dedicated sub-agent model (can be cheaper) | -       |
| `inherit.tools`                      | Inherit tools from main agent              | true    |
| `inherit.context`                    | Inherit conversation history               | false   |
| `contextPassing.includeSystemPrompt` | Pass system prompt                         | true    |
| `contextPassing.includeConversation` | Pass conversation                          | false   |
| `contextPassing.includeSkills`       | Pass skills                                | true    |
| `toolPolicy.allowed`                 | Allowlist tools only                       | -       |
| `toolPolicy.disabled`                | Disable tools                              | -       |

### 4.3 Routing configuration (`agents.defaults.routing`)

Select a model based on message complexity. Use a lightweight model for simple tasks to reduce cost.

| Field        | Description                                                  |
| ------------ | ------------------------------------------------------------ |
| `enabled`    | Enable routing                                               |
| `lightModel` | Model for simple tasks (e.g. `groq/llama-3.1-8b`)            |
| `threshold`  | Complexity threshold \[0,1]; use main model when ≥ threshold |

### 4.4 Token budget (`agents.defaults.tokenBudget`)

Use a compact context for simple tasks to reduce token usage.

| Field             | Description                                 |
| ----------------- | ------------------------------------------- |
| `enabled`         | Enable                                      |
| `simpleThreshold` | 0-1, use compact mode when below this score |

### 4.5 Resource-constrained mode (`agents.defaults.resourceConstrained`)

Suitable for resource-limited environments (embedded devices, low-storage systems). When enabled, the agent will not automatically download or install packages; instead, it provides clear install instructions for you to run manually.

**How to enable**:

- Config file: `agents.defaults.resourceConstrained: true`
- Environment variable: `LUCKCLAW_AGENTS__DEFAULTS__RESOURCECONSTRAINED=true`

**Behavior**:

- `clawhub_install` tool: returns suggested install commands; does not download
- CLI command (`luckclaw clawhub install`): still performs a real install (explicit user action)
- If ClawHub returns 429/non-200 (rate limit/network issues), `clawhub_install` attaches recommended skills and next-step commands (e.g. `onboard --skills`, `web_search/web_fetch`, MCP)
- System prompt informs the agent it is in a resource-constrained environment

**Example**:

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

## V. LLM provider configuration (`providers`)

### 5.1 Supported providers

| Provider    | Default API Base                                          |
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
| vllm        | Custom                                                    |
| ollama      | Custom                                                    |
| custom      | Custom                                                    |

**Note**: If your platform does not have CA certificates installed, set `apiBase` to an `http://` URL.

### 5.2 Provider fields

| Field          | Description                      |
| -------------- | -------------------------------- |
| `apiKey`       | API key                          |
| `apiBase`      | API base URL (optional override) |
| `extraHeaders` | Additional request headers       |

**Enable**: set the provider's `apiKey` under `providers`. For `ollama` and `vllm` (local deployments), only `apiBase` is required (no apiKey).

***

## VI. Channel configuration (`channels`)

### 6.1 Channels and enabling

| Channel        | Enable field                  | Required configuration                                  |
| -------------- | ----------------------------- | ------------------------------------------------------- |
| **Telegram**   | `channels.telegram.enabled`   | `token`                                                 |
| **Discord**    | `channels.discord.enabled`    | `token`                                                 |
| **Feishu**     | `channels.feishu.enabled`     | `appId`, `appSecret`, `encryptKey`, `verificationToken` |
| **Slack**      | `channels.slack.enabled`      | `botToken`, `appToken`                                  |
| **DingTalk**   | `channels.dingtalk.enabled`   | `appKey`, `appSecret`, `robotCode`                      |
| **QQ**         | `channels.qq.enabled`         | `appId`, `secret`                                       |
| **WorkWeixin** | `channels.workweixin.enabled` | `botId`, `secret`                                       |
| **WebUI**      | Built-in                      | Registered as the `webui` channel                       |

### 6.2 Common channel fields

| Field       | Description                                  |
| ----------- | -------------------------------------------- |
| `allowFrom` | Allowed user ID list, `["*"]` means everyone |

### 6.3 Channel-specific fields

**Telegram**

| Field            | Description                                                                                    |
| ---------------- | ---------------------------------------------------------------------------------------------- |
| `proxy`          | Proxy URL                                                                                      |
| `sendProgress`   | Send progress updates                                                                          |
| `sendToolHints`  | Send tool hints                                                                                |
| `replyToMessage` | Reply to the original message                                                                  |
| `typing`         | Continuous typing indicator (until reply is sent)                                              |
| `placeholder`    | Placeholder message: `{ "enabled": true, "text": "Thinking... 💭" }`, edited in place on reply |

**Global UX switch** (`ux`): enable typing/placeholder consistently across agent, tui, channels:

```json
{
  "ux": {
    "typing": true,
    "placeholder": { "enabled": true, "text": "Thinking... 💭" }
  }
}
```

**Discord**

| Field          | Description                                                          |
| -------------- | -------------------------------------------------------------------- |
| `gatewayUrl`   | WebSocket Gateway URL                                                |
| `intents`      | Discord Intents                                                      |
| `groupPolicy`  | Legacy: `mention` / `all`                                            |
| `groupTrigger` | Overrides groupPolicy                                                |
| `typing`       | Continuous typing indicator                                          |
| `placeholder`  | Placeholder message: `{ "enabled": true, "text": "Thinking... 💭" }` |
| `proxy`        | Proxy                                                                |

**Feishu**

| Field            | Description                  |
| ---------------- | ---------------------------- |
| `blockStreaming` | Enable block-streaming sends |

**Slack**

| Field           | Description     |
| --------------- | --------------- |
| `replyInThread` | Reply in thread |
| `reactionEmoji` | Reaction emoji  |

***

## VII. Gateway configuration (`gateway`)

| Field               | Description                    | Default |
| ------------------- | ------------------------------ | ------- |
| `host`              | Listen address                 | 0.0.0.0 |
| `port`              | Port                           | 18790   |
| `inboundQueueCap`   | Inbound queue capacity         | 100     |
| `outboundQueueCap`  | Outbound queue capacity        | 100     |
| `heartbeatInterval` | Heartbeat interval (seconds)   | 300     |
| `heartbeatChannel`  | Channel for sending heartbeats | -       |
| `heartbeatChatID`   | Destination for heartbeats     | -       |

***

## VIII. Tool configuration (`tools`)

### 8.1 Built-in tool switches

| Setting                     | Default | Description                                  |
| --------------------------- | ------- | -------------------------------------------- |
| `tools.restrictToWorkspace` | false   | Restrict file operations to the workspace    |
| `tools.agentBrowser`        | false   | Enable browser automation                    |
| `tools.agentMemory`         | true    | Long-term memory (MEMORY.md + consolidation) |
| `tools.selfImproving`       | true    | Error learning and self-improvement          |
| `tools.clawdstrike`         | true    | Security audit tool                          |
| `tools.evolver`             | false   | Experience logging and evolution             |
| `tools.adaptiveReasoning`   | false   | Adaptive reasoning depth                     |

### 8.2 Exec tool (`tools.exec`)

| Field        | Description       | Default |
| ------------ | ----------------- | ------- |
| `timeout`    | Timeout (seconds) | 60      |
| `pathAppend` | Append to PATH    | -       |

### 8.3 Browser tool (`tools.browser`)

**Enable conditions**: `tools.agentBrowser` + `tools.browser.enabled` + `tools.browser.remoteUrl` are all configured

| Field         | Description                                                          |
| ------------- | -------------------------------------------------------------------- |
| `enabled`     | Enable                                                               |
| `remoteUrl`   | Remote browser WebSocket URL (default `wss://chrome.browserless.io`) |
| `token`       | Browserless token (or env `BROWSERLESS_TOKEN`)                       |
| `profile`     | Profile name (default `default`)                                     |
| `snapshotDir` | Screenshot directory                                                 |
| `debugPort`   | CDP debug port                                                       |

### 8.4 Web search (`tools.web.search`)

| Provider   | Setting                                                            | Description                             |
| ---------- | ------------------------------------------------------------------ | --------------------------------------- |
| Brave      | `brave.enabled`, `brave.apiKey`, `brave.maxResults`                | Brave Search API                        |
| Tavily     | `tavily.enabled`, `tavily.apiKey`, `tavily.maxResults`             | Tavily API                              |
| DuckDuckGo | `duckduckgo.enabled`, `duckduckgo.maxResults`                      | No API key required; enabled by default |
| Perplexity | `perplexity.enabled`, `perplexity.apiKey`, `perplexity.maxResults` | Perplexity API                          |
| SearXNG    | `searxng.enabled`, `searxng.baseUrl`, `searxng.maxResults`         | Self-hosted SearXNG                     |

**Without an API key**: DuckDuckGo is used as the fallback by default.

### 8.5 Web fetch (`tools.web.fetch`)

| Field              | Description       |
| ------------------ | ----------------- |
| `firecrawl.apiKey` | Firecrawl API Key |

### 8.6 Web proxy (`tools.web`)

| Field        | Description  |
| ------------ | ------------ |
| `httpProxy`  | HTTP proxy   |
| `httpsProxy` | HTTPS proxy  |
| `allProxy`   | Global proxy |

### 8.7 MCP servers (`tools.mcpServers`)

Three transports are supported: **stdio**, **sse**, and **streamablehttp**.

#### 8.7.1 stdio (child process, default)

Start a child process via `command`, communicate through stdin/stdout. Suitable for local MCP services (e.g. Node services launched by npx).

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

- `type`: optional; inferred as `stdio` when `command` is present
- `command`: executable path (e.g. `npx`, `uvx`, `python`)
- `args`: command-line arguments
- `env`: environment variables

#### 8.7.2 SSE (HTTP + Server-Sent Events)

Connect to an SSE endpoint compliant with MCP 2024-11-05. Suitable for remote HTTP services.

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

- `type`: must be `sse` (or inferred when URL ends with `/sse`)
- `url`: SSE endpoint URL
- `headers`: optional HTTP headers (e.g. auth)

#### 8.7.3 Streamable HTTP (HTTP streaming)

Connect to a streamable HTTP endpoint compliant with MCP 2025-03-26. Suitable for remote HTTP services.

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

- `type`: must be `streamablehttp` (or inferred when URL does not end with `/sse`)
- `url`: MCP endpoint URL
- `headers`: optional HTTP headers

### 8.8 Tool list summary

| Tool                                           | Configuration / Condition                                                  |
| ---------------------------------------------- | -------------------------------------------------------------------------- |
| read\_file, write\_file, edit\_file, list\_dir | Restricted by `tools.restrictToWorkspace`                                  |
| exec                                           | `tools.exec`                                                               |
| web\_search                                    | `tools.web.search`                                                         |
| web\_fetch                                     | `tools.web.fetch.firecrawl.apiKey`                                         |
| tool\_search                                   | Built-in                                                                   |
| clawhub\_search, clawhub\_install              | When a workspace exists                                                    |
| browser                                        | `tools.agentBrowser` + `tools.browser.enabled` + `tools.browser.remoteUrl` |
| record\_correction                             | `tools.selfImproving`                                                      |
| clawdstrike                                    | `tools.clawdstrike`                                                        |
| cron                                           | Injected by Gateway                                                        |
| message                                        | Injected by Gateway                                                        |
| send\_file                                     | Built-in                                                                   |
| spawn, subagents                               | Sub-agent related                                                          |
| MCP tools                                      | `tools.mcpServers`                                                         |

***

## IX. Skills configuration

### 9.1 Discovery

- **Local Directory**: `{workspace}/skills/{name}/SKILL.md`
- **Remote Directory**: When a remote terminal is active, skills are also discovered from `~/.luckclaw/workspace/skills/` and `~/.luckclaw_remote/ws/{termName}/{sessionHash}/skills/` on the remote host.
- **Frontmatter**: `description`, `metadata` (`luckclaw.requires.bins`, `luckclaw.requires.env`, `luckclaw.always`)

### 9.2 Notes

- No separate config switch; presence is determined by the workspace directory
- Skills with `always: true` are injected into the system prompt automatically

### 9.3 CLI commands

| Command                       | Description             |
| ----------------------------- | ----------------------- |
| `luckclaw skills list`      | List discovered skills  |
| `luckclaw onboard --skills` | Add default skills only |

***

## X. Slash commands (`slashCommands`)

| Command | Description |
|---------|-------------|
| `/help` | Display help information |
| `/new` | Start a new conversation, consolidating the current one into long-term memory (MEMORY.md) before opening |
| `/reset` | Reset current conversation history (without consolidating memory) |
| `/verbose` | Toggle verbose mode (show/hide tool execution process); supports `/verbose on\|off` |
| `/summary` | Auto-summarize the main content, decisions, and results of the current conversation |
| `/simple` | Control compact mode: `on\|off\|auto`. When enabled, uses compact context to save tokens |
| `/plan` | Planning mode: `/plan <task>`. Agent will first output step-by-step plan, then execute gradually |
| `/model` | View or switch the model for the current session: `/model <modelId>` |
| `/models` | List all currently available AI models |
| `/skill` | List discovered skills; `/skill <name>` to run a specific skill |
| `/subagents` | Manage sub-agent tasks: `list\|stop\|status\|kill\|info\|spawn` |
| `/turn` | Temporary personality/perspective switch: `reroll\|status\|on\|off\|save\|clear` |
| `/luck` | Record successful experience to `LUCK.md` and update self-improvement library; supports `list\|last\|<title>` |
| `/badluck` | Record failure experience to `BADLUCK.md` to avoid repeating mistakes; supports `list\|last\|<avoid_note>` |
| `/stop` | Force stop all running tasks in the current session |
| `/mcp` | List connected MCP servers and their tools |
| `/sessions` | Manage and switch sessions (TUI mode only) |
| `/heartbeat` | View heartbeat status (gateway mode only) |
| `/cmdname` | Custom slash command, see configuration file section for format |

### 10.2 Luck & BadLuck Mechanism

Luckclaw has a built-in experience-based learning mechanism that records model behavior through manual feedback:

- **LUCK**: When the model perfectly completes a complex task, run `/luck <description>`. This stores the complete tool call chain in `LUCK.md` and uses it as reference for similar tasks in the future.
- **BADLUCK**: When the model makes mistakes or produces unexpected results, run `/badluck <improvement suggestion>`. This records the failure reason and persists it to the self-improvement library via the `record_correction` tool to prevent the model from making the same mistakes again.

These experience data jointly drive the Agent's **Self-Improving** capability.

***

## XI. Configuration wizard

Run `luckclaw config` to enter the interactive TUI and configure:

1. Agent (workspace, model, provider)
2. Providers (API key, API base)
3. Channels (enable/disable and channel options)
4. Gateway
5. Tools (exec, web, browser, built-ins, etc.)
6. Save and exit

---

## XII. License

This project is licensed under the MIT License - see the [LICENSE](LICENSE) file for details.
