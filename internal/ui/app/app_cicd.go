package app

import (
	"context"
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/oarafat/orangeshell/internal/api"
	svc "github.com/oarafat/orangeshell/internal/service"
	"github.com/oarafat/orangeshell/internal/ui/cicdpopup"
	wcfg "github.com/oarafat/orangeshell/internal/wrangler"
)

// setCICDFallbackTokenInfo passes the fallback token credentials to the CI/CD popup
// so it can include them in SetupCICDMsg for build token registration.
func (m *Model) setCICDFallbackTokenInfo() {
	accountID := m.registry.ActiveAccountID()
	token := m.cfg.FallbackTokenFor(accountID)
	tokenID := m.cfg.FallbackTokenIDFor(accountID)
	m.cicdPopup.SetFallbackTokenInfo(token, tokenID)
}

// handleCICDMsg handles all CI/CD wizard popup messages.
// Returns (model, cmd, handled).
func (m *Model) handleCICDMsg(msg tea.Msg) (Model, tea.Cmd, bool) {
	switch msg := msg.(type) {
	case cicdpopup.CloseMsg:
		m.showCICDPopup = false
		return *m, nil, true

	case cicdpopup.CheckInstallMsg:
		return *m, m.checkCICDInstallCmd(msg), true

	case cicdpopup.CheckInstallDoneMsg:
		var cmd tea.Cmd
		m.cicdPopup, cmd = m.cicdPopup.Update(msg)
		return *m, cmd, true

	case cicdpopup.SetupCICDMsg:
		return *m, m.setupCICDCmd(msg), true

	case cicdpopup.SetupCICDDoneMsg:
		accountID := m.registry.ActiveAccountID()

		// Persist re-provisioned fallback token if the setup flow replaced it.
		if msg.NewFallbackToken != "" {
			m.cfg.SetFallbackToken(accountID, msg.NewFallbackToken)
			if msg.NewFallbackTokenID != "" {
				m.cfg.SetFallbackTokenID(accountID, msg.NewFallbackTokenID)
			}
			_ = m.cfg.Save()

			// Re-wire WorkersService access auth with the new token
			if ws := m.getWorkersService(); ws != nil {
				ws.SetAccessAuth("", "", msg.NewFallbackToken)
			}
		} else if msg.FallbackTokenID != "" {
			// Persist the resolved fallback token ID if we discovered it during setup.
			if m.cfg.FallbackTokenIDFor(accountID) == "" {
				m.cfg.SetFallbackTokenID(accountID, msg.FallbackTokenID)
				_ = m.cfg.Save()
			}
		}

		if msg.Err != nil {
			// If PutRepoConnection failed (likely missing installation),
			// set the dashboard URL so the popup can show it.
			url := fmt.Sprintf("https://dash.cloudflare.com/%s/workers/builds", accountID)
			m.cicdPopup.SetDashboardURL(url)
		}
		var cmd tea.Cmd
		m.cicdPopup, cmd = m.cicdPopup.Update(msg)
		return *m, cmd, true

	case cicdpopup.DoneMsg:
		m.showCICDPopup = false
		m.setToast("CI/CD connected for " + msg.ScriptName)
		// Set CI/CD badge immediately for the just-configured worker
		// (instant feedback before next builds index rebuild).
		idx := m.registry.GetBuildsIndex()
		if idx == nil {
			idx = svc.NewBuildsIndex()
		}
		idx.SetConnected(msg.ScriptName)
		m.registry.SetBuildsIndex(idx)
		m.syncCICDBadges()
		// Refresh deployment data and worker list to reflect the new CI/CD state.
		return *m, tea.Batch(toastTick(), m.refreshAfterMutation()), true
	}
	return *m, nil, false
}

// checkCICDInstallCmd calls GetConfigAutofill to fetch auto-detected build
// configuration. A failure here is non-fatal — the wizard proceeds without
// pre-filled data. The real installation check happens at PutRepoConnection.
func (m *Model) checkCICDInstallCmd(msg cicdpopup.CheckInstallMsg) tea.Cmd {
	client := m.getBuildsClient()
	if client == nil {
		return func() tea.Msg {
			return cicdpopup.CheckInstallDoneMsg{
				Err: fmt.Errorf("no API credentials available"),
			}
		}
	}

	// Copy git info for the goroutine
	gitInfo := &wcfg.GitInfo{
		ProviderType: msg.Provider,
		Owner:        msg.ProviderAccountID,
		RepoName:     msg.RepoID,
	}
	branch := msg.Branch
	rootDir := msg.RootDir

	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		// Resolve string names to numeric GitHub/GitLab IDs.
		// The Cloudflare Builds API requires numeric IDs.
		if err := gitInfo.ResolveGitIDs(ctx); err != nil {
			// Non-fatal — proceed without numeric IDs, API calls may fail
			// but we'll show proper errors
		}

		// Use numeric IDs for API calls (fall back to string names if resolution failed)
		providerAccountID := gitInfo.OwnerID
		if providerAccountID == "" {
			providerAccountID = gitInfo.Owner
		}
		repoID := gitInfo.RepoID
		if repoID == "" {
			repoID = gitInfo.RepoName
		}

		autofill, err := client.GetConfigAutofill(ctx, msg.Provider, providerAccountID, repoID, branch, rootDir)
		return cicdpopup.CheckInstallDoneMsg{
			Autofill: autofill,
			Err:      err,
			OwnerID:  providerAccountID,
			RepoID:   repoID,
		}
	}
}

// setupCICDCmd performs the full CI/CD setup:
//  1. Re-provision the fallback token with broader permissions (if needed)
//  2. Create/update repo connection (PUT)
//  3. Resolve/create build token (delete stale one if re-provisioned)
//  4. Create trigger (or adopt existing one on 409 conflict)
//  5. Attempt a manual build to verify the pipeline
func (m *Model) setupCICDCmd(msg cicdpopup.SetupCICDMsg) tea.Cmd {
	client := m.getBuildsClient()
	if client == nil {
		return func() tea.Msg {
			return cicdpopup.SetupCICDDoneMsg{
				Err: fmt.Errorf("no API credentials available"),
			}
		}
	}

	// Capture credentials for re-provisioning (needed inside goroutine).
	authEmail := m.cfg.Email
	authKey := m.cfg.APIKey
	accountID := m.registry.ActiveAccountID()

	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()

		// Track whether we re-provisioned the fallback token so we can
		// report it back to the app for config persistence.
		var newFallbackToken, newFallbackTokenID string

		fallbackToken := msg.FallbackToken
		fallbackTokenID := msg.FallbackTokenID

		// Step 0: Re-provision the fallback token if we have API Key credentials.
		// The old token may lack Workers Scripts Write and other permissions
		// required by the build token to deploy. CreateScopedToken now includes
		// all required permissions, so we delete the old token and create a new one.
		if authEmail != "" && authKey != "" && fallbackToken != "" {
			// Resolve old token ID if not known
			oldTokenID := fallbackTokenID
			if oldTokenID == "" {
				resolved, err := api.VerifyTokenID(ctx, fallbackToken)
				if err != nil {
					// Non-fatal — proceed without old token ID
				} else {
					oldTokenID = resolved
				}
			}

			// Delete the old token
			if oldTokenID != "" {
				if err := api.DeleteCloudflareToken(ctx, authEmail, authKey, oldTokenID); err != nil {
					// Non-fatal — the old token might already be deleted or invalid.
					// Proceed with creating a new one anyway.
					_ = err
				}
			}

			// Create new token with full permissions
			result, err := api.CreateScopedToken(ctx, authEmail, authKey, accountID)
			if err != nil {
				// Non-fatal — continue with the old token (will likely fail at build time)
			} else {
				fallbackToken = result.Value
				fallbackTokenID = result.ID
				newFallbackToken = result.Value
				newFallbackTokenID = result.ID

				// Re-create the BuildsClient with the new token
				client = api.NewBuildsClient(accountID, "", "", fallbackToken)
			}
		} else if fallbackTokenID == "" && fallbackToken != "" {
			// Just resolve the ID if missing (no re-provisioning possible)
			resolvedID, verifyErr := api.VerifyTokenID(ctx, fallbackToken)
			if verifyErr == nil {
				fallbackTokenID = resolvedID
			}
		}

		// Step 1: Create/update repo connection (numeric IDs required)
		conn, err := client.PutRepoConnection(ctx, api.RepoConnectionRequest{
			ProviderAccountID:   msg.ProviderAccountID, // numeric ID
			ProviderAccountName: msg.ProviderOwnerName, // display name
			ProviderType:        msg.Provider,
			RepoID:              msg.RepoID,   // numeric ID
			RepoName:            msg.RepoName, // display name
		})
		if err != nil {
			return cicdpopup.SetupCICDDoneMsg{
				Err:                fmt.Errorf("creating repo connection: %w", err),
				NewFallbackToken:   newFallbackToken,
				NewFallbackTokenID: newFallbackTokenID,
			}
		}

		// Step 2: Resolve a build token UUID.
		// The Builds API requires a build_token_uuid to authorize deploys.
		// Each build token wraps a Cloudflare API token — builds use that token
		// to clone the repo and run wrangler deploy. A build token created by
		// the dashboard for a different worker may not work for ours, so we
		// prefer to create our own.

		var buildTokenUUID string
		const ourBuildTokenName = "orangeshell-build-token"

		// Check existing build tokens.
		existingTokens, listErr := client.ListBuildTokens(ctx)
		_ = listErr

		// If we re-provisioned the fallback token, any old orangeshell-build-token
		// wraps the now-deleted Cloudflare token and is invalid. Delete it so we
		// can create a fresh one wrapping the new token.
		if newFallbackToken != "" && existingTokens != nil {
			for _, t := range existingTokens {
				if t.Name == ourBuildTokenName {
					_ = client.DeleteBuildToken(ctx, t.UUID)
					break
				}
			}
		} else {
			// No re-provisioning — try to reuse our existing build token
			for _, t := range existingTokens {
				if t.Name == ourBuildTokenName {
					buildTokenUUID = t.UUID
					break
				}
			}
		}

		// If we don't have a build token yet, create one from our fallback token.
		if buildTokenUUID == "" && fallbackToken != "" && fallbackTokenID != "" {
			bt, btErr := client.CreateBuildToken(ctx,
				ourBuildTokenName,
				fallbackToken,
				fallbackTokenID,
			)
			if btErr != nil {
				// Non-fatal: fall back to any existing build token
				if len(existingTokens) > 0 {
					buildTokenUUID = existingTokens[0].UUID
				}
			} else {
				buildTokenUUID = bt.UUID
			}
		} else if buildTokenUUID == "" {
			// No fallback token available — use any existing build token as last resort
			if len(existingTokens) > 0 {
				buildTokenUUID = existingTokens[0].UUID
			}
		}

		// Step 3: Resolve the worker's script tag (internal ID).
		// The Builds API requires the script tag as external_script_id, NOT the
		// human-readable script name. Using the name causes "unable to verify Worker".
		scriptID := msg.ScriptName // fallback to name if resolution fails
		scriptTag, tagErr := client.GetScriptTag(ctx, msg.ScriptName)
		if tagErr == nil {
			scriptID = scriptTag
		}

		// Step 4: Create trigger (or adopt existing one on 409 conflict)
		trigger, err := client.CreateTrigger(ctx, api.TriggerCreateRequest{
			TriggerName:    msg.TriggerName,
			ScriptID:       scriptID,
			RepoConnUUID:   conn.UUID,
			BranchIncludes: msg.BranchIncludes,
			BranchExcludes: msg.BranchExcludes,
			PathIncludes:   msg.PathIncludes,
			PathExcludes:   msg.PathExcludes,
			BuildCommand:   msg.BuildCommand,
			DeployCommand:  msg.DeployCommand,
			RootDirectory:  msg.RootDirectory,
			BuildTokenUUID: buildTokenUUID,
		})

		if err != nil && !api.IsConflictError(err) {
			// Real error — not a duplicate trigger
			return cicdpopup.SetupCICDDoneMsg{
				Err:                fmt.Errorf("creating trigger: %w", err),
				FallbackTokenID:    fallbackTokenID,
				NewFallbackToken:   newFallbackToken,
				NewFallbackTokenID: newFallbackTokenID,
			}
		}

		// Step 4: Verify — fetch triggers to inspect linkage (always runs).
		// On 409, this is how we get the existing trigger object.
		triggers, verifyErr := client.GetWorkerTriggers(ctx, msg.ScriptName)
		if verifyErr != nil {
			if trigger == nil {
				// 409 path: we have no trigger object at all
				return cicdpopup.SetupCICDDoneMsg{
					Err:                fmt.Errorf("trigger exists but couldn't fetch it: %w", verifyErr),
					NewFallbackToken:   newFallbackToken,
					NewFallbackTokenID: newFallbackTokenID,
				}
			}
		} else {
			// On 409, adopt the existing trigger
			if trigger == nil && len(triggers) > 0 {
				trigger = &triggers[0]

				// Update the trigger if the build token or script ID differs.
				// The script ID must be the script tag (internal hash), not the name.
				needsUpdate := false
				if buildTokenUUID != "" && trigger.BuildTokenUUID != buildTokenUUID {
					needsUpdate = true
				}
				if trigger.ScriptID != scriptID {
					needsUpdate = true
				}
				if needsUpdate {
					updated, updateErr := client.UpdateTrigger(ctx, trigger.UUID, api.TriggerCreateRequest{
						TriggerName:    trigger.Name,
						ScriptID:       scriptID,
						RepoConnUUID:   conn.UUID,
						BranchIncludes: trigger.BranchIncludes,
						BranchExcludes: trigger.BranchExcludes,
						PathIncludes:   trigger.PathIncludes,
						PathExcludes:   trigger.PathExcludes,
						BuildCommand:   trigger.BuildCommand,
						DeployCommand:  trigger.DeployCommand,
						RootDirectory:  trigger.RootDirectory,
						BuildTokenUUID: buildTokenUUID,
					})
					if updateErr == nil {
						trigger = updated
					}
				}
			}
		}

		if trigger == nil {
			return cicdpopup.SetupCICDDoneMsg{
				Err:                fmt.Errorf("trigger creation failed and no existing trigger found"),
				FallbackTokenID:    fallbackTokenID,
				NewFallbackToken:   newFallbackToken,
				NewFallbackTokenID: newFallbackTokenID,
			}
		}

		// Step 5: Attempt a manual build to verify the pipeline works end-to-end.
		// Use the first branch from the trigger's branch_includes list.
		buildBranch := "main"
		if len(trigger.BranchIncludes) > 0 {
			buildBranch = trigger.BranchIncludes[0]
		}
		_, _ = client.CreateManualBuild(ctx, trigger.UUID, buildBranch)

		return cicdpopup.SetupCICDDoneMsg{
			Trigger:            trigger,
			FallbackTokenID:    fallbackTokenID,
			NewFallbackToken:   newFallbackToken,
			NewFallbackTokenID: newFallbackTokenID,
		}
	}
}
