package wrangler

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/tidwall/jsonc"
	"github.com/tidwall/sjson"
)

// AddEnvironment writes a new empty environment section into a wrangler config file.
// configPath is the absolute path to the config file.
// envName is the name for the new environment (e.g. "staging").
// Returns an error if the environment already exists or the file cannot be written.
func AddEnvironment(configPath, envName string) error {
	absPath, err := filepath.Abs(configPath)
	if err != nil {
		return fmt.Errorf("invalid config path: %w", err)
	}

	data, err := os.ReadFile(absPath)
	if err != nil {
		return fmt.Errorf("failed to read config file: %w", err)
	}

	ext := strings.ToLower(filepath.Ext(absPath))

	var result []byte
	switch ext {
	case ".toml":
		result, err = addEnvironmentTOML(data, envName)
	case ".json", ".jsonc":
		result, err = addEnvironmentJSON(data, envName)
	default:
		return fmt.Errorf("unsupported config format: %s", ext)
	}
	if err != nil {
		return err
	}

	return os.WriteFile(absPath, result, 0644)
}

// addEnvironmentTOML appends a new [env.<name>] section to TOML config text.
func addEnvironmentTOML(data []byte, envName string) ([]byte, error) {
	content := string(data)

	// Check if the env section already exists
	sectionPattern := fmt.Sprintf(`(?m)^\[env\.%s\]`, regexp.QuoteMeta(envName))
	if regexp.MustCompile(sectionPattern).MatchString(content) {
		return nil, fmt.Errorf("environment %q already exists", envName)
	}

	// Append the new env section at the end of the file
	result := strings.TrimRight(content, "\n") + "\n\n[env." + envName + "]\n"
	return []byte(result), nil
}

// addEnvironmentJSON inserts a new empty env object into a JSON/JSONC config.
// Note: JSONC comments are stripped by this operation.
func addEnvironmentJSON(data []byte, envName string) ([]byte, error) {
	clean := jsonc.ToJSON(data)

	path := "env." + envName
	result, err := sjson.SetRawBytes(clean, path, []byte("{}"))
	if err != nil {
		return nil, fmt.Errorf("failed to update JSON config: %w", err)
	}

	// Pretty-print the result
	var pretty bytes.Buffer
	if err := json.Indent(&pretty, result, "", "  "); err != nil {
		return result, nil
	}
	return append(pretty.Bytes(), '\n'), nil
}

// DeleteEnvironment removes an environment section and all its associated bindings
// from a wrangler config file. The "default" environment cannot be deleted.
func DeleteEnvironment(configPath, envName string) error {
	if envName == "" || envName == "default" {
		return fmt.Errorf("cannot delete the default environment")
	}

	absPath, err := filepath.Abs(configPath)
	if err != nil {
		return fmt.Errorf("invalid config path: %w", err)
	}

	data, err := os.ReadFile(absPath)
	if err != nil {
		return fmt.Errorf("failed to read config file: %w", err)
	}

	ext := strings.ToLower(filepath.Ext(absPath))

	var result []byte
	switch ext {
	case ".toml":
		result, err = deleteEnvironmentTOML(data, envName)
	case ".json", ".jsonc":
		result, err = deleteEnvironmentJSON(data, envName)
	default:
		return fmt.Errorf("unsupported config format: %s", ext)
	}
	if err != nil {
		return err
	}

	return os.WriteFile(absPath, result, 0644)
}

// deleteEnvironmentTOML removes the [env.<name>] section and all its sub-sections
// (e.g. [[env.<name>.d1_databases]]) from TOML content.
func deleteEnvironmentTOML(data []byte, envName string) ([]byte, error) {
	content := string(data)

	// Find the start of [env.<name>] — the main section header
	sectionPattern := fmt.Sprintf(`(?m)^\[env\.%s\]`, regexp.QuoteMeta(envName))
	re := regexp.MustCompile(sectionPattern)
	loc := re.FindStringIndex(content)

	if loc == nil {
		return nil, fmt.Errorf("environment %q not found in config", envName)
	}

	// Extend start backwards to consume at most one preceding blank line.
	// We keep the newline that terminates the previous content line.
	startIdx := loc[0]
	if startIdx >= 2 && content[startIdx-1] == '\n' && content[startIdx-2] == '\n' {
		// There's a blank line before the section — consume it
		startIdx--
	}

	// Find the end of this env section
	endIdx := findEnvSectionEnd(content, loc[1], envName)

	// Remove the section
	result := content[:startIdx] + content[endIdx:]

	// Clean up multiple consecutive blank lines (replace 3+ newlines with 2)
	multiBlank := regexp.MustCompile(`\n{3,}`)
	result = multiBlank.ReplaceAllString(result, "\n\n")

	// Trim trailing whitespace
	result = strings.TrimRight(result, "\n\t ") + "\n"

	return []byte(result), nil
}

// deleteEnvironmentJSON removes the env.<name> key from a JSON/JSONC config.
// Note: JSONC comments are stripped by this operation.
func deleteEnvironmentJSON(data []byte, envName string) ([]byte, error) {
	clean := jsonc.ToJSON(data)

	path := "env." + envName
	result, err := sjson.DeleteBytes(clean, path)
	if err != nil {
		return nil, fmt.Errorf("failed to update JSON config: %w", err)
	}

	// Pretty-print the result
	var pretty bytes.Buffer
	if err := json.Indent(&pretty, result, "", "  "); err != nil {
		return result, nil
	}
	return append(pretty.Bytes(), '\n'), nil
}

// BindingDef describes a binding to be written into a wrangler config file.
type BindingDef struct {
	// Type is one of: "d1", "kv", "r2", "queue"
	Type string
	// BindingName is the JS variable name (e.g. "MY_DB").
	BindingName string
	// ResourceID is the identifier (database_id, namespace id, bucket_name, queue_name).
	ResourceID string
	// ResourceName is the human name (used for D1's database_name field).
	ResourceName string
}

// AddBinding writes a binding definition into a wrangler config file.
// configPath is the absolute path to the config file.
// envName is the target environment ("default" or "" for top-level, otherwise the named env).
func AddBinding(configPath, envName string, binding BindingDef) error {
	absPath, err := filepath.Abs(configPath)
	if err != nil {
		return fmt.Errorf("invalid config path: %w", err)
	}

	data, err := os.ReadFile(absPath)
	if err != nil {
		return fmt.Errorf("failed to read config file: %w", err)
	}

	ext := strings.ToLower(filepath.Ext(absPath))
	isTopLevel := envName == "" || envName == "default"

	var result []byte
	switch ext {
	case ".toml":
		result, err = addBindingTOML(data, envName, isTopLevel, binding)
	case ".json", ".jsonc":
		result, err = addBindingJSON(data, isTopLevel, envName, binding)
	default:
		return fmt.Errorf("unsupported config format: %s", ext)
	}
	if err != nil {
		return err
	}

	return os.WriteFile(absPath, result, 0644)
}

// --- TOML writer ---

// addBindingTOML inserts a binding block into TOML config text.
func addBindingTOML(data []byte, envName string, isTopLevel bool, b BindingDef) ([]byte, error) {
	content := string(data)

	if isTopLevel {
		block := formatTOMLBinding(b)
		// Append the binding block before any [env.*] section, or at EOF.
		insertIdx := findTopLevelInsertPoint(content)
		result := content[:insertIdx] + block + content[insertIdx:]
		return []byte(result), nil
	}

	// Insert into [env.<name>] section using env-prefixed syntax
	block := formatTOMLEnvBinding(envName, b)
	result, err := insertIntoEnvSection(content, envName, block)
	if err != nil {
		return nil, err
	}
	return []byte(result), nil
}

// formatTOMLBinding generates a TOML block for a binding definition.
func formatTOMLBinding(b BindingDef) string {
	var sb strings.Builder
	sb.WriteString("\n")

	switch b.Type {
	case "d1":
		sb.WriteString("[[d1_databases]]\n")
		sb.WriteString(fmt.Sprintf("binding = %q\n", b.BindingName))
		sb.WriteString(fmt.Sprintf("database_name = %q\n", b.ResourceName))
		sb.WriteString(fmt.Sprintf("database_id = %q\n", b.ResourceID))
	case "kv":
		sb.WriteString("[[kv_namespaces]]\n")
		sb.WriteString(fmt.Sprintf("binding = %q\n", b.BindingName))
		sb.WriteString(fmt.Sprintf("id = %q\n", b.ResourceID))
	case "r2":
		sb.WriteString("[[r2_buckets]]\n")
		sb.WriteString(fmt.Sprintf("binding = %q\n", b.BindingName))
		sb.WriteString(fmt.Sprintf("bucket_name = %q\n", b.ResourceID))
	case "queue":
		sb.WriteString("[[queues.producers]]\n")
		sb.WriteString(fmt.Sprintf("binding = %q\n", b.BindingName))
		sb.WriteString(fmt.Sprintf("queue = %q\n", b.ResourceName))
	}

	return sb.String()
}

// formatTOMLEnvBinding generates a TOML block for a binding inside an [env.*] section.
// Uses the dotted env prefix: [[env.<name>.d1_databases]] etc.
func formatTOMLEnvBinding(envName string, b BindingDef) string {
	var sb strings.Builder
	sb.WriteString("\n")

	prefix := fmt.Sprintf("env.%s", envName)

	switch b.Type {
	case "d1":
		sb.WriteString(fmt.Sprintf("[[%s.d1_databases]]\n", prefix))
		sb.WriteString(fmt.Sprintf("binding = %q\n", b.BindingName))
		sb.WriteString(fmt.Sprintf("database_name = %q\n", b.ResourceName))
		sb.WriteString(fmt.Sprintf("database_id = %q\n", b.ResourceID))
	case "kv":
		sb.WriteString(fmt.Sprintf("[[%s.kv_namespaces]]\n", prefix))
		sb.WriteString(fmt.Sprintf("binding = %q\n", b.BindingName))
		sb.WriteString(fmt.Sprintf("id = %q\n", b.ResourceID))
	case "r2":
		sb.WriteString(fmt.Sprintf("[[%s.r2_buckets]]\n", prefix))
		sb.WriteString(fmt.Sprintf("binding = %q\n", b.BindingName))
		sb.WriteString(fmt.Sprintf("bucket_name = %q\n", b.ResourceID))
	case "queue":
		sb.WriteString(fmt.Sprintf("[[%s.queues.producers]]\n", prefix))
		sb.WriteString(fmt.Sprintf("binding = %q\n", b.BindingName))
		sb.WriteString(fmt.Sprintf("queue = %q\n", b.ResourceName))
	}

	return sb.String()
}

// findTopLevelInsertPoint finds the byte index in TOML content where a top-level
// binding block should be inserted — just before the first [env.*] section header,
// or at the end of the file if no env sections exist.
func findTopLevelInsertPoint(content string) int {
	// Match [env] or [env.xxx] or [[env.xxx.yyy]] at the start of a line (not in comments)
	re := regexp.MustCompile(`(?m)^[^#\n]*\[+\s*env[\.\]\s]`)
	loc := re.FindStringIndex(content)
	if loc != nil {
		return loc[0]
	}
	return len(content)
}

// findEnvSectionEnd finds the byte index in content where the env section ends.
// It scans from afterStart (the position right after the [env.<name>] header match)
// and finds the first TOML section header that does NOT belong to the given env.
// Sub-sections like [[env.<name>.d1_databases]] are considered part of the env.
// Returns the byte index of the next unrelated section, or len(content) if none.
func findEnvSectionEnd(content string, afterStart int, envName string) int {
	// Pattern matching any TOML section header at the start of a line
	anySectionRe := regexp.MustCompile(`(?m)^\[`)
	// Pattern matching headers that belong to this env: [env.<name>] or [[env.<name>.xxx]]
	envOwnedPrefix := fmt.Sprintf("env.%s.", envName)
	envOwnedExact := fmt.Sprintf("env.%s]", envName)

	afterSection := content[afterStart:]
	offset := 0

	for {
		candidate := anySectionRe.FindStringIndex(afterSection[offset:])
		if candidate == nil {
			break // no more sections — env extends to EOF
		}

		absPos := afterStart + offset + candidate[0]
		// Extract the header text from '[' to end of line
		lineEnd := strings.IndexByte(content[absPos:], '\n')
		var headerLine string
		if lineEnd >= 0 {
			headerLine = content[absPos : absPos+lineEnd]
		} else {
			headerLine = content[absPos:]
		}

		// Strip leading brackets to get the key path
		inner := strings.TrimLeft(headerLine, "[")
		inner = strings.TrimSpace(inner)

		// Check if this header belongs to the current env
		if strings.HasPrefix(inner, envOwnedPrefix) || strings.HasPrefix(inner, envOwnedExact) {
			// This is a sub-section of our env — skip it and keep looking
			offset += candidate[1]
			continue
		}

		// This section does NOT belong to our env — this is the boundary
		return absPos
	}

	return len(content)
}

// insertIntoEnvSection inserts a binding block into the [env.<name>] section of TOML content.
// The block parameter should already be formatted with the env-prefixed syntax
// (e.g. [[env.staging.d1_databases]]).
func insertIntoEnvSection(content, envName, block string) (string, error) {
	// Find the start of [env.<name>] section
	sectionPattern := fmt.Sprintf(`(?m)^\[env\.%s\]`, regexp.QuoteMeta(envName))
	re := regexp.MustCompile(sectionPattern)
	loc := re.FindStringIndex(content)

	if loc == nil {
		// No explicit [env.<name>] section exists — append at EOF with the section header
		return content + "\n[env." + envName + "]\n" + block + "\n", nil
	}

	// Find the end of this env section
	insertIdx := findEnvSectionEnd(content, loc[1], envName)

	result := content[:insertIdx] + block + content[insertIdx:]
	return result, nil
}

// --- JSON/JSONC writer ---

// addBindingJSON inserts a binding into a JSON/JSONC config.
// Note: JSONC comments are stripped by this operation.
func addBindingJSON(data []byte, isTopLevel bool, envName string, b BindingDef) ([]byte, error) {
	// Strip JSONC comments to get valid JSON
	clean := jsonc.ToJSON(data)

	entry := buildJSONEntry(b)

	var path string
	if isTopLevel {
		path = jsonArrayKey(b.Type) + ".-1"
	} else {
		path = "env." + envName + "." + jsonArrayKey(b.Type) + ".-1"
	}

	result, err := sjson.SetRawBytes(clean, path, entry)
	if err != nil {
		return nil, fmt.Errorf("failed to update JSON config: %w", err)
	}

	// Pretty-print the result
	var pretty bytes.Buffer
	if err := json.Indent(&pretty, result, "", "  "); err != nil {
		// If indent fails, return the raw result
		return result, nil
	}
	return append(pretty.Bytes(), '\n'), nil
}

// jsonArrayKey returns the JSON key for a binding type's array.
func jsonArrayKey(resourceType string) string {
	switch resourceType {
	case "d1":
		return "d1_databases"
	case "kv":
		return "kv_namespaces"
	case "r2":
		return "r2_buckets"
	case "queue":
		// For queues, the JSON path needs special handling since it's nested
		return "queues.producers"
	default:
		return resourceType
	}
}

// buildJSONEntry returns the raw JSON bytes for a binding entry.
func buildJSONEntry(b BindingDef) []byte {
	var m map[string]string

	switch b.Type {
	case "d1":
		m = map[string]string{
			"binding":       b.BindingName,
			"database_name": b.ResourceName,
			"database_id":   b.ResourceID,
		}
	case "kv":
		m = map[string]string{
			"binding": b.BindingName,
			"id":      b.ResourceID,
		}
	case "r2":
		m = map[string]string{
			"binding":     b.BindingName,
			"bucket_name": b.ResourceID,
		}
	case "queue":
		m = map[string]string{
			"binding": b.BindingName,
			"queue":   b.ResourceName,
		}
	default:
		m = map[string]string{
			"binding": b.BindingName,
			"id":      b.ResourceID,
		}
	}

	data, _ := json.Marshal(m)
	return data
}
