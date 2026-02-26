# AGENTS.md — Orangeshell Project Guide

> **Read this file first** at the start of every agent session.
> Update the "Lessons Learned" section at the end of each session.

---

## 1. Project Overview

**Orangeshell** is a TUI application for managing Cloudflare Worker projects. Built with [Bubble Tea](https://github.com/charmbracelet/bubbletea) (Elm Architecture for Go).

- **Module**: `github.com/oarafat/orangeshell`
- **Remote**: `arafato/orangeshell`
- **Go version**: 1.25.1
- **Config**: `~/.orangeshell/config.toml`

### Key Dependencies

| Package | Purpose |
|---------|---------|
| `charmbracelet/bubbletea` v1.3.10 | TUI framework (Elm Architecture) |
| `charmbracelet/lipgloss` v1.1.0 | Terminal styling |
| `charmbracelet/glamour` v0.10.0 | Markdown rendering (AI chat) |
| `cloudflare/cloudflare-go/v6` v6.6.0 | Cloudflare API SDK |
| `lrstanley/bubblezone` v1.0.0 | Mouse event zone tracking |
| `tmaxmax/go-sse` v0.11.0 | SSE client (AI streaming) |
| `gorilla/websocket` | WebSocket client (tail sessions) |
| `BurntSushi/toml` | TOML config parsing/writing |
| `tidwall/gjson` + `sjson` + `jsonc` | JSON path queries/mutations + JSONC |

### Common Import Aliases

```go
tea  "github.com/charmbracelet/bubbletea"
zone "github.com/lrstanley/bubblezone"
svc  "github.com/oarafat/orangeshell/internal/service"
wcfg "github.com/oarafat/orangeshell/internal/wrangler"
uiwrangler "github.com/oarafat/orangeshell/internal/ui/wrangler"
uiconfig   "github.com/oarafat/orangeshell/internal/ui/config"
uiai       "github.com/oarafat/orangeshell/internal/ui/ai"
```

---

## 2. Directory Structure

```
orangeshell/
├── main.go                          # Entry point
├── version/version.go               # Build-time version injection (ldflags)
├── templates/ai-worker/             # AI proxy Worker source (TypeScript)
└── internal/
    ├── config/config.go             # Persistent TOML config + env var overrides
    ├── auth/                        # Authenticator interface + 3 implementations
    │   ├── auth.go                  #   Factory + interface definition
    │   ├── apikey.go                #   API Key + Email auth
    │   ├── apitoken.go              #   API Token (bearer) auth
    │   └── oauth.go                 #   OAuth PKCE flow with token refresh
    ├── api/
    │   ├── client.go                # Cloudflare SDK v6 client wrapper
    │   ├── builds.go                # Workers Builds API (raw HTTP, not SDK)
    │   ├── resources.go             # Lightweight resource list (Vectorize, Hyperdrive, mTLS)
    │   └── token.go                 # Scoped API token auto-provisioning
    ├── service/
    │   ├── service.go               # Service interface, Registry, caching, types
    │   ├── workers.go               # WorkersService + BindingIndex
    │   ├── kv.go, r2.go, d1.go      # KV/R2/D1 CRUD + Deleter
    │   ├── queues.go                # QueueService (name→UUID resolution)
    │   ├── vectorize.go, hyperdrive.go # Raw HTTP services
    │   └── tail.go                  # TailSession: WebSocket live log streaming
    ├── wrangler/
    │   ├── config.go                # WranglerConfig parser (TOML/JSON/JSONC)
    │   ├── finder.go                # Project discovery (recursive walk)
    │   ├── runner.go                # CLI command runner (npx wrangler)
    │   ├── writer.go                # Config file writer (TOML + JSON mutations)
    │   ├── workflows.go             # Workflow class scanner (source file regex)
    │   ├── create.go                # Project scaffolding via C3 + resource creation
    │   ├── deployments.go, versions.go # Parse wrangler output
    │   └── templates.go             # Cloudflare template catalog fetch
    └── ui/
        ├── theme/styles.go          # Lipgloss styles, color palette
        ├── app/                     # ROOT MODEL — composes everything
        │   ├── app.go               # Model struct, Update(), Init()
        │   ├── app_view.go          # View(), overlay compositing
        │   ├── app_services.go      # Service registration, navigation
        │   ├── app_actions.go       # Ctrl+P action popup builders
        │   ├── app_wrangler_cmds.go # Deploy/dev/delete/versions commands
        │   ├── app_wrangler_msgs.go # Wrangler message handler
        │   ├── app_config_ops.go    # Config tab message handlers
        │   ├── app_tail.go          # Tail session management
        │   ├── app_dev_tail.go      # Dev mode sessions + badge sync
        │   ├── app_deployments.go   # Deployment data fetching
        │   ├── app_deploy_all.go    # Parallel monorepo deploys
        │   ├── app_ai.go            # AI tab orchestration + handleAIMsg
        │   ├── app_detail.go        # Detail/resource message handler
        │   ├── app_monitoring_msgs.go # Monitoring message handler
        │   ├── app_overlays.go      # Search/actions/launcher
        │   └── app_resource_popup.go # Resource creation popup
        ├── tabbar/tabbar.go         # Tab bar (stateless rendering)
        ├── header/header.go         # Header: version, account tabs
        ├── setup/setup.go           # First-run auth wizard
        ├── wrangler/                # Operations tab
        ├── monitoring/              # Monitoring tab
        ├── detail/                  # Resources tab
        ├── config/                  # Configuration tab
        ├── ai/                      # AI tab
        │   ├── ai.go                # Dual-pane model: context + chat
        │   ├── chat.go              # Chat panel + permission prompts + spinner
        │   ├── backend.go           # Backend interface
        │   ├── backend_http.go      # HTTP backend (OpenAI + OpenCode protocols)
        │   ├── backend_workersai.go # Workers AI backend
        │   ├── client.go            # Workers AI HTTP/SSE client
        │   ├── context.go           # Context panel: log/file sources
        │   ├── files.go             # Project source file scanning
        │   ├── prompt.go            # System prompt construction
        │   ├── settings.go          # AI settings
        │   └── provision.go         # AI Worker auto-deployment
        └── (popups)                 # search, actions, launcher, envpopup,
                                     # deletepopup, projectpopup, resourcepopup,
                                     # removeprojectpopup, deployallpopup, confirmbox
```

### Tab IDs

```go
TabOperations = 0  // Key: 1     TabMonitoring = 1     // Key: 2
TabResources  = 2  // Key: 3     TabConfiguration = 3  // Key: 4
TabAI         = 4  // Key: 5
```

---

## 3. Architecture

### 3.1 Elm Architecture (Bubble Tea)

Every UI component follows `Init() → Update(msg) → View()`. The root `app.Model` composes ~20 sub-models.

**Value receiver convention**: ALL `Update()` methods use **value receivers** (`func (m Model) Update(...) (Model, tea.Cmd)`). Pointer receivers are used exclusively for mutator helpers (`setToast`, `layout`, `SetSize`, etc.).

**Message flow**: Components communicate via typed message structs. The root `Update()` acts as a message router. Components never call methods on each other directly.

### 3.2 Root Update() Structure

Two-tier dispatch:
1. **Handler chain**: Array of `func(*Model, tea.Msg) (Model, tea.Cmd, bool)` handlers (~15). If `handled == true`, processing stops.
2. **Type switch**: Handles remaining ~6 message types inline (lifecycle, window size, spinner ticks).

### 3.3 Overlay/Popup Pattern

All popups follow: separate package → visibility flag on root model (`showXxx bool`) → input routing when visible → `CloseMsg`/`DoneMsg` lifecycle → composited via `overlayCenter()` with dimmed background.

### 3.4 Service Registry

`service.Registry` — typed service locator with per-account, in-memory caching (30s TTL). Seven implementations: Workers, KV, R2, D1, Queues, Vectorize, Hyperdrive.

**Binding Index**: After Workers are listed, `BuildBindingIndex()` builds a reverse map (`"ServiceName:ResourceID"` → `[]BoundWorker`) for managed resource highlighting and delete warnings.

### 3.5 Wrangler CLI Integration

Commands run via `npx wrangler <args>` with `CI=true`. Two categories:
- **`cmdRunner`** — short-lived (deploy, delete). Output in `CmdPane`.
- **`devRunner`** — long-lived (`wrangler dev`). Output piped to monitoring grid + log file.

### 3.6 Data Refresh

**No polling.** Data refreshed on: stale cache navigation (>30s TTL), after mutations, on account switch.

### 3.7 AI Tab

Dual-pane: Context (30%) + Chat (70%). Three `Backend` implementations:
- **Workers AI**: SSE streaming to deployed proxy. Three model presets.
- **HTTP/OpenAI**: OpenAI-compatible `/v1/chat/completions`. Works with Ollama, LM Studio, vLLM.
- **HTTP/OpenCode**: Session-based API (`POST /session/:id/prompt_async`, `GET /event` SSE). Supports permission prompts (y/a/n), tool execution feedback ("agent working..."), session preservation across ESC (via `sawBusy` flag + `Abort()`), and dead connection detection (`deadlineReader` with 3min timeout).

**Stream cancellation**: ESC cancels context + calls `Abort()` (preserves session for multi-turn). Generation counter (`aiStreamGen`) drops stale Bubble Tea messages. `sawBusy` flag drops stale SSE events. Ctrl+N calls `Close()` for full session reset.

---

## 4. Key Data Types

```go
// Config
type Config struct {
    AuthMethod string; AccountID string; FallbackTokens map[string]string
    // + auth credentials, AI settings, env overrides
}

// Service
type Resource struct { ID, Name, ServiceType, Summary string; ModifiedAt time.Time }
type ResourceDetail struct { Resource; Fields []DetailField; Bindings []BindingInfo }
type BindingIndex struct { /* "ServiceName:ResourceID" → []BoundWorker */ }

// Wrangler
type WranglerConfig struct { Path, Format, Name, Main string; Bindings []Binding; Vars map[string]string }
type Runner struct { cmd *exec.Cmd; linesCh chan OutputLine; doneCh chan RunResult }

// API
type Client struct { CF *cloudflare.Client; AccountID string }
```

---

## 5. Style & Theme

### Color Palette (Cloudflare-inspired)
```go
ColorOrange = "#F6821F"   ColorWhite = "#FAFAFA"    ColorGray = "#7D7D7D"
ColorDarkGray = "#3A3A3A" ColorBg = "#1A1A2E"       ColorGreen = "#73D216"
ColorYellow = "#EDD400"   ColorRed = "#EF2929"       ColorBlue = "#729FCF"
```

**Conventions**: Orange border = focused, dark gray = unfocused. Yellow/gold = dev mode. Green `┃` = live deployment. Pill/button tabs with orange background when active.

---

## 6. Keyboard Shortcuts

| Key | Scope | Action |
|-----|-------|--------|
| `1`–`5` | Global | Switch tabs |
| `[` / `]` | Global | Switch accounts |
| `ctrl+k` | Global | Fuzzy search |
| `ctrl+l` | Global | Service launcher |
| `ctrl+p` | Global | Action palette |
| `ctrl+n` | Resources | New resource |
| `ctrl+s` | AI tab | AI settings |
| `esc` | AI (streaming) | Stop/cancel response |
| `y`/`a`/`n` | AI (permission) | Allow once/always/reject |
| `space` | Monitoring (left) | Toggle worker in/out of grid |
| `t` | Monitoring | Start tail |
| `enter` | Resources | View detail |
| `d` | Resources list | Delete resource |
| `tab` | D1 detail | Toggle SQL console |

---

## 7. Testing

### Test Workers (Cloudflare API)
- **Account ID**: `$CF_ACCOUNT_ID`  |  **Email**: `$CF_AUTH_EMAIL`  |  **Key**: `$CF_AUTH_TOKEN`
- `another-d1-app` — deployed via wrangler only (no build logs)
- `astro-blog` — deployed from git/dashboard (has build logs)

### Build & Run
```bash
go build -o orangeshell . && ./orangeshell [optional-directory]
```

### UX Mockups
- All tabs: `https://excalidraw.cfdata.org/drawing/3905fbe8-6fd5-45ea-8b3c-d22fc59f1682`
- AI Tab + Dev Mode: `https://excalidraw.cfdata.org/drawing/a9afa57a-bde8-45a6-bedf-d3bac64ec9cb`
- Workers Analytics Dashboard: `https://excalidraw.cfdata.org/drawing/c8adf9af-19af-4241-a3e8-278dfacf020d`

---

## 8. Lessons Learned

These are active pitfalls — things that will bite you if you don't know about them.

### Bubble Tea / TUI

1. **ANSI escape codes break `fmt.Sprintf("%-*s", ...)`**: Lipgloss styles add invisible ANSI sequences that corrupt padding width. Pad the raw string first, then apply styles.

2. **Spinner not animating**: `spinner.TickMsg` must be forwarded to the component. Ensure `SpinnerInit()` is batched with the loading command, and `IsLoading()` returns true for all loading states.

3. **Bracketed paste via `msg.Paste`**: Pasted text arrives as a single `tea.KeyMsg` with `Paste: true` and all characters in `Runes`. Check `msg.Paste` first. For single-char typing, check `len(msg.Runes) == 1` (not `len(msg.String()) == 1`).

4. **Pointer-receiver mutations + value-receiver Update = state desync**: When a pointer-receiver method mutates `cursor` or reorders `resources`, correlated state (`isLocalResource`, `detailID`) may be stale in the next value-receiver `Update()`. Any method that changes ordering must also update correlated state or trigger `autoPreview()`.

### Cloudflare SDK / API

5. **SDK `Placement` field panic**: The cloudflare-go v6 SDK panics on certain worker settings due to a union type deserialization bug. Use `client.Get()` with a custom `safeSettingsResponse` struct.

6. **SDK does NOT URL-encode path parameters**: Special characters in KV key names break requests. Call `url.PathEscape(keyName)` before passing to the SDK.

7. **SDK auto-reads env vars into every request**: `cloudflare.NewClient()` reads `CLOUDFLARE_API_KEY`/`EMAIL`/`TOKEN` from env and prepends them as defaults. Both auth mechanisms end up as HTTP headers. Fix: call `option.WithHeaderDel()` to strip conflicting headers after setting your intended auth.

8. **Global API Key may be account-scoped**: Never assume it works across all accounts. Prefer user-level tokens (OAuth, scoped API Token) in multi-account scenarios.

9. **Child process env var inheritance**: `os.Environ()` passes `CLOUDFLARE_API_KEY`/`EMAIL` to wrangler, overriding its own auth. Filter these env vars at every exec boundary (`Runner.Start()`, `CreateResource()`, AI provisioning). Local emulator commands (`--local`) are safe.

10. **Queue bindings store names, not UUIDs**: Wrangler config uses queue names, API uses UUIDs. `QueueService.resolveQueueID()` handles this.

11. **OAuth scope limitations**: Cloudflare's OAuth doesn't support Access or Workers Builds scopes. Use the fallback token pattern: `FallbackTokens map[string]string` per account, auto-provisioned via `POST /user/tokens`.

12. **Cloudflare DELETE APIs may return 200 or 204**: Accept any 2xx status, not just 200.

13. **Singleton vs array bindings**: AI, Browser, Images use `[section]` in TOML / plain object in JSON. All other bindings use `[[section]]` / array. `isSingletonBindingType()` centralizes the check.

### Architecture

14. **`zone.Scan()` must be called exactly once at the root View()**: Calling it in sub-components causes double-scanning.

15. **TOML tag uniqueness is critical**: Two fields with the same TOML tag compile fine but cause silent data corruption during decode.

16. **Never persist env-var-sourced secrets to disk**: `Config.Save()` must strip fields from environment variables before encoding. Track env-sourced fields with a `toml:"-"` map.

17. **interface{} message fields avoid import cycles**: `WriteDirectBindingMsg.BindingDef` is `interface{}` because the config UI package defines the message but the value is a `wcfg.BindingDef`. App layer does the type assertion.

### OpenCode Integration

18. **OpenCode protocol is NOT OpenAI-compatible**: Stateful sessions, global SSE stream (`/event` not `/global/event`), `message.part.delta` events with true deltas. Two separate code paths required.

19. **Generation counter protects Bubble Tea messages, NOT SSE events**: The `aiStreamGen` counter drops stale `aiStreamContinueMsg`/`aiStreamDoneMsg` from old goroutines. But stale `session.idle` SSE events arrive inside the new goroutine. Fix: `sawBusy` flag in `readOpenCodeEvents()`.

20. **OpenCode has two permission systems**: Legacy (`permission.updated`, `POST /session/:id/permissions/:permID`) and new (`permission.asked`, `POST /permission/:requestID/reply`). Handle both events, try both endpoints with fallback.

21. **SSE dead connection detection**: `sse.Read()` blocks forever on a dead socket during tool execution. `deadlineReader` wraps the body with 3-minute per-read timeout.

22. **Abort vs Close**: `Abort()` = stop prompt, keep session (ESC). `Close()` = stop + clear session (Ctrl+N, backend switch). Both fire `POST /session/:id/abort`.

23. **OpenCode auto-approves by default**: Unless `opencode.json` has explicit permission config (e.g., `"permission": {"*": "ask"}`), tool calls are auto-approved and `permission.asked` events are never published. Clear the SQLite `permission` table to reset "always" approvals.

24. **Source files use absolute paths in system prompt**: Relative paths cause OpenCode's Edit tool to fail. `ReadProjectFiles()` uses `f.Path` (absolute) not `f.RelPath`.

25. **Mouse wheel uses `msg.Button` not `msg.Type`**: In Bubble Tea v1.3.10, `MouseEventType` is deprecated. Use `msg.Button == tea.MouseButtonWheelUp` (not `tea.MouseWheelUp`, which is a `MouseEventType`).

26. **Mouse escape sequence fragments leak as KeyMsg**: Rapid mouse wheel events can produce partially-parsed CSI sequences (`\x1b[<65;30;10M`). The `\x1b` is consumed as escape, remaining bytes (`[`, `<`, digits) arrive as `tea.KeyMsg`. Fix: track `lastMouseTime` and suppress character insertion within 100ms of any `tea.MouseMsg`.

---

## 9. Design Principles (Bubble Tea)

### Patterns
- **Elm Architecture (MVU)**: Model → Update(msg) → View(). All state in Model structs.
- **Hierarchical Model Tree**: Root composes sub-models. Root Update delegates messages.
- **Message-Driven Communication**: Children return `tea.Msg` via `tea.Cmd`. Parents listen in Update.
- **Zone-Based Mouse Routing**: `zone.Mark()` in View, `zone.InBounds()` in Update, `zone.Scan()` at root.

### Rules
- **Never block Update()**: All I/O in `tea.Cmd` functions.
- **Propagate WindowSizeMsg**: Root catches it, passes constrained dimensions to children.
- **Unique zone IDs**: Combine component name with item ID/index.
- **Use lipgloss for layout**: Never manually calculate spaces/tabs.
- **Value receivers for Update()**: Pointer receivers only for explicit mutator methods.

---

## 10. Planned Features

### Workers Analytics Dashboard (IN PROGRESS)
- **Location**: Monitoring tab → right pane (Option D: replaces grid when viewing analytics)
- **Navigation**: Press `a` on a worker in the left pane → right pane switches to analytics. `esc` → back to grid.
- **Data source**: Cloudflare GraphQL Analytics API (`workersInvocationsAdaptive` dataset)
- **Fields**: sum{requests, errors, subrequests}, quantiles{cpuTimeP50/P99}, dimensions{datetime, scriptName, status}
- **Time ranges**: 1h, 6h, 24h, 7d, 30d (keyboard-switchable with `[`/`]`)
- **Auto-refresh**: Optional 30s polling via tea.Tick
- **Mockup**: `https://excalidraw.cfdata.org/drawing/c8adf9af-19af-4241-a3e8-278dfacf020d`
- **Files**: `internal/api/analytics.go`, `internal/ui/monitoring/analytics.go`, `internal/ui/monitoring/analytics_view.go`

### Queue Message Inspector (PLANNED)
- Resources tab → Queues detail. Pull/peek messages without acknowledging.
- View JSON payloads formatted. DLQ inspection and retry.
- Uses Cloudflare Queues API pull/ack endpoints.

### Logpush Configuration (PLANNED)
- Create/manage Logpush jobs to R2 or other destinations.
- Persistent log storage beyond the 200-line tail ring buffer.
- New service in `internal/service/logpush.go`.

### Cross-Worker Request Tracing (PLANNED)
- Enable/configure Workers Trace Events (see https://developers.cloudflare.com/workers/observability/traces/).
- Possibly visualize trace waterfall in TUI (Monitoring tab).
- API: trace configuration endpoints for enabling/disabling per-worker tracing.
