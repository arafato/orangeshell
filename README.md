# orangeshell

A terminal UI for managing your Cloudflare Workers — built around the wrangler workflow with first-class monorepo support.

![Go](https://img.shields.io/badge/Go-1.25+-00ADD8?style=flat&logo=go&logoColor=white)
![Cloudflare](https://img.shields.io/badge/Cloudflare-Developer%20Platform-F38020?style=flat&logo=cloudflare&logoColor=white)

## Overview

orangeshell is a TUI that puts your wrangler project at the center. Point it at a directory with a `wrangler.jsonc` (or `.json` / `.toml`) and it shows you everything: environments, bindings, deployments, version splits, and live logs — all without leaving your terminal.

Drop it into a monorepo with multiple Workers and it discovers every project automatically, giving you a unified view across all of them.

Built with [Bubble Tea](https://github.com/charmbracelet/bubbletea) and the [Cloudflare Go SDK](https://github.com/cloudflare/cloudflare-go).

## Features

### Wrangler-native workflow

orangeshell reads your wrangler config directly. Environments, bindings, routes, and variables are displayed per-env with no extra configuration. Run `deploy`, `dev`, `versions list`, and other wrangler commands straight from the action menu (`Ctrl+P`).

### Monorepo support

When orangeshell detects multiple wrangler configs in the directory tree, it switches to a project list view. Drill into any project to see its full config, or stay on the list to get a bird's-eye view of deployment status across all Workers.

### Live log tailing

Press `t` to stream live logs from any Worker via the Cloudflare tail API. Logs are colored by level (request, log, warn, error) and displayed in a dedicated console pane.

In monorepo mode, **Parallel Tail** lets you stream logs from all Workers in an environment simultaneously in a 2-column grid — useful for debugging cross-service flows.

### Deployment visibility

Each environment shows its active deployment: version IDs, traffic split percentages, and workers.dev URLs (rendered as clickable terminal hyperlinks). "Currently not deployed" is surfaced clearly so you know exactly what's live.

### Version management

Deploy a specific version at 100% or set up gradual deployments with custom traffic splits — all from the version picker overlay.

### Service dashboard

Browse Workers, KV Namespaces, R2 Buckets, and D1 Databases from a unified dashboard. Drill into any resource to inspect its configuration, and cross-navigate between Workers and their bindings.

### Multi-account

Switch between Cloudflare accounts instantly with `[` / `]`. Deployment data is cached per-account for instant restore when switching back.

### D1 SQL console

Run SQL queries against D1 databases directly from the detail view. Schema is auto-loaded and refreshed after mutations.

## Install

### Homebrew

```bash
brew tap arafato/tap
brew install orangeshell
```

### Download binary

Grab the latest binary for your platform from the [Releases page](https://github.com/arafato/orangeshell/releases).

### Run

```bash
orangeshell
```

On first launch, the setup wizard walks you through authentication (API Token, API Key + Email, or OAuth) and account selection. Configuration is stored in `~/.orangeshell/config.toml`.

## License

MIT
