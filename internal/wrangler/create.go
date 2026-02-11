package wrangler

import (
	"context"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

// CreateResourceCmd describes a wrangler CLI resource creation command.
type CreateResourceCmd struct {
	// ResourceType is one of: "d1", "kv", "r2", "queue"
	ResourceType string
	// Name is the resource name to create.
	Name string
	// AccountID is passed as CLOUDFLARE_ACCOUNT_ID env var.
	AccountID string
}

// CreateResourceResult holds the output of a resource creation command.
type CreateResourceResult struct {
	Success    bool
	Output     string // combined stdout+stderr (for display in the TUI)
	ResourceID string // parsed resource ID from CLI output (database_id, namespace_id, etc.)
}

// CreateResource runs a synchronous wrangler CLI command to create a resource.
// Unlike the streaming Runner, this blocks until the command completes.
func CreateResource(ctx context.Context, cmd CreateResourceCmd) CreateResourceResult {
	args := buildCreateArgs(cmd)

	execCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	c := exec.CommandContext(execCtx, "npx", args...)
	if cmd.AccountID != "" {
		c.Env = append(os.Environ(), "CLOUDFLARE_ACCOUNT_ID="+cmd.AccountID)
	}

	out, err := c.CombinedOutput()
	output := strings.TrimSpace(string(out))

	if err != nil {
		return CreateResourceResult{
			Success: false,
			Output:  output,
		}
	}

	// Parse the resource ID from the CLI output
	resourceID := parseResourceID(cmd.ResourceType, output)

	return CreateResourceResult{
		Success:    true,
		Output:     output,
		ResourceID: resourceID,
	}
}

// buildCreateArgs builds the CLI arguments for resource creation.
func buildCreateArgs(cmd CreateResourceCmd) []string {
	args := []string{"wrangler"}

	switch cmd.ResourceType {
	case "d1":
		args = append(args, "d1", "create", cmd.Name)
	case "kv":
		args = append(args, "kv", "namespace", "create", cmd.Name)
	case "r2":
		args = append(args, "r2", "bucket", "create", cmd.Name)
	case "queue":
		args = append(args, "queues", "create", cmd.Name)
	default:
		args = append(args, cmd.ResourceType, "create", cmd.Name)
	}

	return args
}

// ResourceTypeLabel returns a human-readable label for a resource type.
func ResourceTypeLabel(resourceType string) string {
	switch resourceType {
	case "d1":
		return "D1 Database"
	case "kv":
		return "KV Namespace"
	case "r2":
		return "R2 Bucket"
	case "queue":
		return "Queue"
	default:
		return resourceType
	}
}

// SuggestBindingName generates a default binding name from a resource name.
// Converts "my-staging-db" to "MY_STAGING_DB".
func SuggestBindingName(name string) string {
	s := strings.ToUpper(name)
	s = strings.ReplaceAll(s, "-", "_")
	s = strings.ReplaceAll(s, " ", "_")

	// Remove any characters that aren't valid JS identifiers
	var b strings.Builder
	for _, c := range s {
		if (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_' {
			b.WriteRune(c)
		}
	}
	result := b.String()

	// Ensure it doesn't start with a digit
	if len(result) > 0 && result[0] >= '0' && result[0] <= '9' {
		result = "_" + result
	}

	if result == "" {
		result = "BINDING"
	}

	return result
}

// parseResourceID extracts the resource ID from wrangler CLI creation output.
// Different resource types output IDs in different formats:
//   - D1: "database_id = \"<uuid>\""
//   - KV: "id: \"<hex-id>\""  or  "id = \"<hex-id>\""
//   - R2: bucket name IS the identifier (no UUID needed)
//   - Queue: queue name IS the identifier (no UUID needed)
func parseResourceID(resourceType, output string) string {
	switch resourceType {
	case "d1":
		// Look for database_id = "..." in the output
		re := regexp.MustCompile(`database_id\s*[=:]\s*"?([a-f0-9-]{36})"?`)
		if m := re.FindStringSubmatch(output); len(m) > 1 {
			return m[1]
		}
		// Also try a bare UUID on its own line
		uuidRe := regexp.MustCompile(`[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}`)
		if m := uuidRe.FindString(output); m != "" {
			return m
		}
	case "kv":
		// Look for id: "..." or id = "..." or namespace_id = "..."
		re := regexp.MustCompile(`(?:namespace_)?id\s*[=:]\s*"?([a-f0-9]{32})"?`)
		if m := re.FindStringSubmatch(output); len(m) > 1 {
			return m[1]
		}
		// Also try any 32-char hex string
		hexRe := regexp.MustCompile(`[a-f0-9]{32}`)
		if m := hexRe.FindString(output); m != "" {
			return m
		}
	case "r2":
		// R2 bucket name is the identifier — return empty to use the name
		return ""
	case "queue":
		// Queue name is the identifier — return empty to use the name
		return ""
	}
	return ""
}
