package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/oarafat/orangeshell/internal/config"
	"github.com/pkg/browser"
)

const (
	// Wrangler's public OAuth client ID (no secret needed — PKCE only).
	oauthClientID = "54d11594-84e4-41aa-b438-e81b8fa78ee7"

	oauthAuthURL   = "https://dash.cloudflare.com/oauth2/auth"
	oauthTokenURL  = "https://dash.cloudflare.com/oauth2/token"
	oauthRevokeURL = "https://dash.cloudflare.com/oauth2/revoke"
	callbackPort   = "8976"
	callbackPath   = "/oauth/callback"
	redirectURI    = "http://localhost:" + callbackPort + callbackPath
)

// Default scopes matching wrangler's defaults.
var defaultScopes = []string{
	"account:read",
	"user:read",
	"workers:write",
	"workers_kv:write",
	"workers_routes:write",
	"workers_scripts:write",
	"workers_tail:read",
	"d1:write",
	"pages:write",
	"zone:read",
	"ssl_certs:write",
	"ai:write",
	"queues:write",
	"offline_access",
}

// OAuthAuth authenticates using the OAuth PKCE flow.
type OAuthAuth struct {
	cfg *config.Config
}

// NewOAuthAuth creates a new OAuth authenticator from existing config (tokens may already be stored).
func NewOAuthAuth(cfg *config.Config) *OAuthAuth {
	return &OAuthAuth{cfg: cfg}
}

func (a *OAuthAuth) Method() config.AuthMethod { return config.AuthMethodOAuth }
func (a *OAuthAuth) GetAPIKey() string         { return "" }
func (a *OAuthAuth) GetEmail() string          { return "" }
func (a *OAuthAuth) GetToken() string          { return a.cfg.OAuthAccessToken }

// Validate checks the stored token. If expired, attempts a refresh.
func (a *OAuthAuth) Validate(ctx context.Context) error {
	if a.cfg.OAuthAccessToken == "" {
		return fmt.Errorf("no OAuth token available — run login first")
	}

	// If token is expired, try to refresh
	if !a.cfg.OAuthExpiresAt.IsZero() && time.Now().After(a.cfg.OAuthExpiresAt) {
		if a.cfg.OAuthRefreshToken != "" {
			if err := a.refresh(ctx); err != nil {
				return fmt.Errorf("token expired and refresh failed: %w", err)
			}
		} else {
			return fmt.Errorf("token expired and no refresh token available")
		}
	}

	// Verify the token works
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		"https://api.cloudflare.com/client/v4/user", nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+a.cfg.OAuthAccessToken)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to validate OAuth token: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized && a.cfg.OAuthRefreshToken != "" {
		// Token invalid, attempt refresh
		if err := a.refresh(ctx); err != nil {
			return fmt.Errorf("token invalid and refresh failed: %w", err)
		}
		return nil
	}

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("OAuth token validation failed (HTTP %d)", resp.StatusCode)
	}

	return nil
}

// Login performs the full OAuth PKCE authorization code flow:
// 1. Generate PKCE verifier + challenge
// 2. Start local HTTP server on :8976
// 3. Open browser to Cloudflare's auth page
// 4. Wait for callback with auth code
// 5. Exchange code for tokens
// 6. Store tokens in config
func (a *OAuthAuth) Login(ctx context.Context) error {
	verifier, challenge, err := generatePKCE()
	if err != nil {
		return fmt.Errorf("failed to generate PKCE: %w", err)
	}

	state, err := generateRandomString(32)
	if err != nil {
		return fmt.Errorf("failed to generate state: %w", err)
	}

	// Channel to receive the authorization code from the callback
	codeCh := make(chan string, 1)
	errCh := make(chan error, 1)

	// Start local server
	listener, err := net.Listen("tcp", ":"+callbackPort)
	if err != nil {
		return fmt.Errorf("failed to start callback server on port %s: %w", callbackPort, err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc(callbackPath, func(w http.ResponseWriter, r *http.Request) {
		// Validate state
		if r.URL.Query().Get("state") != state {
			errCh <- fmt.Errorf("state mismatch in OAuth callback")
			http.Error(w, "State mismatch", http.StatusBadRequest)
			return
		}

		if errMsg := r.URL.Query().Get("error"); errMsg != "" {
			desc := r.URL.Query().Get("error_description")
			errCh <- fmt.Errorf("OAuth error: %s — %s", errMsg, desc)
			http.Error(w, "Authorization failed", http.StatusBadRequest)
			return
		}

		code := r.URL.Query().Get("code")
		if code == "" {
			errCh <- fmt.Errorf("no authorization code in callback")
			http.Error(w, "Missing code", http.StatusBadRequest)
			return
		}

		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<!DOCTYPE html><html><body>
			<h2>Authorization successful!</h2>
			<p>You can close this window and return to orangeshell.</p>
			<script>window.close()</script>
		</body></html>`)

		codeCh <- code
	})

	server := &http.Server{Handler: mux}
	go func() {
		if err := server.Serve(listener); err != http.ErrServerClosed {
			errCh <- err
		}
	}()
	defer server.Close()

	// Build authorization URL
	authURL := buildAuthURL(state, challenge)

	// Open browser
	if err := browser.OpenURL(authURL); err != nil {
		fmt.Printf("Failed to open browser automatically.\nPlease open this URL:\n\n%s\n\n", authURL)
	}

	// Wait for callback or context cancellation
	var code string
	select {
	case code = <-codeCh:
	case err := <-errCh:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}

	// Exchange code for tokens
	return a.exchangeCode(ctx, code, verifier)
}

// Logout revokes the refresh token and clears stored tokens.
func (a *OAuthAuth) Logout(ctx context.Context) error {
	if a.cfg.OAuthRefreshToken != "" {
		data := url.Values{
			"client_id":       {oauthClientID},
			"token_type_hint": {"refresh_token"},
			"token":           {a.cfg.OAuthRefreshToken},
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, oauthRevokeURL,
			strings.NewReader(data.Encode()))
		if err == nil {
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			resp, err := http.DefaultClient.Do(req)
			if err == nil {
				resp.Body.Close()
			}
		}
	}

	a.cfg.OAuthAccessToken = ""
	a.cfg.OAuthRefreshToken = ""
	a.cfg.OAuthExpiresAt = time.Time{}
	a.cfg.OAuthScopes = nil
	return a.cfg.Save()
}

func (a *OAuthAuth) exchangeCode(ctx context.Context, code, verifier string) error {
	data := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {redirectURI},
		"client_id":     {oauthClientID},
		"code_verifier": {verifier},
	}

	return a.doTokenRequest(ctx, data)
}

func (a *OAuthAuth) refresh(ctx context.Context) error {
	data := url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {a.cfg.OAuthRefreshToken},
		"client_id":     {oauthClientID},
	}

	return a.doTokenRequest(ctx, data)
}

func (a *OAuthAuth) doTokenRequest(ctx context.Context, data url.Values) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, oauthTokenURL,
		strings.NewReader(data.Encode()))
	if err != nil {
		return fmt.Errorf("failed to create token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("token request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read token response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("token exchange failed (HTTP %d): %s", resp.StatusCode, string(body))
	}

	var tokenResp struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int    `json:"expires_in"`
		Scope        string `json:"scope"`
		TokenType    string `json:"token_type"`
	}
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return fmt.Errorf("failed to parse token response: %w", err)
	}

	a.cfg.OAuthAccessToken = tokenResp.AccessToken
	if tokenResp.RefreshToken != "" {
		a.cfg.OAuthRefreshToken = tokenResp.RefreshToken
	}
	if tokenResp.ExpiresIn > 0 {
		a.cfg.OAuthExpiresAt = time.Now().Add(time.Duration(tokenResp.ExpiresIn) * time.Second)
	}
	if tokenResp.Scope != "" {
		a.cfg.OAuthScopes = strings.Split(tokenResp.Scope, " ")
	}
	a.cfg.AuthMethod = config.AuthMethodOAuth

	return a.cfg.Save()
}

func buildAuthURL(state, challenge string) string {
	params := url.Values{
		"response_type":         {"code"},
		"client_id":             {oauthClientID},
		"redirect_uri":          {redirectURI},
		"scope":                 {strings.Join(defaultScopes, " ")},
		"state":                 {state},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
	}
	return oauthAuthURL + "?" + params.Encode()
}

// generatePKCE generates a code verifier and its S256 challenge per RFC 7636.
func generatePKCE() (verifier, challenge string, err error) {
	// 96 bytes of randomness for the verifier
	b := make([]byte, 96)
	if _, err := rand.Read(b); err != nil {
		return "", "", err
	}

	// Base64url encode without padding (RFC 7636 appendix B)
	verifier = base64.RawURLEncoding.EncodeToString(b)

	// S256: challenge = BASE64URL(SHA256(verifier))
	h := sha256.Sum256([]byte(verifier))
	challenge = base64.RawURLEncoding.EncodeToString(h[:])

	return verifier, challenge, nil
}

// generateRandomString creates a URL-safe random string of the given byte length.
func generateRandomString(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
