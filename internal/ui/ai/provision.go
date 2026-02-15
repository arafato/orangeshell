package ai

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// ProvisionConfig holds the inputs needed for AI Worker provisioning.
type ProvisionConfig struct {
	AccountID string // Cloudflare account ID
	APIToken  string // Cloudflare API token (for wrangler deploy)
	APIKey    string // Cloudflare API key (alternative auth)
	Email     string // Cloudflare email (for API key auth)
}

// ProvisionResult holds the outputs of a successful provisioning.
type ProvisionResult struct {
	WorkerURL string // e.g. https://orangeshell-ai.{subdomain}.workers.dev
	Secret    string // the generated AUTH_SECRET
}

// gitHubTreeEntry represents a file entry from the GitHub API tree response.
type gitHubTreeEntry struct {
	Path string `json:"path"`
	Type string `json:"type"` // "blob" or "tree"
	URL  string `json:"url"`  // API URL for blob content
}

// gitHubTreeResponse is the response from GitHub's /git/trees API.
type gitHubTreeResponse struct {
	Tree []gitHubTreeEntry `json:"tree"`
}

// gitHubBlobResponse is the response from GitHub's /git/blobs API.
type gitHubBlobResponse struct {
	Content  string `json:"content"`
	Encoding string `json:"encoding"` // "base64"
}

const (
	// GitHub repo coordinates for the AI Worker template.
	templateRepo = "arafato/orangeshell"
	templatePath = "templates/ai-worker"
	templateRef  = "main"

	// Worker name pattern — the account ID prefix ensures uniqueness.
	workerNamePrefix = "orangeshell-ai"
)

// GenerateSecret creates a cryptographically random 32-byte secret, base64url-encoded.
func GenerateSecret() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("failed to generate secret: %w", err)
	}
	return base64.URLEncoding.EncodeToString(b), nil
}

// DownloadTemplate fetches the AI Worker template from GitHub and writes it
// to a temporary directory. Returns the path to the temp directory.
func DownloadTemplate(ctx context.Context) (string, error) {
	tmpDir, err := os.MkdirTemp("", "orangeshell-ai-*")
	if err != nil {
		return "", fmt.Errorf("failed to create temp dir: %w", err)
	}

	// Fetch the directory tree from GitHub API
	treeURL := fmt.Sprintf("https://api.github.com/repos/%s/git/trees/%s?recursive=1", templateRepo, templateRef)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, treeURL, nil)
	if err != nil {
		os.RemoveAll(tmpDir)
		return "", fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github.v3+json")
	req.Header.Set("User-Agent", "orangeshell")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		os.RemoveAll(tmpDir)
		return "", fmt.Errorf("failed to fetch GitHub tree: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		os.RemoveAll(tmpDir)
		return "", fmt.Errorf("GitHub API returned %d", resp.StatusCode)
	}

	var tree gitHubTreeResponse
	if err := json.NewDecoder(resp.Body).Decode(&tree); err != nil {
		os.RemoveAll(tmpDir)
		return "", fmt.Errorf("failed to decode tree response: %w", err)
	}

	// Filter entries under templates/ai-worker/ and download each blob
	for _, entry := range tree.Tree {
		if entry.Type != "blob" {
			continue
		}
		if !strings.HasPrefix(entry.Path, templatePath+"/") {
			continue
		}

		// Relative path within the template
		relPath := strings.TrimPrefix(entry.Path, templatePath+"/")
		destPath := filepath.Join(tmpDir, relPath)

		// Create parent directories
		if err := os.MkdirAll(filepath.Dir(destPath), 0755); err != nil {
			os.RemoveAll(tmpDir)
			return "", fmt.Errorf("failed to create dir for %s: %w", relPath, err)
		}

		// Download blob content
		content, err := fetchBlob(ctx, entry.URL)
		if err != nil {
			os.RemoveAll(tmpDir)
			return "", fmt.Errorf("failed to download %s: %w", relPath, err)
		}

		if err := os.WriteFile(destPath, content, 0644); err != nil {
			os.RemoveAll(tmpDir)
			return "", fmt.Errorf("failed to write %s: %w", relPath, err)
		}
	}

	return tmpDir, nil
}

// fetchBlob downloads a blob from the GitHub API and decodes its base64 content.
func fetchBlob(ctx context.Context, blobURL string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, blobURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github.v3+json")
	req.Header.Set("User-Agent", "orangeshell")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("blob API returned %d", resp.StatusCode)
	}

	var blob gitHubBlobResponse
	if err := json.NewDecoder(resp.Body).Decode(&blob); err != nil {
		return nil, fmt.Errorf("failed to decode blob: %w", err)
	}

	if blob.Encoding != "base64" {
		return nil, fmt.Errorf("unexpected blob encoding: %s", blob.Encoding)
	}

	// GitHub base64 content includes newlines — strip them
	cleaned := strings.ReplaceAll(blob.Content, "\n", "")
	return base64.StdEncoding.DecodeString(cleaned)
}

// NpmInstall runs `npm install` in the given directory.
func NpmInstall(ctx context.Context, dir string) error {
	cmd := exec.CommandContext(ctx, "npm", "install", "--no-audit", "--no-fund")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "CI=true")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("npm install failed: %w\n%s", err, string(output))
	}
	return nil
}

// WranglerDeploy runs `npx wrangler deploy` in the given directory.
// Returns the deployed worker URL parsed from the output.
func WranglerDeploy(ctx context.Context, dir string, cfg ProvisionConfig) (string, error) {
	args := []string{"wrangler", "deploy", "--config", filepath.Join(dir, "wrangler.toml")}

	cmd := exec.CommandContext(ctx, "npx", args...)
	cmd.Dir = dir
	env := append(os.Environ(), "CI=true", fmt.Sprintf("CLOUDFLARE_ACCOUNT_ID=%s", cfg.AccountID))
	if cfg.APIToken != "" {
		env = append(env, fmt.Sprintf("CLOUDFLARE_API_TOKEN=%s", cfg.APIToken))
	} else if cfg.APIKey != "" && cfg.Email != "" {
		env = append(env, fmt.Sprintf("CLOUDFLARE_API_KEY=%s", cfg.APIKey))
		env = append(env, fmt.Sprintf("CLOUDFLARE_EMAIL=%s", cfg.Email))
	}
	cmd.Env = env

	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("wrangler deploy failed: %w\n%s", err, string(output))
	}

	// Parse the worker URL from output
	// Wrangler outputs something like: "Published orangeshell-ai (0.28 sec)"
	// and "  https://orangeshell-ai.xxx.workers.dev"
	url := parseWorkerURL(string(output))
	if url == "" {
		// Fall back to a generic URL (the actual subdomain varies per account)
		url = fmt.Sprintf("https://%s.workers.dev", workerNamePrefix)
	}

	return url, nil
}

// WranglerSecretPut sets a secret on the deployed worker.
func WranglerSecretPut(ctx context.Context, dir string, cfg ProvisionConfig, name, value string) error {
	args := []string{"wrangler", "secret", "put", name, "--config", filepath.Join(dir, "wrangler.toml")}

	cmd := exec.CommandContext(ctx, "npx", args...)
	cmd.Dir = dir
	cmd.Stdin = strings.NewReader(value)
	env := append(os.Environ(), "CI=true", fmt.Sprintf("CLOUDFLARE_ACCOUNT_ID=%s", cfg.AccountID))
	if cfg.APIToken != "" {
		env = append(env, fmt.Sprintf("CLOUDFLARE_API_TOKEN=%s", cfg.APIToken))
	} else if cfg.APIKey != "" && cfg.Email != "" {
		env = append(env, fmt.Sprintf("CLOUDFLARE_API_KEY=%s", cfg.APIKey))
		env = append(env, fmt.Sprintf("CLOUDFLARE_EMAIL=%s", cfg.Email))
	}
	cmd.Env = env

	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("wrangler secret put failed: %w\n%s", err, string(output))
	}
	return nil
}

// WranglerDelete deletes the deployed worker.
func WranglerDelete(ctx context.Context, cfg ProvisionConfig) error {
	args := []string{"wrangler", "delete", "--name", workerNamePrefix, "--force"}

	cmd := exec.CommandContext(ctx, "npx", args...)
	env := append(os.Environ(), "CI=true", fmt.Sprintf("CLOUDFLARE_ACCOUNT_ID=%s", cfg.AccountID))
	if cfg.APIToken != "" {
		env = append(env, fmt.Sprintf("CLOUDFLARE_API_TOKEN=%s", cfg.APIToken))
	} else if cfg.APIKey != "" && cfg.Email != "" {
		env = append(env, fmt.Sprintf("CLOUDFLARE_API_KEY=%s", cfg.APIKey))
		env = append(env, fmt.Sprintf("CLOUDFLARE_EMAIL=%s", cfg.Email))
	}
	cmd.Env = env

	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("wrangler delete failed: %w\n%s", err, string(output))
	}
	return nil
}

// parseWorkerURL extracts the worker URL from wrangler deploy output.
func parseWorkerURL(output string) string {
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "https://") && strings.Contains(line, "workers.dev") {
			// Strip any trailing text after the URL
			if idx := strings.IndexByte(line, ' '); idx != -1 {
				line = line[:idx]
			}
			return line
		}
	}
	return ""
}

// Provision executes the full AI Worker provisioning flow:
// 1. Download template from GitHub
// 2. npm install
// 3. wrangler deploy
// 4. Generate secret
// 5. wrangler secret put AUTH_SECRET
func Provision(ctx context.Context, cfg ProvisionConfig, onProgress func(string)) (*ProvisionResult, error) {
	// Check npx is available
	if _, err := exec.LookPath("npx"); err != nil {
		return nil, fmt.Errorf("npx not found — Node.js is required for AI Worker deployment")
	}

	onProgress("Downloading AI Worker template from GitHub...")
	templateDir, err := DownloadTemplate(ctx)
	if err != nil {
		return nil, fmt.Errorf("template download failed: %w", err)
	}
	defer os.RemoveAll(templateDir)

	onProgress("Installing dependencies (npm install)...")
	installCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	if err := NpmInstall(installCtx, templateDir); err != nil {
		return nil, err
	}

	onProgress("Deploying AI Worker to your account...")
	deployCtx, cancel2 := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel2()
	workerURL, err := WranglerDeploy(deployCtx, templateDir, cfg)
	if err != nil {
		return nil, err
	}

	onProgress("Generating authentication secret...")
	secret, err := GenerateSecret()
	if err != nil {
		return nil, err
	}

	onProgress("Setting AUTH_SECRET on the Worker...")
	secretCtx, cancel3 := context.WithTimeout(ctx, 30*time.Second)
	defer cancel3()
	if err := WranglerSecretPut(secretCtx, templateDir, cfg, "AUTH_SECRET", secret); err != nil {
		return nil, err
	}

	onProgress("AI Worker deployed successfully!")
	return &ProvisionResult{
		WorkerURL: workerURL,
		Secret:    secret,
	}, nil
}

// Deprovision removes the AI Worker from the user's account.
func Deprovision(ctx context.Context, cfg ProvisionConfig) error {
	depCtx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	return WranglerDelete(depCtx, cfg)
}

// CheckWorkerExists checks if the orangeshell-ai worker already exists on the account.
func CheckWorkerExists(ctx context.Context, cfg ProvisionConfig) (bool, string, error) {
	url := fmt.Sprintf("https://api.cloudflare.com/client/v4/accounts/%s/workers/services/%s",
		cfg.AccountID, workerNamePrefix)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return false, "", err
	}

	if cfg.APIToken != "" {
		req.Header.Set("Authorization", "Bearer "+cfg.APIToken)
	} else if cfg.APIKey != "" && cfg.Email != "" {
		req.Header.Set("X-Auth-Key", cfg.APIKey)
		req.Header.Set("X-Auth-Email", cfg.Email)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false, "", err
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)

	if resp.StatusCode == http.StatusOK {
		// Worker exists — we can't reliably construct the URL without knowing
		// the account's workers.dev subdomain, so return empty URL.
		// The caller should use the stored URL from config.
		return true, "", nil
	}
	return false, "", nil
}
