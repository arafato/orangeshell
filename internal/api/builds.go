package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// BuildsClient makes raw HTTP requests to the Workers Builds API,
// which is not covered by the cloudflare-go SDK.
type BuildsClient struct {
	accountID string
	authEmail string // for X-Auth-Email + X-Auth-Key auth
	authKey   string
	authToken string // for Bearer token auth
	http      *http.Client
}

// NewBuildsClient creates a BuildsClient from the parent API Client's credentials.
func NewBuildsClient(accountID, authEmail, authKey, authToken string) *BuildsClient {
	return &BuildsClient{
		accountID: accountID,
		authEmail: authEmail,
		authKey:   authKey,
		authToken: authToken,
		http:      &http.Client{Timeout: 30 * time.Second},
	}
}

func (b *BuildsClient) doRequest(ctx context.Context, method, path string) ([]byte, error) {
	url := fmt.Sprintf("https://api.cloudflare.com/client/v4/accounts/%s/%s", b.accountID, path)
	req, err := http.NewRequestWithContext(ctx, method, url, nil)
	if err != nil {
		return nil, err
	}

	if b.authToken != "" {
		req.Header.Set("Authorization", "Bearer "+b.authToken)
	} else {
		req.Header.Set("X-Auth-Email", b.authEmail)
		req.Header.Set("X-Auth-Key", b.authKey)
	}

	resp, err := b.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("builds API request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading builds API response: %w", err)
	}

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return nil, &AuthError{StatusCode: resp.StatusCode, Body: truncateBody(body, 200)}
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("builds API returned %d: %s", resp.StatusCode, truncateBody(body, 200))
	}

	return body, nil
}

// AuthError is returned when the Builds API responds with 401 or 403,
// indicating that the credentials lack the required Workers CI Read scope.
type AuthError struct {
	StatusCode int
	Body       string
}

func (e *AuthError) Error() string {
	return fmt.Sprintf("builds API authentication failed (HTTP %d): %s", e.StatusCode, e.Body)
}

// IsAuthError returns true if err (or any wrapped error) is an *AuthError.
func IsAuthError(err error) bool {
	var ae *AuthError
	return errors.As(err, &ae)
}

func truncateBody(b []byte, maxLen int) string {
	s := string(b)
	if len(s) > maxLen {
		return s[:maxLen] + "..."
	}
	return s
}

// --- API response types ---

type buildsListResponse struct {
	Success bool          `json:"success"`
	Result  []BuildResult `json:"result"`
	Errors  []cfError     `json:"errors"`
}

type buildsByVersionResponse struct {
	Success bool `json:"success"`
	Result  struct {
		Builds map[string]BuildResult `json:"builds"` // keyed by version_id
	} `json:"result"`
	Errors []cfError `json:"errors"`
}

type buildLogResponse struct {
	Success bool `json:"success"`
	Result  struct {
		Cursor    string    `json:"cursor"`
		Lines     []LogLine `json:"lines"`
		Truncated bool      `json:"truncated"`
	} `json:"result"`
	Errors []cfError `json:"errors"`
}

type cfError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// BuildResult represents a single build from the Workers Builds API.
type BuildResult struct {
	BuildUUID            string           `json:"build_uuid"`
	BuildOutcome         string           `json:"build_outcome"` // "success", "failure", "canceled"
	Status               string           `json:"status"`        // "running", "complete", "queued", "canceled"
	CreatedOn            time.Time        `json:"created_on"`
	ModifiedOn           time.Time        `json:"modified_on"`
	RunningOn            *time.Time       `json:"running_on"`
	StoppedOn            *time.Time       `json:"stopped_on"`
	BuildTriggerMetadata BuildTriggerMeta `json:"build_trigger_metadata"`
	VersionID            string           `json:"version_id,omitempty"` // present in builds-by-version response
}

// BuildTriggerMeta contains git metadata from a CI build.
type BuildTriggerMeta struct {
	BuildTriggerSource  string `json:"build_trigger_source"` // "push", "push_event"
	Branch              string `json:"branch"`
	CommitHash          string `json:"commit_hash"`
	CommitMessage       string `json:"commit_message"`
	Author              string `json:"author"`
	BuildCommand        string `json:"build_command"`
	DeployCommand       string `json:"deploy_command"`
	RootDirectory       string `json:"root_directory"`
	RepoName            string `json:"repo_name"`
	ProviderAccountName string `json:"provider_account_name"`
	ProviderType        string `json:"provider_type"` // "github", "gitlab"
}

// LogLine is a single line in the build log.
// The API returns lines as 2-element arrays: [timestamp_millis, message].
type LogLine struct {
	Timestamp time.Time
	Message   string
}

// UnmarshalJSON handles the [timestamp_millis, "message"] array format.
func (l *LogLine) UnmarshalJSON(data []byte) error {
	var raw []json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	if len(raw) != 2 {
		return fmt.Errorf("expected 2-element array, got %d", len(raw))
	}

	var millis int64
	if err := json.Unmarshal(raw[0], &millis); err != nil {
		return fmt.Errorf("parsing timestamp: %w", err)
	}
	l.Timestamp = time.UnixMilli(millis)

	if err := json.Unmarshal(raw[1], &l.Message); err != nil {
		return fmt.Errorf("parsing message: %w", err)
	}
	return nil
}

// --- Public methods ---

// ListBuilds fetches all builds for a worker script. Returns newest first.
// endpoint: GET /accounts/{account_id}/builds/workers/{script_name}/builds
func (b *BuildsClient) ListBuilds(ctx context.Context, scriptName string, perPage int) ([]BuildResult, error) {
	if perPage <= 0 {
		perPage = 50
	}
	path := fmt.Sprintf("builds/workers/%s/builds?per_page=%d", scriptName, perPage)

	body, err := b.doRequest(ctx, http.MethodGet, path)
	if err != nil {
		return nil, err
	}

	var resp buildsListResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("parsing builds list: %w", err)
	}
	if !resp.Success && len(resp.Errors) > 0 {
		return nil, fmt.Errorf("builds API error: %s", resp.Errors[0].Message)
	}

	return resp.Result, nil
}

// GetBuildsByVersionIDs fetches build data for specific version IDs.
// endpoint: GET /accounts/{account_id}/builds/builds?version_ids=id1,id2,...
func (b *BuildsClient) GetBuildsByVersionIDs(ctx context.Context, versionIDs []string) (map[string]BuildResult, error) {
	if len(versionIDs) == 0 {
		return nil, nil
	}
	path := fmt.Sprintf("builds/builds?version_ids=%s", strings.Join(versionIDs, ","))

	body, err := b.doRequest(ctx, http.MethodGet, path)
	if err != nil {
		return nil, err
	}

	var resp buildsByVersionResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("parsing builds by version: %w", err)
	}
	if !resp.Success && len(resp.Errors) > 0 {
		return nil, fmt.Errorf("builds API error: %s", resp.Errors[0].Message)
	}

	return resp.Result.Builds, nil
}

// GetBuildLog fetches the log for a specific build.
// endpoint: GET /accounts/{account_id}/builds/builds/{build_uuid}/logs
// Returns all lines by following cursor pagination.
func (b *BuildsClient) GetBuildLog(ctx context.Context, buildUUID string) ([]LogLine, error) {
	var allLines []LogLine
	cursor := ""

	for {
		path := fmt.Sprintf("builds/builds/%s/logs", buildUUID)
		if cursor != "" {
			path += "?cursor=" + cursor
		}

		body, err := b.doRequest(ctx, http.MethodGet, path)
		if err != nil {
			return allLines, err
		}

		var resp buildLogResponse
		if err := json.Unmarshal(body, &resp); err != nil {
			return allLines, fmt.Errorf("parsing build log: %w", err)
		}
		if !resp.Success && len(resp.Errors) > 0 {
			return allLines, fmt.Errorf("builds API error: %s", resp.Errors[0].Message)
		}

		allLines = append(allLines, resp.Result.Lines...)

		if !resp.Result.Truncated || resp.Result.Cursor == "" {
			break
		}
		cursor = resp.Result.Cursor
	}

	return allLines, nil
}

// VerifyAuth checks whether the client's credentials are accepted by the
// Builds API. It calls a lightweight endpoint and returns nil if auth succeeds,
// or an *AuthError if the token lacks the required Workers CI Read scope.
func (b *BuildsClient) VerifyAuth(ctx context.Context) error {
	// Use the builds-by-version endpoint with an empty version list.
	// This returns an empty result on success, or 401/403 on auth failure.
	_, err := b.doRequest(ctx, http.MethodGet, "builds/builds?version_ids=_")
	if err != nil {
		if IsAuthError(err) {
			return err
		}
		// Any other error (e.g. 400, 404) means auth itself succeeded.
		return nil
	}
	return nil
}
