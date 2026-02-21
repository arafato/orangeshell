package wrangler

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// LocalResource represents a D1 or KV resource available via a local wrangler dev session.
type LocalResource struct {
	BindingName  string // CLI identifier: JS binding name for KV (e.g. "MY_KV"), database_name for D1 (e.g. "my-db")
	ResourceType string // "KV" or "D1"
	ResourceName string // human-readable name for display (same as BindingName)
	ResourceID   string // upstream resource ID (namespace_id or database_id) — may be empty for local-only
	ConfigPath   string // absolute path to wrangler config file
	ProjectDir   string // absolute path to project directory (for CWD)
	EnvName      string // wrangler environment name (empty for default)
}

// LocalD1QueryResult holds the result of a local D1 SQL query.
type LocalD1QueryResult struct {
	Columns   []string
	Rows      [][]interface{}
	Meta      string // "rows_read: N, rows_written: N"
	ChangedDB bool
}

// ExecuteLocalD1Query runs a SQL query against a local D1 database via wrangler CLI.
// Uses: npx wrangler d1 execute <DB_NAME> --local --command="<SQL>" --json --config <path>
func ExecuteLocalD1Query(ctx context.Context, lr LocalResource, sql string) (*LocalD1QueryResult, error) {
	args := []string{"wrangler", "d1", "execute", lr.BindingName,
		"--local",
		"--command=" + sql,
		"--json",
		"--config", lr.ConfigPath,
	}
	if lr.EnvName != "" && lr.EnvName != "default" {
		args = append(args, "--env", lr.EnvName)
	}

	cmd := exec.CommandContext(ctx, "npx", args...)
	cmd.Dir = lr.ProjectDir
	cmd.Env = append(os.Environ(), "CI=true")

	out, err := cmd.CombinedOutput()
	if err != nil {
		// Try to extract a useful error message from the output
		outStr := strings.TrimSpace(string(out))
		if outStr != "" {
			return nil, fmt.Errorf("%s", outStr)
		}
		return nil, fmt.Errorf("wrangler d1 execute failed: %w", err)
	}

	return parseLocalD1Output(out)
}

// parseLocalD1Output parses the JSON output from `wrangler d1 execute --json`.
// Format: [{"results": [...], "success": true, "meta": {...}}]
func parseLocalD1Output(data []byte) (*LocalD1QueryResult, error) {
	// wrangler d1 execute --json outputs an array of result objects
	var results []struct {
		Results []map[string]interface{} `json:"results"`
		Success bool                     `json:"success"`
		Meta    struct {
			ChangedDB   bool    `json:"changed_db"`
			Changes     float64 `json:"changes"`
			Duration    float64 `json:"duration"`
			RowsRead    float64 `json:"rows_read"`
			RowsWritten float64 `json:"rows_written"`
			LastRowID   float64 `json:"last_row_id"`
			SizeAfter   float64 `json:"size_after"`
		} `json:"meta"`
	}

	if err := json.Unmarshal(data, &results); err != nil {
		return nil, fmt.Errorf("failed to parse d1 output: %w", err)
	}

	if len(results) == 0 {
		return &LocalD1QueryResult{}, nil
	}

	r := results[0]

	// Build meta string
	var metaParts []string
	if r.Meta.RowsRead > 0 {
		metaParts = append(metaParts, fmt.Sprintf("Read: %.0f", r.Meta.RowsRead))
	}
	if r.Meta.RowsWritten > 0 {
		metaParts = append(metaParts, fmt.Sprintf("Written: %.0f", r.Meta.RowsWritten))
	}
	if r.Meta.Duration > 0 {
		metaParts = append(metaParts, fmt.Sprintf("%.1fms", r.Meta.Duration))
	}
	if r.Meta.Changes > 0 {
		metaParts = append(metaParts, fmt.Sprintf("Changes: %.0f", r.Meta.Changes))
	}

	result := &LocalD1QueryResult{
		Meta:      strings.Join(metaParts, "  "),
		ChangedDB: r.Meta.ChangedDB,
	}

	// Extract columns from the first result row
	if len(r.Results) > 0 {
		first := r.Results[0]
		for k := range first {
			result.Columns = append(result.Columns, k)
		}
		// Convert to rows
		for _, row := range r.Results {
			var vals []interface{}
			for _, col := range result.Columns {
				vals = append(vals, row[col])
			}
			result.Rows = append(result.Rows, vals)
		}
	}

	return result, nil
}

// LocalKVKeyEntry represents a single key from a local KV namespace.
type LocalKVKeyEntry struct {
	Name       string
	Expiration float64 // UNIX timestamp, 0 if no expiration
	Metadata   interface{}
}

// ListLocalKVKeys lists keys from a local KV namespace via wrangler CLI.
// Uses: npx wrangler kv key list --binding=<BINDING> --local --config <path> [--prefix=<PREFIX>]
func ListLocalKVKeys(ctx context.Context, lr LocalResource, prefix string) ([]LocalKVKeyEntry, error) {
	args := []string{"wrangler", "kv", "key", "list",
		"--binding=" + lr.BindingName,
		"--local",
		"--config", lr.ConfigPath,
	}
	if prefix != "" {
		args = append(args, "--prefix="+prefix)
	}
	if lr.EnvName != "" && lr.EnvName != "default" {
		args = append(args, "--env", lr.EnvName)
	}

	cmd := exec.CommandContext(ctx, "npx", args...)
	cmd.Dir = lr.ProjectDir
	cmd.Env = append(os.Environ(), "CI=true")

	out, err := cmd.CombinedOutput()
	if err != nil {
		outStr := strings.TrimSpace(string(out))
		if outStr != "" {
			return nil, fmt.Errorf("%s", outStr)
		}
		return nil, fmt.Errorf("wrangler kv key list failed: %w", err)
	}

	var keys []LocalKVKeyEntry
	if err := json.Unmarshal(out, &keys); err != nil {
		return nil, fmt.Errorf("failed to parse kv key list output: %w", err)
	}

	return keys, nil
}

// GetLocalKVValue fetches a single value from a local KV namespace via wrangler CLI.
// Uses: npx wrangler kv key get <KEY> --binding=<BINDING> --local --config <path>
func GetLocalKVValue(ctx context.Context, lr LocalResource, keyName string) (string, error) {
	args := []string{"wrangler", "kv", "key", "get", keyName,
		"--binding=" + lr.BindingName,
		"--local",
		"--config", lr.ConfigPath,
	}
	if lr.EnvName != "" && lr.EnvName != "default" {
		args = append(args, "--env", lr.EnvName)
	}

	cmd := exec.CommandContext(ctx, "npx", args...)
	cmd.Dir = lr.ProjectDir
	cmd.Env = append(os.Environ(), "CI=true")

	out, err := cmd.CombinedOutput()
	if err != nil {
		outStr := strings.TrimSpace(string(out))
		if outStr != "" {
			return "", fmt.Errorf("%s", outStr)
		}
		return "", fmt.Errorf("wrangler kv key get failed: %w", err)
	}

	return string(out), nil
}

// DiscoverLocalResources extracts D1 and KV bindings from a wrangler config
// to produce local resource entries for an active dev session.
func DiscoverLocalResources(cfg *WranglerConfig, envName string) []LocalResource {
	if cfg == nil {
		return nil
	}

	projectDir := filepath.Dir(cfg.Path)

	// Determine which bindings to use — environment-specific if available, else top-level
	bindings := cfg.Bindings
	if envName != "" && envName != "default" {
		if env, ok := cfg.Environments[envName]; ok && len(env.Bindings) > 0 {
			bindings = env.Bindings
		}
	}

	var resources []LocalResource
	for _, b := range bindings {
		switch b.Type {
		case "kv_namespace":
			resources = append(resources, LocalResource{
				BindingName:  b.Name,
				ResourceType: "KV",
				ResourceName: b.Name, // binding name as display name
				ResourceID:   b.ResourceID,
				ConfigPath:   cfg.Path,
				ProjectDir:   projectDir,
				EnvName:      envName,
			})
		case "d1":
			dbName := b.DisplayName
			if dbName == "" {
				dbName = b.Name // fallback to binding name if no database_name
			}
			resources = append(resources, LocalResource{
				BindingName:  dbName, // Use database_name for d1 execute CLI command
				ResourceType: "D1",
				ResourceName: dbName,
				ResourceID:   b.ResourceID,
				ConfigPath:   cfg.Path,
				ProjectDir:   projectDir,
				EnvName:      envName,
			})
		}
	}

	return resources
}

// ListLocalKVKeysWithValues lists keys and fetches each value (up to limit).
// Returns entries compatible with the KV data explorer display.
func ListLocalKVKeysWithValues(ctx context.Context, lr LocalResource, prefix string, limit int) ([]LocalKVKeyValueEntry, error) {
	if limit <= 0 {
		limit = 20
	}

	keys, err := ListLocalKVKeys(ctx, lr, prefix)
	if err != nil {
		return nil, err
	}

	if len(keys) > limit {
		keys = keys[:limit]
	}

	entries := make([]LocalKVKeyValueEntry, 0, len(keys))
	for _, k := range keys {
		entry := LocalKVKeyValueEntry{
			Name: k.Name,
		}
		if k.Expiration > 0 {
			entry.Expiration = time.Unix(int64(k.Expiration), 0)
		}

		// Fetch value
		val, err := GetLocalKVValue(ctx, lr, k.Name)
		if err != nil {
			entry.Value = fmt.Sprintf("(error: %s)", err)
		} else {
			entry.Value = val
			entry.ValueSize = len(val)
		}

		entries = append(entries, entry)
	}

	return entries, nil
}

// LocalKVKeyValueEntry is a key-value pair with metadata, for display in the explorer.
type LocalKVKeyValueEntry struct {
	Name       string
	Value      string
	Expiration time.Time
	ValueSize  int
}

// LocalSchemaTable mirrors service.SchemaTable for local D1 schema introspection.
type LocalSchemaTable struct {
	Name    string
	Columns []LocalSchemaColumn
	FKs     []LocalSchemaFK
}

// LocalSchemaColumn mirrors service.SchemaColumn.
type LocalSchemaColumn struct {
	Name    string
	Type    string
	NotNull bool
	PK      bool
}

// LocalSchemaFK mirrors service.SchemaFK.
type LocalSchemaFK struct {
	FromCol string
	ToTable string
	ToCol   string
}

// QueryLocalD1Schema introspects a local D1 database schema via wrangler CLI.
// Runs sqlite_master + PRAGMA table_info + PRAGMA foreign_key_list for each table.
func QueryLocalD1Schema(ctx context.Context, lr LocalResource) ([]LocalSchemaTable, error) {
	// Step 1: Get all user table names
	tableResult, err := ExecuteLocalD1Query(ctx, lr,
		"SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%' AND name NOT LIKE '_cf_%' ORDER BY name")
	if err != nil {
		return nil, fmt.Errorf("failed to query tables: %w", err)
	}

	if len(tableResult.Rows) == 0 {
		return nil, nil
	}

	// Extract table names from the result
	nameColIdx := -1
	for i, col := range tableResult.Columns {
		if col == "name" {
			nameColIdx = i
			break
		}
	}
	if nameColIdx < 0 {
		return nil, fmt.Errorf("no 'name' column in sqlite_master result")
	}

	var tables []LocalSchemaTable
	for _, row := range tableResult.Rows {
		if nameColIdx >= len(row) {
			continue
		}
		tableName := fmt.Sprintf("%v", row[nameColIdx])
		if tableName == "" {
			continue
		}

		table := LocalSchemaTable{Name: tableName}

		// Step 2: Get columns via PRAGMA table_info
		colResult, err := ExecuteLocalD1Query(ctx, lr,
			fmt.Sprintf("PRAGMA table_info('%s')", tableName))
		if err == nil && len(colResult.Columns) > 0 {
			// Build column index map for PRAGMA table_info output
			colIdx := make(map[string]int)
			for i, c := range colResult.Columns {
				colIdx[c] = i
			}
			for _, cr := range colResult.Rows {
				col := LocalSchemaColumn{
					Name: localStrFromRow(cr, colIdx, "name"),
					Type: localStrFromRow(cr, colIdx, "type"),
				}
				if localNumFromRow(cr, colIdx, "notnull") == 1 {
					col.NotNull = true
				}
				if localNumFromRow(cr, colIdx, "pk") > 0 {
					col.PK = true
				}
				table.Columns = append(table.Columns, col)
			}
		}

		// Step 3: Get foreign keys via PRAGMA foreign_key_list
		fkResult, err := ExecuteLocalD1Query(ctx, lr,
			fmt.Sprintf("PRAGMA foreign_key_list('%s')", tableName))
		if err == nil && len(fkResult.Columns) > 0 {
			fkIdx := make(map[string]int)
			for i, c := range fkResult.Columns {
				fkIdx[c] = i
			}
			for _, fr := range fkResult.Rows {
				fk := LocalSchemaFK{
					FromCol: localStrFromRow(fr, fkIdx, "from"),
					ToTable: localStrFromRow(fr, fkIdx, "table"),
					ToCol:   localStrFromRow(fr, fkIdx, "to"),
				}
				if fk.FromCol != "" && fk.ToTable != "" {
					table.FKs = append(table.FKs, fk)
				}
			}
		}

		tables = append(tables, table)
	}

	return tables, nil
}

// localStrFromRow extracts a string value from a row slice using a column index map.
func localStrFromRow(row []interface{}, colIdx map[string]int, key string) string {
	idx, ok := colIdx[key]
	if !ok || idx >= len(row) || row[idx] == nil {
		return ""
	}
	if s, ok := row[idx].(string); ok {
		return s
	}
	return fmt.Sprintf("%v", row[idx])
}

// localNumFromRow extracts a numeric value from a row slice using a column index map.
func localNumFromRow(row []interface{}, colIdx map[string]int, key string) float64 {
	idx, ok := colIdx[key]
	if !ok || idx >= len(row) || row[idx] == nil {
		return 0
	}
	if f, ok := row[idx].(float64); ok {
		return f
	}
	return 0
}
