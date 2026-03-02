package api

import (
	"bytes"
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
	return b.doRequestWithBody(ctx, method, path, nil)
}

func (b *BuildsClient) doRequestWithBody(ctx context.Context, method, path string, reqBody io.Reader) ([]byte, error) {
	url := fmt.Sprintf("https://api.cloudflare.com/client/v4/accounts/%s/%s", b.accountID, path)
	req, err := http.NewRequestWithContext(ctx, method, url, reqBody)
	if err != nil {
		return nil, err
	}

	if b.authToken != "" {
		req.Header.Set("Authorization", "Bearer "+b.authToken)
	} else {
		req.Header.Set("X-Auth-Email", b.authEmail)
		req.Header.Set("X-Auth-Key", b.authKey)
	}

	if reqBody != nil {
		req.Header.Set("Content-Type", "application/json")
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

	if resp.StatusCode == http.StatusConflict {
		return nil, &ConflictError{Body: truncateBody(body, 500)}
	}

	// Accept any 2xx status code (write endpoints may return 201)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("builds API returned %d: %s", resp.StatusCode, truncateBody(body, 500))
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

// ConflictError is returned when the Builds API responds with 409,
// indicating a resource already exists (e.g. duplicate trigger).
type ConflictError struct {
	Body string
}

func (e *ConflictError) Error() string {
	return fmt.Sprintf("builds API conflict (HTTP 409): %s", e.Body)
}

// IsConflictError returns true if err (or any wrapped error) is a *ConflictError.
func IsConflictError(err error) bool {
	var ce *ConflictError
	return errors.As(err, &ce)
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

// --- CI/CD types ---

// RepoConnection represents a Git repository connection for Workers Builds.
type RepoConnection struct {
	UUID                string `json:"repo_connection_uuid"`
	ProviderType        string `json:"provider_type"`         // "github" or "gitlab"
	ProviderAccountID   string `json:"provider_account_id"`   // GitHub/GitLab org or user ID
	ProviderAccountName string `json:"provider_account_name"` // display name
	RepoID              string `json:"repo_id"`               // repository identifier
	RepoName            string `json:"repo_name"`             // repository display name
}

// Trigger represents a CI/CD trigger linking a worker to a repo connection.
type Trigger struct {
	UUID           string          `json:"trigger_uuid"`
	Name           string          `json:"trigger_name"`
	ScriptID       string          `json:"external_script_id"`
	BranchIncludes []string        `json:"branch_includes"`
	BranchExcludes []string        `json:"branch_excludes"`
	PathIncludes   []string        `json:"path_includes"`
	PathExcludes   []string        `json:"path_excludes"`
	BuildCommand   string          `json:"build_command"`
	DeployCommand  string          `json:"deploy_command"`
	RootDirectory  string          `json:"root_directory"`
	BuildCaching   bool            `json:"build_caching_enabled"`
	BuildTokenUUID string          `json:"build_token_uuid,omitempty"`
	BuildTokenName string          `json:"build_token_name,omitempty"`
	RepoConnection *RepoConnection `json:"repo_connection,omitempty"`
	CreatedOn      string          `json:"created_on,omitempty"`
	ModifiedOn     string          `json:"modified_on,omitempty"`
}

// ConfigAutofill holds auto-detected configuration from a repository.
type ConfigAutofill struct {
	ConfigFile        string            `json:"config_file"`
	DefaultWorkerName string            `json:"default_worker_name"`
	EnvWorkerNames    map[string]string `json:"env_worker_names"`
	PackageManager    string            `json:"package_manager"`
	Scripts           map[string]string `json:"scripts"`
}

// BuildToken represents a registered build authentication token.
type BuildToken struct {
	UUID              string `json:"build_token_uuid"`
	Name              string `json:"build_token_name"`
	CloudflareTokenID string `json:"cloudflare_token_id"`
	OwnerType         string `json:"owner_type"` // "user" or "account"
}

// --- CI/CD response types ---

type repoConnectionResponse struct {
	Success bool           `json:"success"`
	Result  RepoConnection `json:"result"`
	Errors  []cfError      `json:"errors"`
}

type triggerResponse struct {
	Success bool      `json:"success"`
	Result  Trigger   `json:"result"`
	Errors  []cfError `json:"errors"`
}

type triggersListResponse struct {
	Success bool      `json:"success"`
	Result  []Trigger `json:"result"`
	Errors  []cfError `json:"errors"`
}

type configAutofillResponse struct {
	Success bool           `json:"success"`
	Result  ConfigAutofill `json:"result"`
	Errors  []cfError      `json:"errors"`
}

type buildTokenResponse struct {
	Success bool       `json:"success"`
	Result  BuildToken `json:"result"`
	Errors  []cfError  `json:"errors"`
}

// --- CI/CD request types ---

// RepoConnectionRequest is the request body for PUT /builds/repos/connections.
type RepoConnectionRequest struct {
	ProviderAccountID   string `json:"provider_account_id"`
	ProviderAccountName string `json:"provider_account_name"`
	ProviderType        string `json:"provider_type"`
	RepoID              string `json:"repo_id"`
	RepoName            string `json:"repo_name"`
}

// TriggerCreateRequest is the request body for POST /builds/triggers.
// All fields are always serialized (no omitempty) because the Builds API
// rejects requests with missing fields (12002: Invalid request body).
// build_token_uuid is the only optional field (omitempty).
type TriggerCreateRequest struct {
	TriggerName    string   `json:"trigger_name"`
	ScriptID       string   `json:"external_script_id"`
	RepoConnUUID   string   `json:"repo_connection_uuid"`
	BranchIncludes []string `json:"branch_includes"`
	BranchExcludes []string `json:"branch_excludes"`
	PathIncludes   []string `json:"path_includes"`
	PathExcludes   []string `json:"path_excludes"`
	BuildCommand   string   `json:"build_command"`
	DeployCommand  string   `json:"deploy_command"`
	RootDirectory  string   `json:"root_directory"`
	BuildTokenUUID string   `json:"build_token_uuid,omitempty"`
}

type buildTokenRequest struct {
	Name              string `json:"build_token_name"`
	Secret            string `json:"build_token_secret"`
	CloudflareTokenID string `json:"cloudflare_token_id"`
}

// --- CI/CD public methods ---

// GetScriptTag resolves a worker script name to its script tag (internal ID).
// The Builds API requires the script tag as external_script_id, not the human-readable name.
// endpoint: GET /accounts/{account_id}/workers/services/{script_name}
func (b *BuildsClient) GetScriptTag(ctx context.Context, scriptName string) (string, error) {
	path := fmt.Sprintf("workers/services/%s", scriptName)
	body, err := b.doRequest(ctx, http.MethodGet, path)
	if err != nil {
		return "", fmt.Errorf("fetching worker service: %w", err)
	}

	var resp struct {
		Success bool `json:"success"`
		Result  struct {
			DefaultEnvironment struct {
				ScriptTag string `json:"script_tag"`
			} `json:"default_environment"`
		} `json:"result"`
		Errors []cfError `json:"errors"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return "", fmt.Errorf("parsing worker service response: %w", err)
	}
	if !resp.Success && len(resp.Errors) > 0 {
		return "", fmt.Errorf("workers API error: %s", resp.Errors[0].Message)
	}
	if resp.Result.DefaultEnvironment.ScriptTag == "" {
		return "", fmt.Errorf("script tag not found for %s", scriptName)
	}
	return resp.Result.DefaultEnvironment.ScriptTag, nil
}

// GetWorkerTriggers lists all CI/CD triggers for a worker script.
// endpoint: GET /accounts/{account_id}/builds/workers/{script_name}/triggers
func (b *BuildsClient) GetWorkerTriggers(ctx context.Context, scriptName string) ([]Trigger, error) {
	path := fmt.Sprintf("builds/workers/%s/triggers", scriptName)

	body, err := b.doRequest(ctx, http.MethodGet, path)
	if err != nil {
		return nil, err
	}

	var resp triggersListResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("parsing triggers list: %w", err)
	}
	if !resp.Success && len(resp.Errors) > 0 {
		return nil, fmt.Errorf("builds API error: %s", resp.Errors[0].Message)
	}

	return resp.Result, nil
}

// GetConfigAutofill fetches auto-detected build configuration for a repository.
// This also serves as a check for whether the GitHub/GitLab installation exists —
// if it fails with a non-auth error, the installation may not be set up.
// endpoint: GET /accounts/{account_id}/builds/repos/{provider}/{account_id}/{repo_id}/config_autofill
func (b *BuildsClient) GetConfigAutofill(ctx context.Context, provider, providerAccountID, repoID, branch, rootDir string) (*ConfigAutofill, error) {
	path := fmt.Sprintf("builds/repos/%s/%s/%s/config_autofill", provider, providerAccountID, repoID)

	// Add optional query parameters
	var params []string
	if branch != "" {
		params = append(params, "branch="+branch)
	}
	if rootDir != "" {
		params = append(params, "root_directory="+rootDir)
	}
	if len(params) > 0 {
		path += "?" + strings.Join(params, "&")
	}

	body, err := b.doRequest(ctx, http.MethodGet, path)
	if err != nil {
		return nil, err
	}

	var resp configAutofillResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("parsing config autofill: %w", err)
	}
	if !resp.Success && len(resp.Errors) > 0 {
		return nil, fmt.Errorf("builds API error: %s", resp.Errors[0].Message)
	}

	return &resp.Result, nil
}

// ListRepoConnections returns all repository connections for the account.
// endpoint: GET /accounts/{account_id}/builds/repos/connections
func (b *BuildsClient) ListRepoConnections(ctx context.Context) ([]RepoConnection, error) {
	body, err := b.doRequest(ctx, http.MethodGet, "builds/repos/connections")
	if err != nil {
		return nil, err
	}

	var resp struct {
		Success bool             `json:"success"`
		Result  []RepoConnection `json:"result"`
		Errors  []struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("parsing repo connections: %w", err)
	}
	if !resp.Success && len(resp.Errors) > 0 {
		return nil, fmt.Errorf("builds API error: %s", resp.Errors[0].Message)
	}

	return resp.Result, nil
}

// PutRepoConnection creates or updates a repository connection (upsert).
// endpoint: PUT /accounts/{account_id}/builds/repos/connections
func (b *BuildsClient) PutRepoConnection(ctx context.Context, req RepoConnectionRequest) (*RepoConnection, error) {
	payload, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshalling repo connection: %w", err)
	}

	body, err := b.doRequestWithBody(ctx, http.MethodPut, "builds/repos/connections", bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}

	var resp repoConnectionResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("parsing repo connection response: %w", err)
	}
	if !resp.Success && len(resp.Errors) > 0 {
		return nil, fmt.Errorf("builds API error: %s", resp.Errors[0].Message)
	}

	return &resp.Result, nil
}

// CreateTrigger creates a new CI/CD trigger linking a worker to a repo connection.
// endpoint: POST /accounts/{account_id}/builds/triggers
func (b *BuildsClient) CreateTrigger(ctx context.Context, req TriggerCreateRequest) (*Trigger, error) {
	// Normalize nil slices to empty slices so JSON marshals as [] not null.
	// The Builds API rejects null arrays in the request body.
	if req.BranchIncludes == nil {
		req.BranchIncludes = []string{}
	}
	if req.BranchExcludes == nil {
		req.BranchExcludes = []string{}
	}
	if req.PathIncludes == nil {
		req.PathIncludes = []string{}
	}
	if req.PathExcludes == nil {
		req.PathExcludes = []string{}
	}

	payload, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshalling trigger: %w", err)
	}

	body, err := b.doRequestWithBody(ctx, http.MethodPost, "builds/triggers", bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}

	var resp triggerResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("parsing trigger response: %w", err)
	}
	if !resp.Success && len(resp.Errors) > 0 {
		return nil, fmt.Errorf("builds API error: %s", resp.Errors[0].Message)
	}

	return &resp.Result, nil
}

// UpdateTrigger updates an existing CI/CD trigger via PATCH.
// Only the fields present in the request body are updated.
// endpoint: PATCH /accounts/{account_id}/builds/triggers/{trigger_uuid}
func (b *BuildsClient) UpdateTrigger(ctx context.Context, triggerUUID string, req TriggerCreateRequest) (*Trigger, error) {
	payload, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshalling trigger update: %w", err)
	}

	path := fmt.Sprintf("builds/triggers/%s", triggerUUID)

	body, err := b.doRequestWithBody(ctx, http.MethodPatch, path, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}

	var resp triggerResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("parsing trigger update response: %w", err)
	}
	if !resp.Success && len(resp.Errors) > 0 {
		return nil, fmt.Errorf("builds API error: %s", resp.Errors[0].Message)
	}

	return &resp.Result, nil
}

// ListBuildTokens returns all registered build tokens for the account.
// endpoint: GET /accounts/{account_id}/builds/tokens
func (b *BuildsClient) ListBuildTokens(ctx context.Context) ([]BuildToken, error) {
	body, err := b.doRequest(ctx, http.MethodGet, "builds/tokens")
	if err != nil {
		return nil, err
	}

	var resp struct {
		Success bool         `json:"success"`
		Result  []BuildToken `json:"result"`
		Errors  []cfError    `json:"errors"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("parsing build tokens: %w", err)
	}
	if !resp.Success && len(resp.Errors) > 0 {
		return nil, fmt.Errorf("builds API error: %s", resp.Errors[0].Message)
	}

	return resp.Result, nil
}

// ManualBuildResult is the response from triggering a manual build.
type ManualBuildResult struct {
	BuildUUID string `json:"build_uuid"`
	CreatedOn string `json:"created_on"`
}

// manualBuildRequest is the request body for CreateManualBuild.
// The API requires at least a branch; commit_hash is optional.
type manualBuildRequest struct {
	Branch     string `json:"branch"`
	CommitHash string `json:"commit_hash,omitempty"`
}

// CreateManualBuild triggers a manual build for a specific trigger.
// This is useful for verifying the pipeline works without a git push.
// endpoint: POST /accounts/{account_id}/builds/triggers/{trigger_uuid}/builds
func (b *BuildsClient) CreateManualBuild(ctx context.Context, triggerUUID, branch string) (*ManualBuildResult, error) {
	path := fmt.Sprintf("builds/triggers/%s/builds", triggerUUID)

	reqBody := manualBuildRequest{Branch: branch}
	payload, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshalling manual build request: %w", err)
	}

	body, err := b.doRequestWithBody(ctx, http.MethodPost, path, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}

	var resp struct {
		Success bool              `json:"success"`
		Result  ManualBuildResult `json:"result"`
		Errors  []cfError         `json:"errors"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("parsing manual build response: %w", err)
	}
	if !resp.Success && len(resp.Errors) > 0 {
		return nil, fmt.Errorf("builds API error: %s", resp.Errors[0].Message)
	}

	return &resp.Result, nil
}

// DeleteBuildToken deletes a registered build token by UUID.
// endpoint: DELETE /accounts/{account_id}/builds/tokens/{build_token_uuid}
func (b *BuildsClient) DeleteBuildToken(ctx context.Context, buildTokenUUID string) error {
	path := fmt.Sprintf("builds/tokens/%s", buildTokenUUID)
	_, err := b.doRequest(ctx, http.MethodDelete, path)
	return err
}

// CreateBuildToken registers a build authentication token for Workers Builds.
// endpoint: POST /accounts/{account_id}/builds/tokens
func (b *BuildsClient) CreateBuildToken(ctx context.Context, name, secret, cfTokenID string) (*BuildToken, error) {
	req := buildTokenRequest{
		Name:              name,
		Secret:            secret,
		CloudflareTokenID: cfTokenID,
	}
	payload, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshalling build token: %w", err)
	}

	body, err := b.doRequestWithBody(ctx, http.MethodPost, "builds/tokens", bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}

	var resp buildTokenResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("parsing build token response: %w", err)
	}
	if !resp.Success && len(resp.Errors) > 0 {
		return nil, fmt.Errorf("builds API error: %s", resp.Errors[0].Message)
	}

	return &resp.Result, nil
}
