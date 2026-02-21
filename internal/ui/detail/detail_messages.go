package detail

import (
	"github.com/oarafat/orangeshell/internal/service"
	"github.com/oarafat/orangeshell/internal/wrangler"
)

// SelectServiceMsg is emitted when the user selects a service from the dropdown.
// The app layer handles this to trigger resource loading.
type SelectServiceMsg struct {
	ServiceName string
}

// Messages sent by the detail panel to the parent (app model).
type (
	// LoadResourcesMsg requests the app to load resources for a service.
	LoadResourcesMsg struct {
		ServiceName string
	}
	// ResourcesLoadedMsg carries the loaded resources back.
	ResourcesLoadedMsg struct {
		ServiceName   string
		AccountID     string // account this response belongs to (for staleness checks)
		Resources     []service.Resource
		Err           error
		NotIntegrated bool // true only when the service has no backend integration
	}
	// LoadDetailMsg requests the app to load detail for a single resource.
	LoadDetailMsg struct {
		ServiceName string
		ResourceID  string
	}
	// DetailLoadedMsg carries the loaded resource detail back.
	DetailLoadedMsg struct {
		ServiceName string
		ResourceID  string
		Detail      *service.ResourceDetail
		Err         error
	}
	// BackgroundRefreshMsg carries updated resources from a background refresh.
	BackgroundRefreshMsg struct {
		ServiceName string
		AccountID   string // account this response belongs to (for staleness checks)
		Resources   []service.Resource
		Err         error
	}

	// Tail-related messages

	// TailStartMsg requests the app to start tailing a Worker's logs.
	TailStartMsg struct {
		ScriptName string
		AccountID  string
	}
	// TailStartedMsg indicates a tail session was created successfully.
	TailStartedMsg struct {
		Session *service.TailSession
	}
	// TailLogMsg carries new log lines from the websocket.
	TailLogMsg struct {
		Lines []service.TailLine
	}
	// TailErrorMsg indicates tail creation/connection failed.
	TailErrorMsg struct {
		Err error
	}
	// TailStoppedMsg indicates the tail was stopped (cleanup complete).
	TailStoppedMsg struct{}

	// D1 SQL console messages

	// D1QueryMsg requests the app to execute a SQL query against a D1 database.
	D1QueryMsg struct {
		DatabaseID string
		SQL        string
	}
	// D1QueryResultMsg carries the result of a SQL query.
	D1QueryResultMsg struct {
		Result *service.D1QueryResult
		Err    error
	}
	// D1SchemaLoadMsg requests the app to load the schema for a D1 database.
	D1SchemaLoadMsg struct {
		DatabaseID string
	}
	// D1SchemaLoadedMsg carries the structured schema data.
	D1SchemaLoadedMsg struct {
		DatabaseID string
		Tables     []service.SchemaTable
		Err        error
	}

	// EnterInteractiveMsg is emitted when the user enters interactive mode on a
	// ReadWrite service's detail view. The app layer handles this to initialize
	// service-specific interactive features (e.g. D1 SQL console, KV data explorer).
	EnterInteractiveMsg struct {
		ServiceName   string
		ResourceID    string
		Mode          DetailMode
		IsLocal       bool                    // true when entering interactive mode on a local emulator resource
		LocalResource *wrangler.LocalResource // the local resource (nil for remote)
	}

	// KV Data Explorer messages

	// KVKeysLoadMsg requests the app to load keys (with values) from a KV namespace.
	KVKeysLoadMsg struct {
		NamespaceID string
		Prefix      string
	}
	// KVKeysLoadedMsg carries the loaded KV key-value entries back.
	KVKeysLoadedMsg struct {
		NamespaceID string
		Keys        []service.KVKeyEntry
		Err         error
	}

	// Local emulator messages

	// LocalResourcesUpdatedMsg carries local D1/KV resources discovered from active dev sessions.
	// The detail model merges these into the resource list with yellow styling.
	LocalResourcesUpdatedMsg struct {
		Resources []wrangler.LocalResource
	}

	// LocalD1QueryMsg requests the app to execute a SQL query against a local D1 database.
	LocalD1QueryMsg struct {
		LocalResource wrangler.LocalResource
		SQL           string
	}
	// LocalD1QueryResultMsg carries the result of a local D1 SQL query.
	LocalD1QueryResultMsg struct {
		Result *service.D1QueryResult
		Err    error
	}

	// LocalKVKeysLoadMsg requests the app to load keys from a local KV namespace.
	LocalKVKeysLoadMsg struct {
		LocalResource wrangler.LocalResource
		Prefix        string
	}
	// LocalKVKeysLoadedMsg carries the loaded local KV key-value entries back.
	LocalKVKeysLoadedMsg struct {
		BindingName string
		Keys        []service.KVKeyEntry
		Err         error
	}

	// CopyToClipboardMsg requests the app to copy text to the system clipboard.
	CopyToClipboardMsg struct {
		Text string
	}

	// DeleteResourceRequestMsg requests the app to open the delete confirmation popup.
	DeleteResourceRequestMsg struct {
		ServiceName  string
		ResourceID   string
		ResourceName string
	}

	// Version history messages (Workers only)

	// LoadVersionHistoryMsg requests the app to fetch version + deployment history.
	LoadVersionHistoryMsg struct {
		ScriptName string
	}
	// VersionHistoryLoadedMsg carries the merged version history data.
	VersionHistoryLoadedMsg struct {
		ScriptName string
		Entries    []wrangler.VersionHistoryEntry
		Err        error
	}

	// BuildsEnrichedMsg indicates that build metadata has been merged into entries.
	BuildsEnrichedMsg struct {
		ScriptName string
		Entries    []wrangler.VersionHistoryEntry
	}

	// Build log messages (Workers Builds — Phase 2)

	// FetchBuildLogMsg requests the app to fetch a build log.
	FetchBuildLogMsg struct {
		ScriptName string
		BuildUUID  string
		Entry      wrangler.VersionHistoryEntry // for display header
	}
	// BuildLogLoadedMsg carries the fetched build log.
	BuildLogLoadedMsg struct {
		BuildUUID string
		Lines     []string // log lines
		Err       error
		Entry     wrangler.VersionHistoryEntry
	}

	// BuildsAuthFailedMsg indicates that the Builds API returned 401/403,
	// meaning the current credentials lack the Workers CI Read scope.
	BuildsAuthFailedMsg struct {
		ScriptName string
	}
)
