# orangeshell

A terminal UI for managing your Cloudflare developer platform resources.

![Go](https://img.shields.io/badge/Go-1.23+-00ADD8?style=flat&logo=go&logoColor=white)
![Cloudflare](https://img.shields.io/badge/Cloudflare-Developer%20Platform-F38020?style=flat&logo=cloudflare&logoColor=white)

## Overview

orangeshell is a TUI (terminal user interface) that gives you a fast, keyboard-driven way to browse and inspect resources across Cloudflare's developer platform — Workers, KV, R2, and more — without leaving your terminal.

Built with [Bubble Tea](https://github.com/charmbracelet/bubbletea) and the [Cloudflare Go SDK](https://github.com/cloudflare/cloudflare-go).

## Features

- **Service browser** — Navigate Workers, KV Namespaces, and R2 Buckets from a single dashboard
- **Resource detail** — Drill into any resource to see its configuration and metadata
- **Quick search** — `Ctrl+K` to fuzzy search across all services
- **Session cache** — Instant service switching with background refresh every 30s
- **Multiple auth methods** — API Key + Email, API Token, or OAuth PKCE
- **First-run setup wizard** — Guided configuration on first launch

## Getting Started

### Prerequisites

- Go 1.23+
- A Cloudflare account with an API token or API key

### Install

```bash
go install github.com/oarafat/orangeshell@latest
```

Or build from source:

```bash
git clone https://github.com/oarafat/orangeshell.git
cd orangeshell
go build -o orangeshell .
```

### Run

```bash
./orangeshell
```

On first launch, the setup wizard will walk you through authentication and account selection. Configuration is stored in `~/.orangeshell/config.toml`.

## Keyboard Shortcuts

| Key | Action |
|---|---|
| `j` / `k` | Navigate up/down |
| `Tab` | Switch focus between sidebar and detail panel |
| `Enter` | View resource detail |
| `Esc` | Go back |
| `Ctrl+K` or `/` | Open search |
| `q` / `Ctrl+C` | Quit |

## Supported Services

| Service | Status |
|---|---|
| Workers | Available |
| KV | Available |
| R2 | Available |
| D1 | Planned |
| Pages | Planned |
| Queues | Planned |
| Hyperdrive | Planned |
| Durable Objects | Planned |

## License

MIT
