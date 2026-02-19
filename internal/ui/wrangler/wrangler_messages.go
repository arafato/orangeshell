package wrangler

import (
	"fmt"
	"time"

	wcfg "github.com/oarafat/orangeshell/internal/wrangler"
)

// ProjectBoxZoneID returns the bubblezone marker ID for a monorepo project box.
func ProjectBoxZoneID(idx int) string {
	return fmt.Sprintf("proj-box-%d", idx)
}

// versionCacheTTL is how long fetched versions remain valid before re-fetching.
const versionCacheTTL = 30 * time.Second

// NavigateMsg is sent when the user selects a navigable binding in the wrangler view.
// The parent (app.go) handles this to cross-link to the dashboard detail view.
type NavigateMsg struct {
	ServiceName string // "KV", "R2", "D1", "Workers"
	ResourceID  string // namespace_id, bucket_name, database_id, script_name
}

// NavigateToBindingMsg is sent when the user presses enter on a binding that
// does not have a corresponding Resources tab service (e.g. AI, Vectorize, Workflow).
// The parent navigates to the Configuration tab → Bindings category with the binding highlighted.
type NavigateToBindingMsg struct {
	ConfigPath  string
	EnvName     string
	BindingName string
}

// DirBrowserMode indicates what the directory browser was opened for.
type DirBrowserMode int

const (
	DirBrowserModeOpen   DirBrowserMode = iota // Browse for an existing project
	DirBrowserModeCreate                       // Browse for a location to create a new project
)

// EmptyMenuSelectMsg is sent when the user selects an option from the empty-state menu
// (no wrangler config found). The parent (app.go) handles this.
type EmptyMenuSelectMsg struct {
	Action string // "create_project" or "open_project"
}

// DeleteBindingRequestMsg is sent when the user presses 'd' on a binding inside an env box.
// The parent (app.go) handles this to show the delete confirmation popup.
type DeleteBindingRequestMsg struct {
	ConfigPath  string
	EnvName     string
	BindingName string
	BindingType string
	WorkerName  string
}

// ShowEnvVarsMsg is sent when the user presses enter on the "Environment Variables" item
// inside an env box. The parent (app.go) handles this to open the envvars view.
type ShowEnvVarsMsg struct {
	ConfigPath  string
	EnvName     string
	ProjectName string
}

// ShowTriggersMsg is sent when the user presses enter on the "Triggers" item
// inside an env box. The parent (app.go) handles this to open the triggers view.
type ShowTriggersMsg struct {
	ConfigPath  string
	ProjectName string
}

// OpenURLMsg is sent when the user clicks a URL in the wrangler view.
// The parent (app.go) handles this to open the URL in the default browser.
type OpenURLMsg struct {
	URL string
}

// ConfigLoadedMsg is sent when a wrangler config has been scanned and parsed.
type ConfigLoadedMsg struct {
	Config *wcfg.WranglerConfig
	Path   string
	Err    error
}

// ActionMsg is sent when the user triggers a wrangler action from Ctrl+P.
// The parent (app.go) handles this to start the command runner.
type ActionMsg struct {
	Action  string // "deploy", "rollback", "versions list", "deployments status"
	EnvName string // environment name (empty or "default" for top-level)
}

// CmdOutputMsg carries a line of output from a running wrangler command.
type CmdOutputMsg struct {
	RunnerKey string // "projectName:envName" — identifies which runner produced this output
	IsDevCmd  bool   // true for dev server output, false for deploy/delete/etc.
	Line      wcfg.OutputLine
}

// CmdDoneMsg signals that a wrangler command has finished.
type CmdDoneMsg struct {
	RunnerKey string // "projectName:envName" — identifies which runner finished
	IsDevCmd  bool   // true for dev server, false for deploy/delete/etc.
	Result    wcfg.RunResult
}

// LoadConfigPathMsg is sent when the user enters a directory path to load a wrangler config from.
// The parent (app.go) handles this by scanning the given path.
type LoadConfigPathMsg struct {
	Path string
}

// VersionsFetchedMsg delivers parsed version data from `wrangler versions list --json`.
// The parent (app.go) sends this after a background fetch completes.
type VersionsFetchedMsg struct {
	Versions []wcfg.Version
	Err      error
}

// ProjectsDiscoveredMsg is sent when monorepo discovery finds multiple projects.
type ProjectsDiscoveredMsg struct {
	Projects []wcfg.ProjectInfo
	RootName string // CWD basename (monorepo name)
	RootDir  string // absolute path to the monorepo root directory
}

// ProjectDeploymentLoadedMsg delivers deployment data for a single project+env.
type ProjectDeploymentLoadedMsg struct {
	AccountID    string // for staleness check on account switch
	ProjectIndex int
	EnvName      string
	ScriptName   string // worker script name (cache key)
	Deployment   *DeploymentDisplay
	Subdomain    string
	Err          error
}

// EnvDeploymentLoadedMsg delivers deployment data for a single-project env.
type EnvDeploymentLoadedMsg struct {
	AccountID  string // for staleness check on account switch
	EnvName    string
	ScriptName string // worker script name (cache key)
	Deployment *DeploymentDisplay
	Subdomain  string
	Err        error
}

// TailStartMsg requests the app to start tailing a worker from the wrangler view.
type TailStartMsg struct {
	ScriptName string
}

// TailStoppedMsg signals that the wrangler-initiated tail was stopped.
type TailStoppedMsg struct{}

// projectEntry holds data for a single project in the monorepo list.
type projectEntry struct {
	box        ProjectBox
	config     *wcfg.WranglerConfig
	configPath string
}

// DevBadge holds dev server status information for display in project/env boxes.
type DevBadge struct {
	Kind   string // "local" or "remote"
	Port   string // e.g. "8787"
	Status string // "starting", "running", "failed"
}

// WorkerInfo describes a single worker resolved from a wrangler config.
// Used by the Monitoring tab to build the worker tree.
type WorkerInfo struct {
	ProjectName string // wrangler project name (for grouping)
	EnvName     string // environment name (e.g. "default", "staging")
	ScriptName  string // resolved worker script name
}
