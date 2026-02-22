# AGENTS.md — Orangeshell Project Guide

> **Read this file first** at the start of every agent session.
> Update the "Lessons Learned" section at the end of each session.

---

## 1. Project Overview

**Orangeshell** is a terminal UI (TUI) application for managing Cloudflare Worker projects. Built with [Bubble Tea](https://github.com/charmbracelet/bubbletea) (Elm Architecture for Go), it provides a unified cockpit for deploying, monitoring, configuring, and analyzing Cloudflare Workers and their associated resources (KV, R2, D1, Queues).

- **Module**: `github.com/oarafat/orangeshell`
- **Remote**: `arafato/orangeshell`
- **Go version**: 1.25.1
- **Config location**: `~/.orangeshell/config.toml`

### Key Dependencies

| Package | Version | Purpose |
|---------|---------|---------|
| `charmbracelet/bubbletea` | v1.3.10 | Elm Architecture TUI framework |
| `charmbracelet/lipgloss` | v1.1.0 | Terminal CSS-like styling |
| `charmbracelet/glamour` | v0.10.0 | Markdown rendering (AI chat) |
| `cloudflare/cloudflare-go/v6` | v6.6.0 | Cloudflare API SDK |
| `lrstanley/bubblezone` | v1.0.0 | Mouse event zone tracking |
| `tmaxmax/go-sse` | v0.11.0 | SSE client (AI streaming) |
| `gorilla/websocket` | — | WebSocket client (tail sessions) |
| `BurntSushi/toml` | — | TOML config parsing/writing |
| `tidwall/gjson` + `sjson` + `jsonc` | — | JSON path queries/mutations + JSONC |
| `atotto/clipboard` | — | System clipboard write |
| `pkg/browser` | — | Open URLs in default browser |

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
├── assets/                          # README images
├── .goreleaser.yaml                 # Cross-platform release config
├── .github/workflows/release.yml    # GitHub Actions release workflow
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
    │   ├── workers.go               # WorkersService: list/get/settings/bindings
    │   ├── kv.go                    # KVService: CRUD + Deleter
    │   ├── r2.go                    # R2Service: CRUD + Deleter
    │   ├── d1.go                    # D1Service: CRUD + SQL console + schema
    │   ├── queues.go                # QueueService: CRUD + name→UUID resolution
    │   ├── vectorize.go             # VectorizeService: list/get/delete (raw HTTP)
    │   ├── hyperdrive.go            # HyperdriveService: list/get/delete (raw HTTP)
    │   └── tail.go                  # TailSession: WebSocket live log streaming
    ├── wrangler/
    │   ├── config.go                # WranglerConfig parser (TOML/JSON/JSONC)
    │   ├── finder.go                # Project discovery (recursive walk)
    │   ├── runner.go                # CLI command runner (npx wrangler)
    │   ├── writer.go                # Config file writer (TOML + JSON mutations)
    │   ├── workflows.go             # Workflow class scanner (source file regex)
    │   ├── create.go                # Project scaffolding via C3 + resource creation
    │   ├── deployments.go           # Parse wrangler deployments list output
    │   ├── versions.go              # Parse wrangler versions list output
    │   └── templates.go             # Cloudflare template catalog fetch
    └── ui/
        ├── theme/
        │   └── styles.go            # Lipgloss styles, color palette
        ├── app/                     # ROOT MODEL — composes everything
        │   ├── app.go               # Model struct, Update(), Init(), lifecycle msgs
        │   ├── app_view.go          # View(), overlay compositing
        │   ├── app_services.go      # Service registration, navigation
        │   ├── app_actions.go       # Ctrl+P action popup builders
        │   ├── app_wrangler_cmds.go # Deploy/dev/delete/versions commands
        │   ├── app_wrangler_msgs.go # handleWranglerMsg: wrangler message handler
        │   ├── app_config_ops.go    # Config tab message handlers (popup lifecycle)
        │   ├── app_tail.go          # Tail session management
        │   ├── app_dev_tail.go      # Dev mode sessions + badge sync
        │   ├── app_deployments.go   # Deployment data fetching
        │   ├── app_deploy_all.go    # Parallel monorepo deploys
        │   ├── app_ai.go            # AI tab orchestration + handleAIMsg
        │   ├── app_detail.go        # handleDetailMsg: detail/resource message handler
        │   ├── app_monitoring_msgs.go  # handleMonitoringMsg: monitoring message handler
        │   ├── app_overlays.go      # handleOverlayMsg: search/actions/launcher
        │   └── app_resource_popup.go # handleResourcePopupMsg: resource creation popup
        ├── tabbar/tabbar.go         # Tab bar (stateless rendering)
        ├── header/header.go         # Header: version, account tabs
        ├── setup/setup.go           # First-run auth wizard
        ├── wrangler/                # Operations tab
        │   ├── wrangler.go          # Main model: single/monorepo views
        │   ├── wrangler_messages.go # Message type definitions
        │   ├── wrangler_monorepo.go # Monorepo-specific functions
        │   ├── wrangler_view.go     # View rendering functions
        │   ├── projectbox.go        # Monorepo project grid item
        │   ├── envbox.go            # Environment panel with bindings
        │   ├── cmdpane.go           # Command output split pane
        │   ├── dirbrowser.go        # Directory browser
        │   ├── versionpicker.go     # Version deploy/rollout overlay
        │   └── paralleltail.go      # Multi-worker tail messages
        ├── monitoring/              # Monitoring tab
        │   ├── monitoring.go        # Model struct, tree/grid API, update logic
        │   ├── monitoring_messages.go # Message + type definitions
        │   └── monitoring_view.go   # View rendering functions
        ├── detail/                  # Resources tab
        │   ├── detail.go            # Model, core state management, Update/View entry
        │   ├── detail_messages.go   # Message type definitions
        │   ├── detail_d1.go         # D1 SQL console
        │   ├── detail_versions.go   # Version history + build log
        │   ├── detail_dropdown.go   # Service dropdown
        │   └── detail_helpers.go    # Utility functions
        ├── config/                  # Configuration tab
        │   ├── config.go            # Project dropdown + category tabs
        │   ├── messages.go          # Shared message types (env vars, triggers)
        │   ├── envvars.go           # Env variables sub-view
        │   ├── triggers.go          # Cron triggers sub-view
        │   ├── bindings.go          # Bindings sub-view
        │   └── environments.go      # Environments sub-view
        ├── ai/                      # AI tab
        │   ├── ai.go                # Dual-pane model: context + chat
        │   ├── chat.go              # Chat panel: history + streaming
        │   ├── client.go            # Workers AI HTTP/SSE client
        │   ├── context.go           # Context panel: log/file sources
        │   ├── files.go             # Project source file scanning
        │   ├── prompt.go            # System prompt construction
        │   ├── settings.go          # AI settings: provider/model/deploy
        │   └── provision.go         # AI Worker auto-deployment
        ├── resourcepopup/resourcepopup.go   # Ctrl+N resource creation popup
        ├── search/search.go         # Ctrl+K fuzzy search overlay
        ├── launcher/launcher.go     # Ctrl+L service launcher
        ├── actions/actions.go       # Ctrl+P command palette
        ├── envpopup/envpopup.go     # Add/delete environment popup
        ├── deletepopup/deletepopup.go       # Delete resource confirmation
        ├── projectpopup/projectpopup.go     # Create project wizard
        ├── removeprojectpopup/              # Remove project popup
        ├── deployallpopup/deployallpopup.go # Monorepo batch deploy
        └── confirmbox/confirmbox.go # Generic yes/no confirm component
```

### Tab IDs

```go
TabOperations    = 0  // Key: 1
TabMonitoring    = 1  // Key: 2
TabResources     = 2  // Key: 3
TabConfiguration = 3  // Key: 4
TabAI            = 4  // Key: 5
```

---

## 3. Architecture

### 3.1 Elm Architecture (Bubble Tea)

Every UI component follows `Init() → Update(msg) → View()`. The root `app.Model` composes ~20 sub-models. Messages bubble up from children and are handled in the root `Update()`.

**Value receiver convention**: ALL `Update()` and `Init()` methods use **value receivers** (`func (m Model) Update(...) (Model, tea.Cmd)`). Only the root app returns `(tea.Model, tea.Cmd)` — sub-components return their **concrete type**. Pointer receivers are used exclusively for mutator helpers (`setToast`, `layout`, `SetSize`, etc.).

**Message flow**: Components communicate via typed message structs defined in their own packages. The root `Update()` acts as a message router — type-switching and translating component messages into app-level state changes. Components never call methods on each other directly.

### 3.2 Root Update() Structure

The root `Update()` in `app.go` uses a two-tier dispatch:

1. **Handler chain** (first): An array of `func(*Model, tea.Msg) (Model, tea.Cmd, bool)` handlers. Each returns `(result, cmd, handled)`. If `handled == true`, processing stops. Currently covers 15 domain handlers.

2. **Type switch** (second): A small `switch msg := msg.(type)` handles the remaining ~6 message types inline (lifecycle, window size, spinner ticks).

Handler chain functions are defined in `app_config_ops.go` and take `*Model` as their first argument.

### 3.3 Overlay/Popup Pattern

All 9 popups follow this pattern:

1. **Separate package** with `Model`, `New()`, `Update()`, `View(termWidth, termHeight int) string`
2. **Visibility flag** on root model: `showXxx bool` + `xxxPopup ModelType`
3. **Input routing**: When `showXxx` is true, all input is routed to the popup
4. **Lifecycle**: Parent sets `showXxx = true` → popup emits `CloseMsg`/`DoneMsg` → parent sets `showXxx = false`
5. **View compositing**: `viewDashboard()` checks an `overlayEntry` array; first active overlay wins; background is dimmed with `dimContent()` and composited via `overlayCenter()`

**Note**: `confirmbox` is a pure rendering helper (no state, no Update) — used by `deletepopup`.

### 3.4 Bubblezone Mouse Handling

```go
// Marking (in View):
zone.Mark(zoneID, renderedContent)

// Hit-testing (in Update):
if z := zone.Get(zoneID); z != nil && z.InBounds(msg) { ... }

// Scanning (in root View — once):
zone.Scan(m.viewDashboard())
```

Zone ID conventions:
- `tab-{N}` — tab bar
- `hdr-acct-{N}` — header accounts
- `res-item-{N}` — resource list items
- `ProjectBoxZoneID(i)`, `EnvBoxZoneID(i)` — wrangler UI
- `cfg-dropdown`, `cfg-dd-{N}` — config dropdown
- String constants for one-off zones

### 3.5 Service Registry

`service.Registry` is a typed service locator with per-account, in-memory caching (30s TTL). Services are registered on authentication, cleared and re-registered on account switch.

```go
type Service interface {
    Name() string
    List() ([]Resource, error)
    Get(id string) (*ResourceDetail, error)
    SearchItems() []Resource
}
type Deleter interface { Delete(ctx context.Context, id string) error }
```

Seven implementations: `WorkersService`, `KVService`, `R2Service`, `D1Service`, `QueueService`, `VectorizeService`, `HyperdriveService`.

**Binding Index**: After Workers are listed, `BuildBindingIndex()` concurrently fetches settings for all workers and builds a reverse map (`"ServiceName:ResourceID"` → `[]BoundWorker`). Powers: managed resource highlighting, "Worker(s)" enrichment in detail views, and binding warnings in delete confirmations.

### 3.6 Wrangler CLI Integration

Commands run via `npx wrangler <args>` with `CI=true` to skip interactive prompts. Two runner categories:

- **`cmdRunner`** — short-lived: deploy, delete, versions deploy. Output shown in `CmdPane`.
- **`devRunner`** — long-lived: `wrangler dev`. Output piped to monitoring grid + log file.

The `Runner` type streams stdout/stderr line-by-line via channels. `--env=""` is passed for default environments to avoid wrangler ambiguity.

### 3.7 Data Refresh Strategy

**No polling.** Data is refreshed:
- When navigating to a view with stale cache (> 30s TTL)
- Immediately after mutating actions (deploy, delete, etc.)
- On account switch (cache retained per-account, background refresh triggered)

### 3.8 Multi-Account Support

Account tabs in the header allow switching via `[`/`]` or mouse clicks. Switching re-registers services with the new account ID while preserving cached data for other accounts. The binding index, deployment data, and resource caches are all per-account.

---

## 4. Key Data Types

### Config Layer
```go
type Config struct {
    AuthMethod     AuthMethod    // "apikey" | "apitoken" | "oauth"
    AccountID      string
    Email, APIKey, APIToken string
    OAuthAccessToken, OAuthRefreshToken string
    OAuthExpiresAt time.Time
    FallbackTokens map[string]string // Per-account scoped tokens for restricted APIs (Access, Builds)
    AIProvider     AIProvider    // "workers_ai"
    AIModelPreset  AIModelPreset // "fast" | "balanced" | "deep"
    AIWorkerURL    string
    AIWorkerSecret string
    envOverrides   map[string]bool // tracks env-var-sourced fields (never serialized)
}
```

### Service Layer
```go
type Resource struct { ID, Name, ServiceType, Summary string; ModifiedAt time.Time }
type ResourceDetail struct { Resource; Fields []DetailField; ExtraContent string; Bindings []BindingInfo }
type BindingInfo struct { Name, Type, TypeDisplay, Detail, NavService, NavResource string }
type BindingIndex struct { /* "ServiceName:ResourceID" → []BoundWorker */ }
```

### Wrangler Layer
```go
type WranglerConfig struct {
    Path, Format, Name, Main, CompatDate string
    Bindings []Binding; Vars map[string]string
    Crons []string; Environments map[string]*Environment
}
type Runner struct { cmd *exec.Cmd; linesCh chan OutputLine; doneCh chan RunResult }
type Version struct { ID string; Number int; CreatedOn time.Time; Source, AuthorEmail string }
type Deployment struct { ID, Source, AuthorEmail, Message string; Versions []DeploymentVersion }
type VersionHistoryEntry struct { /* Merged version + deployment + optional build metadata */ }
```

### API Layer
```go
type Client struct { CF *cloudflare.Client; AccountID string }
type BuildsClient struct { /* Raw HTTP for Workers Builds API */ }
type AuthError struct { StatusCode int; Body string } // Returned on 401/403
```

---

## 5. Message Catalog

### Application Lifecycle
| Message | Purpose |
|---------|---------|
| `SetProgramMsg` | Stores `*tea.Program` for background goroutine → UI sends |
| `initDashboardMsg` | Auth complete, transitions to dashboard |
| `errMsg` | Generic error display |
| `toastExpireMsg` | Clears toast notification |

### Wrangler Project
| Message | Purpose |
|---------|---------|
| `ConfigLoadedMsg` | Wrangler config parsed, triggers deployment fetch |
| `ProjectsDiscoveredMsg` | Monorepo projects found |
| `ActionMsg` | Ctrl+P action dispatched (deploy, dev, delete, etc.) |
| `CmdOutputMsg` / `CmdDoneMsg` | Wrangler command output streaming / completion |

### Resources
| Message | Purpose |
|---------|---------|
| `LoadResourcesMsg` / `ResourcesLoadedMsg` | Service list fetch |
| `LoadDetailMsg` / `DetailLoadedMsg` | Resource detail fetch |
| `DeleteResourceRequestMsg` | Opens delete confirmation |
| `bindingIndexBuiltMsg` | Reverse binding index ready |
| `accessIndexBuiltMsg` | Access protection index ready |
| `LoadVersionHistoryMsg` / `VersionHistoryLoadedMsg` | Version history fetch |
| `BuildsEnrichedMsg` / `BuildsAuthFailedMsg` | Builds API enrichment |
| `FetchBuildLogMsg` / `BuildLogLoadedMsg` | Build log fetch |

### Monitoring
| Message | Purpose |
|---------|---------|
| `TailStartMsg` / `TailStartedMsg` / `TailLogMsg` / `TailStopMsg` | WebSocket tail lifecycle |
| `TailAddMsg` / `TailRemoveMsg` | Grid pane management |
| `DevCronTriggerMsg` | Fire cron on local dev server |

### AI
| Message | Purpose |
|---------|---------|
| `AIProvisionRequestMsg` / `aiProvisionDoneMsg` | Deploy AI Worker |
| `AIChatSendMsg` | Start AI streaming |
| `aiStreamBatchMsg` / `aiStreamContinueMsg` / `AIChatStreamDoneMsg` | SSE stream lifecycle |

### Config Operations
| Message | Purpose |
|---------|---------|
| `config.SetVarMsg` / `config.DeleteVarMsg` | Env variable CRUD |
| `config.AddCronMsg` / `config.DeleteCronMsg` | Cron CRUD |
| `config.ListBindingResourcesMsg` / `config.BindingResourcesLoadedMsg` | Binding picker resource fetch |
| `config.WriteDirectBindingMsg` / `config.WriteDirectBindingDoneMsg` | Inline binding write |
| `envpopup.CreateEnvMsg` / `envpopup.DeleteEnvMsg` | Environment CRUD |

---

## 6. Style & Theme

### Color Palette (Cloudflare-inspired)
```go
ColorOrange    = "#F6821F"  // Primary accent
ColorOrangeDim = "#C46A1A"  // Dimmed accent
ColorWhite     = "#FAFAFA"  // Primary text
ColorGray      = "#7D7D7D"  // Secondary text
ColorDarkGray  = "#3A3A3A"  // Borders, separators
ColorBg        = "#1A1A2E"  // Background
ColorBgLight   = "#222240"  // Light background
ColorGreen     = "#73D216"  // Success
ColorYellow    = "#EDD400"  // Warning, dev mode
ColorRed       = "#EF2929"  // Error
ColorBlue      = "#729FCF"  // Info, labels
```

### Visual Conventions
- **Orange border** for focused/active elements; **dark gray** for unfocused
- **Pill/button style** for category tabs — active has orange background
- **Green "+"** prefix for Add buttons
- **Yellow/gold** borders and `[dev]`/`[dev-remote]` badges for dev mode
- **Green fat pipe** `┃` for live deployment marker in version history
- Delete confirmations: cursor-based No/Yes buttons navigated with `h`/`l` + enter
- Standard glamour dark style for AI markdown rendering (not custom)
- Rounded borders on all popups with orange border foreground

---

## 7. Keyboard Shortcuts

| Key | Scope | Action |
|-----|-------|--------|
| `1`–`5` | Global | Switch tabs (guarded by text input check) |
| `[` / `]` | Global | Switch accounts |
| `ctrl+k` | Global | Fuzzy search |
| `ctrl+l` | Global | Service launcher |
| `ctrl+p` | Global | Action palette |
| `ctrl+n` | Resources tab | New resource creation popup |
| `ctrl+s` | AI tab | AI settings |
| `t` | Monitoring | Start tail |
| `a` / `d` | Monitoring tree | Add/remove worker from grid |
| `c` | Monitoring (dev) | Fire cron trigger |
| `enter` | Resources | View detail / build log |
| `d` | Resources list | Delete resource |
| `tab` | D1 detail | Toggle SQL console |
| `pgup`/`pgdn` | Scrollable views | Page scroll |

---

## 8. Dev Mode Architecture

Dev mode runs `wrangler dev` (local) or `wrangler dev --remote` as long-lived processes.

**devRunner** tracks: Runner, key (`"project:env"`), kind (local/remote), status (starting/running/failed), port, log file handle.

**Lifecycle**:
1. Clean up existing runner for this project/env
2. Open log file at `~/.orangeshell/logs/dev-<name>-<timestamp>.log`
3. Create Runner with `--show-interactive-dev-session=false`
4. Add dev pane to monitoring grid (script name prefixed with `"dev:"`)
5. Stream output: classify lines by level, extract port via regex
6. Sync badges on EnvBox/ProjectBox: yellow `[dev:PORT]`

**Cron trigger**: HTTP GET to `localhost:<port>/cdn-cgi/handler/scheduled`.

---

## 9. AI Tab Architecture

Dual-pane: Context (30%) + Chat (70%).

**Context sources**: Log sources from monitoring grid + file sources from project code. Selection persists across updates. `BuildSystemPrompt()` interleaves logs chronologically with `[worker-name]` prefixes. Budget: ~120K chars total, 40K reserved for files.

**Client**: SSE streaming to deployed AI Worker proxy (`orangeshell-ai`). Three Workers AI presets: Fast (Llama 3.1 8B), Balanced (Llama 3.3 70B), Deep (DeepSeek R1 32B). Default `max_tokens`: 4096.

**Provisioning**: Auto-deploys from template at `arafato/orangeshell/templates/ai-worker` via GitHub API. Generates 32-byte base64url secret. Sets `AUTH_SECRET` via `wrangler secret put`.

---

## 10. Version History Feature

Located in Resources tab → Worker detail view.

**Data flow**:
1. `DetailLoadedMsg` for a Worker triggers `fetchVersionHistory()`
2. `wrangler versions list --json` + `wrangler deployments list --json` run in parallel
3. `BuildVersionHistory()` merges versions + deployments
4. `fetchBuildsForVersionHistory()` enriches with Workers Builds API (git metadata)
5. If Builds API returns 403 → `BuildsAuthFailedMsg` → token popup

**Workers Builds API** requires `Workers CI Read` scope, which cannot be created from the Cloudflare dashboard UI and cannot be granted via OAuth. Only the Global API Key or a programmatically-created API Token works. The `api_token_fallback` config field (auto-provisioned or manually set) provides these credentials.

**Display**: Table with short ID, relative time, source, message, author. Green `┃` marks the live deployment. Enter on CI-deployed version opens build log overlay.

---

## 11. Known Anti-Patterns & Refactoring Targets

### Large Files
| Lines | File | Issue |
|-------|------|-------|
| ~1122 | `app/app.go` | Root model + Update (reduced from ~1952 via handler chain expansion) |

**Combined app package**: ~6,600 lines across 16 files.

### Specific Issues

1. **Boolean overlay management**: 9 separate `showXxx bool` + popup model field pairs. Could use an overlay stack or enum.

2. **Stale-account guard duplication**: `m.isStaleAccount(msg.AccountID)` checked in 10+ places. Could be a single guard at the top of Update.

3. **Spinner tick routing**: Manual check of 6 components for `spinner.TickMsg`. Fragile as spinners are added.

4. **No sub-component interfaces**: Root model embeds all 15+ components by concrete type.

### Completed Refactoring (Phases 1-4)

1. **Phase 1**: Split `detail/detail.go` (2466→6 files), `monitoring/monitoring.go` (already split), `wrangler/wrangler.go` (already split).

2. **Phase 2**: Expanded handler chain from 10→16 handlers. Main type switch reduced from ~66 cases to 6. Created `app_detail.go`, `app_wrangler_msgs.go`, `app_monitoring_msgs.go`, `app_overlays.go`, extended `app_ai.go`.

3. **Phase 3**: Removed legacy `envvars/` and `triggers/` packages (~1,466 lines deleted). Migrated shared message types into `config/messages.go`. Removed `ViewEnvVars`/`ViewTriggers` enum values, ~18 guard conditions, 4 model fields. All env var/trigger actions now route to the unified Configuration tab.

4. **Phase 4**: Deleted unused `theme/keys.go`.

5. **Phase 5 (Phase C)**: Eliminated the `bindings/` popup wizard package (~769 lines deleted). Converted D1/KV/R2/Queue binding types from `Kind: "wizard"` to `Kind: "picker"`, unifying all binding creation through the inline picker→form flow on the Configuration tab. Removed `OpenBindingsWizardMsg`, `showBindings`/`bindingsPopup` fields, `handleBindingsMsg` handler, and 4 helper functions (`updateBindings`, `listResourcesCmd`, `createResourceCmd`, `writeBindingCmd`). The handler chain shrank from 16→15 handlers.

---

## 12. Testing

### Test Workers (Cloudflare API)
Use the following environment variables. If not set, ask for them if needed.
- **Account ID**: $CF_ACCOUNT_ID
- **Auth email**: $CF_AUTH_EMAIL
- **Auth key**: $CF_AUTH_TOKEN

Test workers:
- `another-d1-app` — deployed via wrangler only (no git, no build logs)
- `astro-blog` — deployed from git/dashboard (has build logs via Workers Builds API)

### Build & Run
```bash
go build -o orangeshell .
./orangeshell [optional-directory]
```

### UX Mockups
- All tabs: `https://excalidraw.cfdata.org/drawing/3905fbe8-6fd5-45ea-8b3c-d22fc59f1682`
- AI Tab + Dev Mode: `https://excalidraw.cfdata.org/drawing/a9afa57a-bde8-45a6-bedf-d3bac64ec9cb`
- Version History: `https://excalidraw.cfdata.org/drawing/5366f0e5-231f-4e6b-9aaf-63854fe54cef`

---

## 13. User Preferences (Persistent Across Sessions)

- Bubble Tea value receiver convention for `Update()`; pointer receivers for mutators
- Orange border/accent for focused; dark gray for unfocused
- Pill/button style for category tabs — active has orange background
- Inline picker→form flow for binding creation (select resource → pre-filled form)
- Inline edit/add/delete for env vars, triggers, environments
- Delete confirmations: cursor-based No/Yes (`h`/`l` + enter)
- Dev mode: yellow/gold borders and badges
- AI tab: always-visible left/right split, 3 model presets, Workers AI first
- Source file context: ONE toggle per project (not per file), default OFF
- AI Worker template fetched from GitHub repo (`arafato/orangeshell`)
- No CmdPane for dev commands — status shown inline via badges
- CmdPane kept for deploy/delete/status
- Dev server log files at `~/.orangeshell/logs/dev-{scriptName}-{timestamp}.log`
- Standard glamour dark style for AI markdown (not custom)
- Settings keybinding: `ctrl+s`
- Live version marker: green fat pipe `┃`
- Version history in Resources tab → Worker detail view (not a new tab)
- Data fetch: wrangler CLI for versions/deployments; raw HTTP for Workers Builds API
- Builds API token popup: removed — replaced by auto-provisioning + `(restricted)` badge

---

## 14. Lessons Learned

### Bubble Tea / TUI

1. **ANSI escape codes break `fmt.Sprintf("%-*s", ...)`**: Lipgloss styles add invisible ANSI sequences. When padding with `%-*s`, the pad count includes escape code bytes, not visible characters. Solution: pad the raw string first, then apply styles. Use a `padRight(s, width)` helper that pads to rune width before styling.

2. **Multi-line strings in table cells**: Commit messages with `\n` spill into adjacent rows. Always collapse `\n` to spaces and truncate before rendering in table layouts.

3. **Spinner not animating**: If a component reports `IsLoading()` but the spinner doesn't tick, it's because `spinner.TickMsg` isn't being forwarded. Ensure `SpinnerInit()` is called in the same `tea.Batch` as the loading command, and `IsLoading()` returns true for all loading states (not just the original ones).

4. **Wrangler output order assumptions**: `wrangler deployments list` returns oldest-first (chronological), NOT newest-first. Don't assume reverse-chronological. Check actual output format before building merge logic.

5. **Workers Builds API log format**: Log lines come as `[timestamp_millis, "message"]` 2-element JSON arrays, not objects. Requires custom `UnmarshalJSON`.

6. **Workers Builds API auth**: OAuth tokens (from wrangler login or orangeshell) return 403. Only Global API Key or a programmatically-created token with `Workers CI Read` scope (permission group `ad99c5ae555e45c4bef5bdf2678388ba`) works. This scope cannot be added via the Cloudflare dashboard UI.

7. **SDK `Placement` field panic**: The cloudflare-go v6 SDK panics on certain worker settings responses due to a union type deserialization bug. Workaround: use `client.Get()` with a custom `safeSettingsResponse` struct.

8. **Queue bindings store names, not UUIDs**: Wrangler config uses queue names for bindings, but the API uses UUIDs. The `QueueService` has `resolveQueueID()` to handle this mismatch.

### Architecture

9. **Handler chain pattern works well**: The `func(*Model, msg) (Model, cmd, handled)` pattern cleanly separates domain concerns. Expand it to cover all message types rather than using the inline type switch.

10. **Per-account caching is essential**: Users switch accounts frequently. Retaining caches per-account and refreshing in the background provides a smooth experience.

11. **`zone.Scan()` must be called exactly once at the root**: Calling it in sub-components causes double-scanning. Only the root `View()` should scan.

12. **Dev runner keys must be `"project:env"`**: Using just the script name causes collisions in monorepos where multiple projects may have the same environment names.

13. **Toast behind popup overlay**: Setting a toast while a popup is visible means the toast renders but is hidden by the overlay. Low priority but worth noting.

### Refactoring

14. **Handler chain scales well**: The handler chain pattern (`func(*Model, msg) (Model, cmd, handled)`) can comfortably handle 15+ handlers without performance issues. Each handler should return early (`return *m, nil, false`) for unhandled message types, so the chain is essentially O(n) where n is the number of handlers that check the message.

15. **Same-package file splits are safe**: Moving methods and functions to new files in the same package requires zero import changes and is very low risk. The Go compiler treats all `.go` files in a package as one unit. This is the safest refactoring approach for large files.

16. **Private message types can cross files**: Private message types like `bindingIndexBuiltMsg` defined in `app.go` can be handled in a different file (`app_detail.go`) because they're in the same package. No need to export them.

17. **Monitor unused imports after refactoring**: When moving message handling code to new files, the original file may have unused imports. Run `go build` after each step and fix import errors immediately rather than batching.

18. **Split monolithic type switch incrementally**: Rather than moving all cases at once, extract one domain (e.g., detail, wrangler, monitoring) at a time, build-verify, then continue. This makes debugging much easier.

19. **Define shared message types in the consumer package**: When removing a legacy package whose message types are used by both the new replacement and the app layer, define the shared types in the replacement package (e.g., `config/messages.go`) rather than creating a separate messages package. This avoids import cycles and keeps the types close to where they're emitted.

20. **Replace legacy navigation with config tab navigation**: Instead of opening legacy full-screen views (ViewEnvVars/ViewTriggers), navigate to the unified config tab with the appropriate category pre-selected using `syncConfigProjects()` + `SelectProjectByPath()` + `SetCategory()`. This eliminates entire view states and their associated guard conditions.

21. **Guard condition removal cascades**: Removing two ViewState enum values (`ViewEnvVars`, `ViewTriggers`) cascaded into removing ~18 guard conditions across 5 files. Each guard was a `m.viewState != ViewEnvVars && m.viewState != ViewTriggers` check that existed solely to bypass the legacy views. The removals simplified every branch they touched.

### Access Protection Feature

22. **SDK union types use `interface{}` — use raw HTTP instead**: The cloudflare-go v6 SDK's `AccessApplicationListResponse` uses `interface{}` for many fields (policies, destinations, self_hosted_domains, allowed_idps) due to union type deserialization. This is the same class of issue as the Placement field panic (lesson #7). Solution: use `client.Get()` with hand-rolled "safe" structs (`safeAccessApp`, etc.), same pattern as `getSettings()`.

23. **Access protects URLs, not Workers directly**: Access Applications are tied to domains/hostnames, not to Worker scripts. Matching Workers to Access apps requires collecting all Worker URLs (routes, custom domains, workers.dev subdomains) and matching them against Access app domains. Route patterns like `*.example.com/*` need wildcard matching in both directions.

24. **Parallel background indexes work well**: Building both the `BindingIndex` and `AccessIndex` in parallel after Workers list loads (`tea.Batch(buildBindingIndexCmd(), buildAccessIndexCmd())`) adds negligible latency since they use separate API endpoints. The access index fetch (Access apps + custom domains) is fully independent of the binding index fetch (worker settings).

25. **Badge re-sync on config/project reload**: When wrangler config loads or projects are discovered, EnvBoxes and ProjectBoxes are recreated from scratch, losing any previously set badge state. Must call `syncAccessBadges()` (and `syncDevBadges()`) after `ConfigLoadedMsg` and `ProjectsDiscoveredMsg` to reapply badge data from the cached indexes.

26. **Silent permission fallback is cleanest**: For optional enrichment features (Access info), returning an empty index on 401/403 (rather than propagating errors or prompting) keeps the UI clean. Users without the `Access: Apps and Policies Read` permission simply don't see Access badges — no error popups, no toast messages, no degraded UX.

### Restricted API Auth (Fallback Token Pattern)

27. **OAuth scope limitations are systemic**: Cloudflare's OAuth system (client ID `54d11594-...`) only supports a fixed set of scopes (`workers:write`, `d1:write`, etc.). Access/Zero Trust and Workers Builds scopes do not exist. This is a platform limitation, not a bug. Any API requiring `Access: Apps and Policies Read` or `Workers CI Read` will always 403 with an OAuth token.

28. **Auto-provisioning scoped tokens is the cleanest UX**: Rather than prompting users with a popup (old `buildstokenpopup` pattern) or requiring manual config edits, auto-provisioning via `POST /user/tokens` with the Global API Key from env vars (`CLOUDFLARE_API_KEY` + `CLOUDFLARE_EMAIL`) is seamless. The provisioned token is saved to config (`api_token_fallback`) so env vars are only needed once.

29. **TOML tag uniqueness is critical**: Having two struct fields with the same TOML tag (e.g., both `APIToken` and `APITokenFallback` tagged as `toml:"api_token"`) compiles fine but causes silent data corruption during decode — one field shadows the other. Always use distinct TOML tags.

30. **Restricted mode badge communicates degradation**: When fallback credentials are unavailable and auto-provisioning can't run (no env vars), showing a dimmed `(restricted)` badge in the header next to the auth method communicates the limitation without interrupting the workflow. Much better than error popups or toasts.

31. **Fallback auth chain pattern**: For restricted APIs, the auth priority chain should be: (1) dedicated fallback token from config, (2) primary auth method if it has permissions, (3) auto-provisioned token via env vars, (4) silent degradation. **IMPORTANT**: Do NOT use the Global API Key from env vars as a shortcut for OAuth auth — it may be scoped to a single account and cause 403 errors on account switch. Only use the primary auth method's own credentials. This pattern applies to both Access API and Workers Builds API and can be extended to future restricted APIs.

32. **Never persist env-var-sourced secrets to disk**: `Config.Save()` must strip fields that came from environment variables (`CLOUDFLARE_API_KEY`, `CLOUDFLARE_EMAIL`, etc.) before encoding to TOML. Track env-sourced fields with a `toml:"-"` map in `Load()`, then temporarily clear them in `Save()` and restore after encoding. For OAuth auth, also strip API Key + Email unconditionally — they're runtime-only fallback credentials and should never appear in the config file alongside OAuth tokens.

33. **Pre-fetch Workers list on dashboard init for early index builds**: The binding index and access index are only built after Workers are listed. If Workers aren't fetched until the user visits the Resources tab, badges on the Operations tab (EnvBoxes, ProjectBoxes) never appear. Dispatching `loadServiceResources("Workers")` from `initDashboardMsg` ensures both indexes are built in the background immediately, so badges are ready before the user even sees the Operations tab.

34. **Monorepo drill-in recreates envboxes from scratch**: `drillIntoProject()` creates fresh `EnvBox` instances, copying deployment data and subdomain from the `ProjectBox` but losing any badge state (Access, Dev) that was set on the project-level. Always copy badge fields (`AccessProtected`, dev badges) from the parent `ProjectBox` into the child `EnvBox` during drill-in, same pattern as deployment data.

### Bubble Tea Input Handling

35. **Bracketed paste via `msg.Paste`**: When the user pastes text (Cmd+V / Ctrl+V), Bubble Tea delivers it as a single `tea.KeyMsg` with `Paste: true` and all pasted characters in `Runes`. The `String()` method wraps pasted text in `[...]`, so `len(msg.String()) == 1` will never match a paste. Custom text input handlers must check `msg.Paste` first and iterate `msg.Runes` to append all characters. For single-character typing, check `len(msg.Runes) == 1` (not `len(msg.String()) == 1`) to correctly handle multi-byte UTF-8.

36. **Gate interactive UI elements on focus state**: Cursors, selection highlights, and other interactive indicators in sub-panes should only render when that pane has focus (`m.focus == FocusDetail`). Showing a cursor highlight unconditionally (e.g., version history table always showing a purple row) confuses users about which pane is active. The focus state already exists for border color switching — reuse it for cursor visibility.

### Binding Type Expansion

37. **Two-tier binding kind pattern**: Binding types fall into two categories: (1) "form" types (AI/Browser/Images, DO, Analytics Engine) with inline text inputs, and (2) "picker" types (D1, KV, R2, Queue, Service, Vectorize, Hyperdrive, mTLS, Workflow) that fetch API resources or scan local files first, then transition to a pre-filled form. The picker types use the service registry for D1/KV/R2/Queue/Workers, raw HTTP for Vectorize/Hyperdrive/mTLS, and local file scanning for Workflow.

38. **Singleton vs array bindings need distinct code paths everywhere**: AI, Browser, and Images use `[section]` in TOML (not `[[section]]`) and a plain object in JSON (not an array). The parser `collectBindings()`, writer `formatTOMLBinding()`/`addBindingJSON()`, and remover `removeBindingTOML()`/`removeBindingJSON()` all needed separate branches. Added `isSingletonBindingType()` helper to centralize the check.

39. **`ExtraFields map[string]string` is better than proliferating struct fields**: For binding types with 3+ config fields (Workflow: `name`, `class_name`, `script_name`; Service: `entrypoint`; DO: `script_name`), a generic `ExtraFields` map on `BindingDef` avoids adding many type-specific fields. The existing 4 types (D1/KV/R2/Queue) continue using `ResourceID`/`ResourceName` for backward compatibility.

40. **Inline mode chain within a category**: The binding add flow uses three sequential modes (`modeAddBinding` → `modeAddBindingForm` or `modeAddBindingPicker` → form submit). Picker mode transitions to form mode with pre-filled values after resource selection. All modes coexist within the same category's Update/View dispatch, not as separate overlay components. This keeps the config tab self-contained.

41. **interface{} message fields avoid import cycles**: `WriteDirectBindingMsg.BindingDef` is typed as `interface{}` because the `config` UI package defines the message but the value is a `wcfg.BindingDef`. The app layer (which imports both packages) does the type assertion. This avoids the config UI package importing wrangler types in its message definitions, which would create import cycle risk.

42. **Raw HTTP resource list functions use the generic Cloudflare v4 envelope**: All list endpoints (Vectorize, Hyperdrive, mTLS) return `{success, result[], errors[]}`. A single `parseResourceList(body, idField, nameField)` function handles all of them, extracting just ID and Name from each result item via `map[string]interface{}`. Much simpler than defining per-type response structs.

43. **Service bindings use the existing Workers registry, not raw HTTP**: For the "service" resource picker, the Workers service is already registered in the service registry and caches worker lists. Using `m.registry.Get("Workers").List()` avoids duplicating API calls. Only Vectorize, Hyperdrive, and mTLS need the raw HTTP `ResourceListClient` since they don't have full Service implementations.

44. **Workflow class auto-discovery via local source scan**: The Workflow binding type uses a "picker" kind that scans the project's source files for exported `WorkflowEntrypoint` subclasses instead of calling an API. The scanner (`wrangler/workflows.go`) supports two patterns: JS/TS `export class X extends WorkflowEntrypoint` and Python `class X(WorkflowEntrypoint):`. It reuses the same directory walk logic as the AI tab's file scanner (skip `node_modules`, `.git`, etc.; resolve source dir from `Main` entry). **Known limitations (NOT handled)**: (1) re-exports (`export { MyWorkflow } from './workflows'`), (2) barrel/index re-exports, (3) renamed imports (`import { WorkflowEntrypoint as WF }` then `extends WF`), (4) dynamic class names or factory patterns, (5) Python `__all__` exports, (6) classes in `node_modules` or other skipped directories. These cover edge cases; the standard pattern of a direct `export class ... extends WorkflowEntrypoint` in a source file covers the vast majority of real-world usage. The picker falls back gracefully — if no classes are found, the user sees "No resources found" and can press esc to go back and use the form manually by changing the type or entering values directly.

45. **Dual navigation paths for bindings**: Bindings in EnvBox now have two Enter targets: (1) types with `NavService()` (KV, R2, D1, Service, Queue, Vectorize, Hyperdrive) emit `NavigateMsg` → Resources tab via `navigateTo()`, (2) types without `NavService()` (AI, mTLS, Workflow, Browser, Images, Analytics, DO) emit `NavigateToBindingMsg` → Configuration tab → Bindings category with the specific binding highlighted via `SelectBindingByName()`. Both paths follow the same cross-tab navigation pattern as `ShowEnvVarsMsg`/`ShowTriggersMsg`: `syncConfigProjects()` + `SelectProjectByPath()` + `SetCategory()` + set `activeTab`. The new path adds one extra call: `SelectBindingByName(envName, bindingName)` to pre-position the cursor.

### Resource Tab Expansion & Service Registry

46. **New services use `ResourceListClient` not Cloudflare SDK**: The 5 original services (Workers, KV, R2, D1, Queues) use the cloudflare-go v6 SDK. New services (Vectorize, Hyperdrive) use raw HTTP via `ResourceListClient` because the SDK either doesn't cover these APIs or has union type deserialization issues. The service constructor takes `*api.ResourceListClient` instead of `*cloudflare.Client`. The `ResourceListClient` is constructed from auth credentials via `newResourceListClient()` in `app_config_ops.go`.

47. **Binding index now indexes 7 binding types**: `BuildBindingIndex()` in `workers.go` indexes all bindings with `NavService != ""`. This includes: KV (`kv_namespace`), D1 (`d1`), R2 (`r2_bucket`), Service (`service`), Queue (`queue`), Vectorize (`vectorize` → `NavResource: b.IndexName`), and Hyperdrive (`hyperdrive` → `NavResource: b.ID`). Delete guards now work for all seven resource types.

48. **Delete guard has two paths — immediate and deferred**: When the user presses `d` on a resource: (1) if the binding index is already built, lookup bound workers immediately and show confirm/blocked popup, (2) if index is not built, show a spinner popup via `NewLoading()`, stash the request in `pendingDeleteReq`, kick off Workers fetch + index build, then transition the popup via `SetBindingWarnings()` when the index arrives. New deletable services just need to be added to `isDeletableService()` in `detail_helpers.go`.

49. **Non-integrated services show "(coming soon)" and are non-selectable**: The `Integrated: false` flag on `ServiceEntry` causes dimmed rendering with "(coming soon)" suffix, and the Enter key guard prevents `SelectServiceMsg` from being emitted. Converting a service from non-integrated to integrated just requires setting `Integrated: true` and registering the service in the registry.

50. **Separation of resource creation and binding creation**: Resource creation (D1 databases, KV namespaces, Vectorize indexes, etc.) belongs on the Resources tab (Ctrl+N = "New Resource"). Binding creation (assigning an existing resource to a worker) belongs on the Configuration tab (inline picker flow). The old binding wizard popup conflated both. Splitting them simplifies both flows: resource creation is a standalone popup with type-specific fields, binding creation is always "select from existing + enter binding name".

51. **Secrets Store removed (beta)**: Secrets Store was removed from the codebase while the API is in beta. The API had several issues: no GET single store endpoint (405), OAuth scope gaps (`secrets_store:write` missing from orangeshell's flow), Global API Key unable to list secrets within stores, and secret creation requiring wrangler CLI interactive prompts. Will re-add when the API reaches GA and stabilizes.

52. **Refactor `doGet` into generic `doRequest` for method reuse**: When adding `DoDelete` to `ResourceListClient`, extract the shared HTTP logic (URL construction, auth headers, status checking) into a `doRequest(ctx, method, path)` method. `doGet` becomes a one-liner wrapper. This avoids duplicating auth header setup across GET/DELETE/POST methods.

53. **Cloudflare DELETE APIs may return 200 or 204**: Some Cloudflare v4 DELETE endpoints return 200 with `{success: true}`, others return 204 No Content. The `doRequest` status check should accept any 2xx status (`200 <= code < 300`) rather than checking `== 200` only. This prevents false failures on perfectly valid delete operations.

54. **Cache eviction after delete uses index-based splice**: After a successful delete via raw HTTP, evict the entry from the service's `cached []Resource` slice using index-based removal (`append(cached[:i], cached[i+1:]...)`). This matches the SDK-backed services' pattern of `delete(cachedRaw, id)` — both ensure subsequent `SearchItems()` and list rendering don't show stale entries.

### Resource Creation Popup (Phase A)

55. **ExtraArgs map extends CreateResourceCmd without breaking callers**: Adding `ExtraArgs map[string]string` to `CreateResourceCmd` is backward-compatible because Go zero-values the nil map, and `buildCreateArgs()` only iterates non-nil maps. Existing callers that create D1/KV/R2/Queue resources don't need changes — they simply don't set ExtraArgs.

56. **Dynamic extra fields via fieldDef slice**: Instead of hard-coding forms per resource type, the resource popup uses `extraFieldDefs(resourceType)` to return a slice of `fieldDef` structs (key, label, placeholder, required, validate). The Update and View functions iterate this slice generically, so adding a new resource type with custom fields requires zero changes to the popup's core logic — just a new case in `extraFieldDefs()`.

57. **Resource popup is tab-scoped, not global**: Unlike the old Ctrl+N binding wizard which was global (worked on Operations tab), the new resource popup is scoped to the Resources tab only. This aligns with the architectural separation: Resources tab owns creation/deletion, Configuration tab owns binding assignment. The popup handler is added to the handler chain alongside other popup handlers.

58. **Wrangler CLI flags use `--key=value` format**: When passing extra args to wrangler (e.g., `--dimensions=768`), the `=` format is preferred over `--key value` because wrangler's argument parser handles it consistently across all subcommands. The `buildCreateArgs()` function formats ExtraArgs as `--key=value` strings appended after the base command.

### Binding Wizard Elimination (Phase C)

59. **Wizard→picker conversion is minimal when the picker infra exists**: Converting D1/KV/R2/Queue from `Kind: "wizard"` to `Kind: "picker"` required only: (a) change the kind string, (b) add `pickerResourceType()` cases, (c) add `bindingFormFields()` definitions, (d) add `prefillBindingFormFromPicker()` cases, (e) add `buildBindingDef()` cases, and (f) extend `listBindingResourcesCmd()` to handle the new resource types via the existing service registry. The entire picker/form infrastructure from Phase B6 was reusable without modification.

60. **Service registry is the cleanest resource list source for binding pickers**: D1/KV/R2/Queue resources are already cached in the service registry. Using `m.registry.Get("D1").List()` for the binding picker avoids duplicating API calls that the old popup wizard made via a separate `listResourcesCmd()` code path. A single `registryServiceName()` helper maps picker resource types to registry service names, keeping the mapping centralized.

61. **Eliminating a popup removes ~5 integration points**: Removing the `bindings/` popup eliminated: (1) the `showBindings`/`bindingsPopup` fields on the root model, (2) the `handleBindingsMsg` handler in the handler chain, (3) the `updateBindings` input routing guard in `updateDashboard`, (4) the overlay entry in `viewDashboard`, (5) four helper functions (`listResourcesCmd`, `createResourceCmd`, `writeBindingCmd`, `updateBindings`), and (6) the import of the `bindings` package in two app files. This is the standard cost of each popup in the current architecture.

62. **Two-step pickers can be deferred**: Some binding types may need two-step resource selection (e.g., pick a parent resource → pick a child). A two-step picker would require a new `modeAddBindingPickerStep2`, additional state fields, and a second async fetch cycle. This can be added later as a UX enhancement without architectural changes.

63. **ResourceListClient must follow the auth method, not env var shortcuts**: Some APIs return 403 with OAuth tokens (same class of issue as lesson 27). Since `newResourceListClient()` creates a single client shared by Vectorize and Hyperdrive services (and binding pickers for mTLS), it must use the primary auth method's credentials — NOT the Global API Key from env vars (see lesson 76). Correct chain for OAuth: (1) `api_token_fallback`, (2) OAuth token as last resort. For API Key auth: use API Key directly. For API Token auth: use API Token directly.

### KV Data Explorer

64. **KV Values.Get returns raw `*http.Response`**: Unlike most SDK methods that return deserialized structs, `KV.Namespaces.Values.Get()` returns a raw HTTP response with `application/octet-stream` content. Must manually read the body, check UTF-8 validity, and handle binary values gracefully. Limit reads to 10KB to avoid OOM on large values.

65. **SDK does NOT URL-encode path parameters**: The cloudflare-go v6 SDK uses `fmt.Sprintf` to construct URL paths, meaning special characters in KV key names (e.g., `/`, `%`, `:`) will break the request. Must call `url.PathEscape(keyName)` before passing to `Values.Get()`.

66. **ReadWrite mode is the toggle for interactive data exploration**: Changing a service from `ReadOnly` to `ReadWrite` in the `ServiceEntry` registration enables the tab/enter interactive mode flow (same as D1). The `EnterInteractiveMsg` is the gateway — the app layer initializes the explorer and auto-loads data. No need for a separate "enter explorer" message type.

67. **Sequential value fetching is acceptable for small key sets**: Fetching values individually (1 API call per key, up to 20) after listing keys avoids the `BulkGet` union type issues (lesson #7 class). With a 60s context timeout and typical API latency of ~100ms per call, 20 sequential fetches take ~2s. Acceptable for an interactive explorer with explicit search.

### Local Emulator Integration

68. **D1 and KV CLI commands use different identifier types**: `wrangler d1 execute <DATABASE_NAME>` uses the `database_name` from the config's `[[d1_databases]]` section. `wrangler kv key list --binding=<BINDING>` uses the JS binding name. A single `BindingName` field on `LocalResource` stores whichever identifier the CLI command needs, not necessarily the JS binding name.

69. **`Binding.DisplayName` preserves CLI-facing names through the parser**: The `Binding` struct's `Name` is always the JS binding name, but `DisplayName` stores the resource-specific name (e.g., D1's `database_name`) needed by CLI commands. Without this, the `BindingName` → CLI argument mapping breaks for D1.

70. **Synchronous local resource discovery on dev session lifecycle**: `syncLocalResources()` re-parses wrangler configs synchronously on dev session start/stop. This is fast (<1ms per config) since it's just TOML/JSON parsing and binding extraction, not I/O. No need for async commands. The method scans all active `devSessions` and calls `m.detail.SetLocalResources()` to update the resource list.

71. **Local data persists after dev server stops**: Wrangler CLI `--local` commands read/write files in `.wrangler/state/` independently of the dev server process. When a dev server fails (non-zero exit), local entries remain visible and queryable because the session stays in `devSessions` with "failed" status. When the dev server exits cleanly (code 0), `cleanupDevSessionByKey` removes the session and `syncLocalResources` removes the local entries.

72. **Export shared formatting functions for cross-layer reuse**: `FormatASCIITable` in `service/d1.go` was initially unexported. The local emulator app-layer code needs to convert `wrangler.LocalD1QueryResult` → `service.D1QueryResult`, which requires the same table formatting. Exporting it avoids code duplication. The pattern: if a function is pure (no state, no side effects) and useful across package boundaries, export it.

73. **Local resources are always interactable regardless of service ReadWrite mode**: The `canInteract` check in the detail model uses `m.activeServiceMode() == ReadWrite || m.isLocalResource`. This means KV (which is ReadWrite remotely) and D1 (also ReadWrite) both work as expected. But even if a service were ReadOnly for remote resources, local entries would still be interactable — the local emulator always supports read/write operations.

74. **`SetManagedResources` sort must exclude local entries**: The managed/unmanaged sort in `SetManagedResources` was sorting the entire `m.resources` slice, which includes local entries prepended by `rebuildCombinedResources`. Local entries have IDs like `local:binding:path` that are never in the `managedIDs` set, so they sort to the "unmanaged" section — breaking the invariant that local entries occupy indices `0..localCount-1`. The fix: sort only `m.resources[localCount:]` (the remote portion). Also sync `remoteResources` with the sorted order so future `rebuildCombinedResources` calls preserve managed-first ordering.

75. **Pointer-receiver mutations + value-receiver Update = state desync trap**: When a pointer-receiver method (like `SetManagedResources`, `SetResources`, `RefreshResources`) mutates `cursor` or reorders `resources` without also updating correlated state (`isLocalResource`, `detailID`, `activeLocalResource`), the next value-receiver `Update()` call sees inconsistent state. The cursor points at one resource but the detail/local flags describe a different one. Any method that changes cursor position or resource ordering must either (a) also update the correlated preview state, or (b) trigger an `autoPreview()` to re-sync.

### SDK Env Var Leakage & Multi-Account Auth

76. **Cloudflare Go SDK v6 auto-reads env vars into every request**: `cloudflare.NewClient()` calls `DefaultClientOptions()` which reads `CLOUDFLARE_API_KEY`, `CLOUDFLARE_EMAIL`, `CLOUDFLARE_API_TOKEN` from environment variables and prepends them as default request options. When the app explicitly passes different auth (e.g., `WithAPIToken(oauthToken)`), the user's options are appended AFTER the env defaults. Since `WithAPIKey` sets `X-Auth-Key` header and `WithAPIToken` sets `Authorization: Bearer` header, **both auth mechanisms end up as HTTP headers on every request**. The Cloudflare API uses the API Key auth preferentially, which fails if the Key is scoped to a different account. Fix: after setting the intended auth, call `option.WithHeaderDel("X-Auth-Key")` and `option.WithHeaderDel("X-Auth-Email")` (for OAuth/Token auth) or `option.WithHeaderDel("authorization")` (for API Key auth) to strip the conflicting headers.

77. **Global API Key may be account-scoped, not user-scoped**: Contrary to the name, a Cloudflare "Global API Key" from the dashboard may only have permissions for the account it was created under. Never assume it works across all accounts the user has access to. In multi-account scenarios, always prefer user-level tokens (OAuth, scoped API Token) over the Global API Key. The previous codebase assumption that "Global API Key has all permissions" (used as a shortcut in `newResourceListClient()`, `getBuildsClient()`, `registerServices()` Access auth, and wrangler CLI passthrough) was incorrect and caused complete auth failure on account switch.

78. **Child process env var inheritance is dangerous**: `os.Environ()` passes ALL parent process env vars to child processes (wrangler CLI, npm, etc.). When the parent has `CLOUDFLARE_API_KEY` / `CLOUDFLARE_EMAIL` set, wrangler uses those instead of its own OAuth session, causing auth failures for non-matching accounts. Fix: add a `FilterEnv []string` field to command structs and strip the listed env var names from `os.Environ()` before passing to the child process. For OAuth auth, always filter `CLOUDFLARE_API_KEY` and `CLOUDFLARE_EMAIL`. The helper `wranglerFilterEnv()` centralizes this check.

79. **Env var isolation must be applied at every exec boundary**: There are 4 distinct exec boundaries in orangeshell: (1) `Runner.Start()` for streaming wrangler commands, (2) `CreateResource()` for synchronous wrangler commands, (3) AI provisioning functions (`WranglerDeploy`, `WranglerSecretPut`, `WranglerDelete`), and (4) local emulator functions (`ExecuteLocalD1Query`, `ListLocalKVKeys`, etc.). Categories 1-3 need env var filtering. Category 4 uses `--local` (no API calls) and is safe without filtering. Missing any exec boundary causes silent auth failures that only manifest on account switch.

### Per-Account Fallback Tokens

80. **Single fallback token breaks multi-account**: The original `api_token_fallback` was a single string scoped to one account. When switching accounts, the token lacked permissions for the new account, causing 403 on Access/Builds APIs. Fix: replace with `FallbackTokens map[string]string` (`[fallback_tokens]` TOML table) mapping `accountID → token`. Auto-provisioning now runs per-account, and each token is stored separately.

81. **Per-session toast deduplication via map**: The `restrictedToastShown map[string]bool` on the root model tracks which account IDs have shown the "restricted mode" toast during the current session. Without this, switching between two restricted accounts would show the toast on every switch, which is annoying. The map is session-scoped (not persisted) — restarting the app shows the toast once more for each restricted account.

## General Design Considerations
### Design-Patterns
1. The Elm Architecture (MVU)
Bubble Tea is heavily based on the Model-View-Update (MVU) pattern. Every component in a large application should strictly adhere to this lifecycle:

Model: The centralized state of the application.

Update: A pure (or purely synchronous) function that takes a tea.Msg and the current state, returning a new state and a tea.Cmd for side effects.

View: A pure function that renders the current state as a string using tools like lipgloss.

2. Hierarchical Model Tree (Nested Models)
For large applications, do not keep all state in a single, flat root struct. Instead, compose your root model of smaller, independent sub-models (e.g., List, Viewport, StatusBar).

Routing: The root Update function delegates messages to the sub-models' Update functions.

Rendering: The root View function stitches together the string outputs of the sub-models' View functions.

3. Model Stack Architecture (View Switching)
When dealing with distinct application screens (e.g., Login, Dashboard, Settings), a deeply nested tree becomes cumbersome. Instead, use a Stack-based approach:

Maintain a state variable (e.g., type sessionState int) in the main model.

Depending on the active state, route incoming messages only to the currently active sub-model, and return only that sub-model's view.

4. Message-Driven Communication
To keep components decoupled, child components should never reference their parents. Instead, child components return custom tea.Msg structs via tea.Cmd. The parent model listens for these specific message types in its Update loop and updates its own state or triggers a view change accordingly.

5. Zone-Based Mouse Routing (Bubblezone)
Using lrstanley/bubblezone abstracts away the complex math of calculating relative X/Y coordinates in the terminal.

Pattern: Tag child views with unique string IDs using zone.Mark("unique_id", childViewString).

In the parent's Update function, intercept tea.MouseMsg and use zone.Get("unique_id").InBounds(msg) to determine which child component was clicked, then route the message exclusively to that child.

### Best-Practices
1. Keep the Event Loop Fast (Asynchronous Commands)
The Update function must return immediately. Offload all I/O, database queries, network requests, and heavy computations to a tea.Cmd.

How: Return a function matching the tea.Cmd signature (func() tea.Msg) that executes the heavy task and returns a custom tea.Msg containing the result.

2. Top-Level Zone Scanning
For bubblezone to work effectively, you must wrap the entire output of your root model's View() method in zone.Scan().

Why: This allows the zone manager to parse all embedded markers, calculate their absolute terminal offsets, and strip the marker ANSI sequences before handing the final string to Bubble Tea to render.

3. Propagate WindowSizeMsg Downward
Terminal resizing can break complex layouts.

How: Always catch tea.WindowSizeMsg at the root model. Calculate the remaining width/height after subtracting root-level padding/borders, and explicitly pass the constrained dimensions down to child models either via custom setter methods (e.g., child.SetSize(w, h)) or by passing the message down.

4. Unique and Deterministic Zone IDs
When rendering lists or dynamic components with mouse support, ensure bubblezone IDs are strictly unique.

How: Combine component names with database IDs or slice indexes (e.g., zone.Mark(fmt.Sprintf("item_%d", item.ID), view)). Overlapping or duplicated IDs will cause unpredictable mouse routing.

5. Use Lipgloss for Layouts, Not String Math
Never manually calculate spaces, tabs, or line breaks to align components. Use charmbracelet/lipgloss to define declarative borders, margins, padding, and alignments (e.g., lipgloss.JoinHorizontal).

### Anti-Patterns
1. Blocking the Update Loop
The Problem: Using time.Sleep or synchronous network calls inside Update().

The Consequence: The entire TUI freezes. Keystrokes and mouse events back up in the channel, creating massive input lag when the loop finally unblocks.

2. Manual Coordinate Math for Mouse Events
The Problem: Trying to calculate if a mouse click hit a specific button by manually tracking msg.X and msg.Y against hardcoded line numbers or calculated offsets.

The Consequence: Highly brittle code that breaks immediately when the terminal is resized, borders are added, or layouts change. Always use bubblezone for this.

3. Monolithic Update Functions
The Problem: A giant switch msg := msg.(type) block in the root model that handles the business logic for every single input field, list, and button in the application.

The Consequence: Unmaintainable spaghetti code. Delegate! If a keystroke belongs to the search bar, pass it to searchBar.Update(msg).

4. Global Mutable State
The Problem: Storing application data, active user info, or UI state in global variables instead of within the component's Model struct.

The Consequence: Breaks the pure functional nature of TEA, introduces race conditions when using goroutines (via tea.Cmd), and makes end-to-end testing with tools like teatest impossible.

5. Pointer Receivers vs. Value Receivers Inconsistency
The Problem: Arbitrarily mixing pointer receivers (func (m *Model) Update(...)) and value receivers (func (m Model) Update(...)) throughout your components.

The Consequence: Accidental state mutations or lost state updates. Recommendation: Stick to value receivers to enforce immutability (returning a modified copy) unless dealing with a massive struct where pointer receivers are required for performance—in which case, document the mutation boundaries clearly.
