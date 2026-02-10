package api

import (
	"context"
	"fmt"

	cloudflare "github.com/cloudflare/cloudflare-go/v6"
	"github.com/cloudflare/cloudflare-go/v6/accounts"
	"github.com/cloudflare/cloudflare-go/v6/option"

	"github.com/oarafat/orangeshell/internal/auth"
	"github.com/oarafat/orangeshell/internal/config"
)

// Account is a simplified account representation for the TUI.
type Account struct {
	ID   string
	Name string
}

// Client wraps the Cloudflare v6 SDK client.
type Client struct {
	CF        *cloudflare.Client
	AccountID string
}

// NewClient creates a Cloudflare API client from the given authenticator and config.
func NewClient(a auth.Authenticator, cfg *config.Config) (*Client, error) {
	var opts []option.RequestOption

	switch a.Method() {
	case config.AuthMethodAPIKey:
		opts = append(opts, option.WithAPIKey(a.GetAPIKey()), option.WithAPIEmail(a.GetEmail()))
	case config.AuthMethodAPIToken, config.AuthMethodOAuth:
		opts = append(opts, option.WithAPIToken(a.GetToken()))
	default:
		return nil, fmt.Errorf("unsupported auth method: %s", a.Method())
	}

	cf := cloudflare.NewClient(opts...)

	return &Client{
		CF:        cf,
		AccountID: cfg.AccountID,
	}, nil
}

// ListAccounts returns all accounts accessible to the authenticated user.
func (c *Client) ListAccounts(ctx context.Context) ([]Account, error) {
	pager := c.CF.Accounts.ListAutoPaging(ctx, accounts.AccountListParams{})

	var result []Account
	for pager.Next() {
		acc := pager.Current()
		result = append(result, Account{
			ID:   acc.ID,
			Name: acc.Name,
		})
	}
	if err := pager.Err(); err != nil {
		return nil, fmt.Errorf("failed to list accounts: %w", err)
	}

	return result, nil
}

// VerifyConnection checks that the client can successfully call the API.
func (c *Client) VerifyConnection(ctx context.Context) error {
	_, err := c.ListAccounts(ctx)
	return err
}
