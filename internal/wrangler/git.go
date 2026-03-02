package wrangler

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// GitInfo holds local git repository metadata detected from the filesystem.
// Detection is file-based (reads .git/HEAD and .git/config) — no exec("git") required.
type GitInfo struct {
	IsRepo       bool   // true if the directory is inside a git repository
	RepoRoot     string // absolute path to the repository root (parent of .git/)
	Branch       string // current branch name (or "HEAD" if detached)
	RemoteURL    string // origin remote URL (first remote if no origin)
	RemoteName   string // name of the remote ("origin" or first found)
	ProviderType string // "github", "gitlab", or "" (unknown)
	Owner        string // repository owner (org or user)
	RepoName     string // repository name (without .git suffix)
	OwnerID      string // numeric provider account/user ID (resolved via API)
	RepoID       string // numeric provider repository ID (resolved via API)
}

// DetectGit walks up from dir looking for a .git/ directory and extracts
// repository metadata by reading .git/HEAD and .git/config directly.
func DetectGit(dir string) *GitInfo {
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return &GitInfo{}
	}

	// Walk up to find .git directory
	repoRoot := findGitRoot(absDir)
	if repoRoot == "" {
		return &GitInfo{}
	}

	info := &GitInfo{
		IsRepo:   true,
		RepoRoot: repoRoot,
	}

	gitDir := filepath.Join(repoRoot, ".git")

	// Read current branch from .git/HEAD
	info.Branch = readGitBranch(gitDir)

	// Read remote URL from .git/config
	info.RemoteName, info.RemoteURL = readGitRemote(gitDir)

	// Parse provider/owner/repo from the remote URL
	if info.RemoteURL != "" {
		info.ProviderType, info.Owner, info.RepoName = parseRemoteURL(info.RemoteURL)
	}

	return info
}

// findGitRoot walks up from dir looking for a .git/ directory.
// Returns the parent directory of .git, or "" if not found.
func findGitRoot(dir string) string {
	for {
		gitPath := filepath.Join(dir, ".git")
		if fi, err := os.Stat(gitPath); err == nil && fi.IsDir() {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "" // reached filesystem root
		}
		dir = parent
	}
}

// readGitBranch reads the current branch from .git/HEAD.
// Returns the branch name, or "HEAD" if detached.
func readGitBranch(gitDir string) string {
	data, err := os.ReadFile(filepath.Join(gitDir, "HEAD"))
	if err != nil {
		return ""
	}
	content := strings.TrimSpace(string(data))

	// Symbolic ref: "ref: refs/heads/<branch>"
	if strings.HasPrefix(content, "ref: refs/heads/") {
		return strings.TrimPrefix(content, "ref: refs/heads/")
	}

	// Detached HEAD (raw SHA)
	if len(content) >= 7 {
		return "HEAD"
	}
	return ""
}

// readGitRemote reads the first remote URL from .git/config.
// Prefers "origin" if it exists, otherwise returns the first remote found.
func readGitRemote(gitDir string) (remoteName, remoteURL string) {
	f, err := os.Open(filepath.Join(gitDir, "config"))
	if err != nil {
		return "", ""
	}
	defer f.Close()

	var (
		inRemote    bool
		currentName string
		firstRemote string
		firstURL    string
	)

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())

		// Section header: [remote "origin"]
		if strings.HasPrefix(line, "[remote \"") && strings.HasSuffix(line, "\"]") {
			inRemote = true
			currentName = line[len("[remote \"") : len(line)-len("\"]")]
			continue
		}

		// Any other section header ends the remote block
		if strings.HasPrefix(line, "[") {
			inRemote = false
			currentName = ""
			continue
		}

		// Inside a remote section, look for url = ...
		if inRemote && strings.HasPrefix(line, "url") {
			parts := strings.SplitN(line, "=", 2)
			if len(parts) == 2 {
				url := strings.TrimSpace(parts[1])
				if currentName == "origin" {
					return "origin", url
				}
				if firstRemote == "" {
					firstRemote = currentName
					firstURL = url
				}
			}
		}
	}

	return firstRemote, firstURL
}

// SSH pattern: git@github.com:owner/repo.git
var sshRemotePattern = regexp.MustCompile(`^git@([^:]+):([^/]+)/(.+?)(?:\.git)?$`)

// HTTPS pattern: https://github.com/owner/repo.git
var httpsRemotePattern = regexp.MustCompile(`^https?://([^/]+)/([^/]+)/(.+?)(?:\.git)?$`)

// parseRemoteURL extracts provider type, owner, and repo name from a git remote URL.
// Handles both SSH (git@github.com:owner/repo.git) and HTTPS (https://github.com/owner/repo) formats.
func parseRemoteURL(url string) (providerType, owner, repoName string) {
	// Try SSH format first
	if m := sshRemotePattern.FindStringSubmatch(url); m != nil {
		return detectProvider(m[1]), m[2], m[3]
	}

	// Try HTTPS format
	if m := httpsRemotePattern.FindStringSubmatch(url); m != nil {
		return detectProvider(m[1]), m[2], m[3]
	}

	return "", "", ""
}

// detectProvider maps a hostname to a provider type string.
func detectProvider(host string) string {
	host = strings.ToLower(host)
	switch {
	case strings.Contains(host, "github"):
		return "github"
	case strings.Contains(host, "gitlab"):
		return "gitlab"
	default:
		return ""
	}
}

// ResolveGitIDs queries the GitHub or GitLab API to resolve the Owner and
// RepoName strings to their numeric IDs (OwnerID, RepoID). The Cloudflare
// Builds API requires numeric IDs, not string names.
// This is a no-op if the provider is unsupported or the IDs are already set.
func (g *GitInfo) ResolveGitIDs(ctx context.Context) error {
	if g.OwnerID != "" && g.RepoID != "" {
		return nil // already resolved
	}
	switch g.ProviderType {
	case "github":
		return g.resolveGitHubIDs(ctx)
	case "gitlab":
		return g.resolveGitLabIDs(ctx)
	default:
		return fmt.Errorf("unsupported provider: %s", g.ProviderType)
	}
}

// resolveGitHubIDs fetches numeric user/org ID and repo ID from the GitHub API.
// These are public endpoints that don't require authentication.
func (g *GitInfo) resolveGitHubIDs(ctx context.Context) error {
	client := &http.Client{Timeout: 10 * time.Second}

	// Resolve repo (includes owner info)
	path := fmt.Sprintf("https://api.github.com/repos/%s/%s", g.Owner, g.RepoName)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/vnd.github.v3+json")

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("GitHub API request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("reading GitHub response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GitHub API returned %d for %s/%s", resp.StatusCode, g.Owner, g.RepoName)
	}

	var repo struct {
		ID    int64 `json:"id"`
		Owner struct {
			ID int64 `json:"id"`
		} `json:"owner"`
	}
	if err := json.Unmarshal(body, &repo); err != nil {
		return fmt.Errorf("parsing GitHub response: %w", err)
	}

	g.OwnerID = fmt.Sprintf("%d", repo.Owner.ID)
	g.RepoID = fmt.Sprintf("%d", repo.ID)
	return nil
}

// resolveGitLabIDs fetches the numeric project ID from the GitLab API.
// For GitLab, the project ID serves as both the repo identifier.
// The namespace (owner) ID is extracted from the project metadata.
func (g *GitInfo) resolveGitLabIDs(ctx context.Context) error {
	client := &http.Client{Timeout: 10 * time.Second}

	// GitLab uses URL-encoded "owner/repo" as the project identifier
	projectPath := fmt.Sprintf("%s/%s", g.Owner, g.RepoName)
	encodedPath := strings.ReplaceAll(projectPath, "/", "%2F")

	path := fmt.Sprintf("https://gitlab.com/api/v4/projects/%s", encodedPath)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, path, nil)
	if err != nil {
		return err
	}

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("GitLab API request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("reading GitLab response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GitLab API returned %d for %s", resp.StatusCode, projectPath)
	}

	var project struct {
		ID        int64 `json:"id"`
		Namespace struct {
			ID int64 `json:"id"`
		} `json:"namespace"`
	}
	if err := json.Unmarshal(body, &project); err != nil {
		return fmt.Errorf("parsing GitLab response: %w", err)
	}

	g.OwnerID = fmt.Sprintf("%d", project.Namespace.ID)
	g.RepoID = fmt.Sprintf("%d", project.ID)
	return nil
}
