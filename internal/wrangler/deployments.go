package wrangler

import (
	"encoding/json"
	"fmt"
	"time"
)

// Deployment represents a single deployment from `wrangler deployments list --json`.
type Deployment struct {
	ID          string
	Source      string
	AuthorEmail string
	Message     string // annotations["workers/message"]
	TriggeredBy string // annotations["workers/triggered_by"]
	Versions    []DeploymentVersion
	CreatedOn   time.Time
}

// DeploymentVersion represents a version within a deployment.
type DeploymentVersion struct {
	VersionID  string
	Percentage float64
}

// rawDeployment matches the JSON shape from `wrangler deployments list --json`.
type rawDeployment struct {
	ID          string            `json:"id"`
	Source      string            `json:"source"`
	Strategy    string            `json:"strategy"`
	AuthorEmail string            `json:"author_email"`
	Annotations map[string]string `json:"annotations"`
	Versions    []struct {
		VersionID  string  `json:"version_id"`
		Percentage float64 `json:"percentage"`
	} `json:"versions"`
	CreatedOn string `json:"created_on"`
}

// ParseDeploymentsJSON parses the JSON output of `wrangler deployments list --json`.
// Returns deployments sorted by the order they appear in the JSON (newest first).
func ParseDeploymentsJSON(data []byte) ([]Deployment, error) {
	var raw []rawDeployment
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("failed to parse deployments JSON: %w", err)
	}

	deployments := make([]Deployment, 0, len(raw))
	for _, r := range raw {
		createdOn, err := time.Parse(time.RFC3339Nano, r.CreatedOn)
		if err != nil {
			createdOn, err = time.Parse(time.RFC3339, r.CreatedOn)
			if err != nil {
				createdOn = time.Time{}
			}
		}

		versions := make([]DeploymentVersion, len(r.Versions))
		for i, v := range r.Versions {
			versions[i] = DeploymentVersion{
				VersionID:  v.VersionID,
				Percentage: v.Percentage,
			}
		}

		deployments = append(deployments, Deployment{
			ID:          r.ID,
			Source:      r.Source,
			AuthorEmail: r.AuthorEmail,
			Message:     r.Annotations["workers/message"],
			TriggeredBy: r.Annotations["workers/triggered_by"],
			Versions:    versions,
			CreatedOn:   createdOn,
		})
	}

	return deployments, nil
}

// VersionHistoryEntry is a merged view of a version and its deployment context,
// suitable for display in the version history table.
type VersionHistoryEntry struct {
	VersionID    string
	DeploymentID string
	Number       int
	Source       string // display name: "wrangler", "dashboard", "CI", "api"
	RawSource    string // original source value from the API
	Message      string // deployment message or "—"
	AuthorEmail  string
	CreatedOn    time.Time
	IsLive       bool    // true if this version is in the most recent deployment
	Percentage   float64 // traffic percentage (only meaningful if IsLive)
	HasBuildLog  bool    // true if source is "workersci" (Phase 2: build log available)

	// Phase 2: populated by Workers Builds API
	GitBranch   string
	GitCommit   string
	CommitMsg   string
	BuildID     string
	BuildStatus string // "success", "failed", "canceled", "running"
}

// ShortID returns the first 8 characters of the version ID.
func (e VersionHistoryEntry) ShortID() string {
	if len(e.VersionID) >= 8 {
		return e.VersionID[:8]
	}
	return e.VersionID
}

// RelativeTime returns a human-readable relative time string.
func (e VersionHistoryEntry) RelativeTime() string {
	d := time.Since(e.CreatedOn)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		m := int(d.Minutes())
		if m == 1 {
			return "1m ago"
		}
		return fmt.Sprintf("%dm ago", m)
	case d < 24*time.Hour:
		h := int(d.Hours())
		if h == 1 {
			return "1h ago"
		}
		return fmt.Sprintf("%dh ago", h)
	default:
		days := int(d.Hours() / 24)
		if days == 1 {
			return "1d ago"
		}
		return fmt.Sprintf("%dd ago", days)
	}
}

// DisplaySource returns a human-readable source name.
func (e VersionHistoryEntry) DisplaySource() string {
	switch e.RawSource {
	case "wrangler":
		return "wrangler"
	case "dash", "dash_template":
		return "dashboard"
	case "workersci":
		return "CI"
	case "api":
		return "API"
	case "terraform":
		return "terraform"
	case "quick_editor":
		return "editor"
	case "playground":
		return "playground"
	default:
		if e.RawSource == "" {
			return "unknown"
		}
		return e.RawSource
	}
}

// DisplayMessage returns the message column text.
// In Phase 2, CI entries show the commit message from the Builds API.
func (e VersionHistoryEntry) DisplayMessage() string {
	if e.CommitMsg != "" {
		return e.CommitMsg
	}
	if e.Message != "" && e.Message != "Automatic deployment on upload." {
		return e.Message
	}
	return "—"
}

// DisplayAuthor returns a shortened author email (just the local part).
func (e VersionHistoryEntry) DisplayAuthor() string {
	for i, c := range e.AuthorEmail {
		if c == '@' {
			return e.AuthorEmail[:i]
		}
	}
	return e.AuthorEmail
}

// BuildVersionHistory merges version and deployment data into a unified
// history suitable for display. Versions are the primary list; deployment
// data enriches each version with deployment context and live status.
func BuildVersionHistory(versions []Version, deployments []Deployment) []VersionHistoryEntry {
	// Build lookup: version_id -> latest deployment referencing it.
	// Deployments from wrangler are in chronological order (oldest first),
	// so later entries overwrite earlier ones and the last write wins.
	type deplInfo struct {
		deploymentID string
		source       string
		message      string
		createdOn    time.Time
	}
	deplByVersion := make(map[string]deplInfo)
	for _, d := range deployments {
		for _, v := range d.Versions {
			deplByVersion[v.VersionID] = deplInfo{
				deploymentID: d.ID,
				source:       d.Source,
				message:      d.Message,
				createdOn:    d.CreatedOn,
			}
		}
	}

	// Determine which version(s) are currently live (from the most recent deployment).
	// Deployments are chronological (oldest first), so the last entry is the current one.
	liveVersions := make(map[string]float64)
	if len(deployments) > 0 {
		latest := deployments[len(deployments)-1]
		for _, v := range latest.Versions {
			liveVersions[v.VersionID] = v.Percentage
		}
	}

	// Versions from wrangler are in chronological order (oldest first).
	// Build entries in that order, then reverse so newest appears at the top.
	entries := make([]VersionHistoryEntry, 0, len(versions))
	for _, v := range versions {
		source := v.Source
		message := "—"
		deploymentID := ""

		if di, ok := deplByVersion[v.ID]; ok {
			deploymentID = di.deploymentID
			// Use the deployment source if it's more specific
			if di.source != "" {
				source = di.source
			}
			if di.message != "" {
				message = di.message
			}
		}

		// Override message for purely manual deployments
		if message == "Automatic deployment on upload." {
			message = "Auto-deploy"
		}

		pct, isLive := liveVersions[v.ID]

		entries = append(entries, VersionHistoryEntry{
			VersionID:    v.ID,
			DeploymentID: deploymentID,
			Number:       v.Number,
			Source:       displaySource(source),
			RawSource:    source,
			Message:      message,
			AuthorEmail:  v.AuthorEmail,
			CreatedOn:    v.CreatedOn,
			IsLive:       isLive,
			Percentage:   pct,
			HasBuildLog:  false, // set by EnrichWithBuilds after querying the Builds API
		})
	}

	// Reverse so newest is first
	for i, j := 0, len(entries)-1; i < j; i, j = i+1, j-1 {
		entries[i], entries[j] = entries[j], entries[i]
	}

	return entries
}

// EnrichWithBuilds updates version history entries with git metadata from
// the Workers Builds API. The builds map is keyed by version_id.
// Any version that has a matching build gets HasBuildLog set to true,
// regardless of what the wrangler source metadata says (Workers Builds
// uses `wrangler deploy` internally, so source may say "wrangler" even
// for CI-deployed versions).
func EnrichWithBuilds(entries []VersionHistoryEntry, builds map[string]BuildInfo) {
	for i := range entries {
		if b, ok := builds[entries[i].VersionID]; ok {
			entries[i].GitBranch = b.Branch
			entries[i].GitCommit = b.CommitHash
			entries[i].CommitMsg = b.CommitMessage
			entries[i].BuildID = b.BuildUUID
			entries[i].BuildStatus = b.BuildOutcome
			entries[i].HasBuildLog = true
			// Override source display to include branch
			if b.Branch != "" {
				entries[i].Source = "CI (" + b.Branch + ")"
			} else {
				entries[i].Source = "CI"
			}
			// Override raw source so display logic treats it as CI
			entries[i].RawSource = "workersci"
		}
	}
}

// BuildInfo is a simplified representation of a build from the Workers Builds API,
// used to enrich version history entries. Conversion from api.BuildResult happens
// in the app layer to avoid a circular dependency between api and wrangler packages.
type BuildInfo struct {
	BuildUUID     string
	BuildOutcome  string
	Branch        string
	CommitHash    string
	CommitMessage string
	Author        string
	RepoName      string
	ProviderType  string
}

func displaySource(source string) string {
	switch source {
	case "wrangler":
		return "wrangler"
	case "dash", "dash_template":
		return "dashboard"
	case "workersci":
		return "CI"
	case "api":
		return "API"
	case "terraform":
		return "terraform"
	case "quick_editor":
		return "editor"
	case "playground":
		return "playground"
	default:
		if source == "" {
			return "unknown"
		}
		return source
	}
}
