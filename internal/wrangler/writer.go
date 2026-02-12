package wrangler

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/tidwall/gjson"
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

// RemoveBinding removes a binding entry from a wrangler config file.
// bindingName is the JS variable name (e.g. "MY_KV").
// bindingType is the normalized type from Binding.Type (e.g. "kv_namespace", "d1", "r2_bucket", "queue_producer").
// envName is the target environment ("default" or "" for top-level, otherwise the named env).
func RemoveBinding(configPath, envName, bindingName, bindingType string) error {
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
		result, err = removeBindingTOML(data, envName, isTopLevel, bindingName, bindingType)
	case ".json", ".jsonc":
		result, err = removeBindingJSON(data, isTopLevel, envName, bindingName, bindingType)
	default:
		return fmt.Errorf("unsupported config format: %s", ext)
	}
	if err != nil {
		return err
	}

	return os.WriteFile(absPath, result, 0644)
}

// bindingTypeToConfigKey maps a normalized Binding.Type to the TOML/JSON config key.
func bindingTypeToConfigKey(bindingType string) string {
	switch bindingType {
	case "kv_namespace":
		return "kv_namespaces"
	case "r2_bucket":
		return "r2_buckets"
	case "d1":
		return "d1_databases"
	case "service":
		return "services"
	case "queue_producer":
		return "queues.producers"
	case "queue_consumer":
		return "queues.consumers"
	case "durable_object_namespace":
		return "durable_objects.bindings"
	case "vectorize":
		return "vectorize"
	case "hyperdrive":
		return "hyperdrive"
	case "analytics_engine":
		return "analytics_engine"
	case "ai":
		return "ai"
	default:
		return bindingType
	}
}

// bindingNameField returns the TOML/JSON field name that holds the binding's JS variable name
// for a given binding type. Most use "binding", but durable objects use "name".
func bindingNameField(bindingType string) string {
	if bindingType == "durable_object_namespace" {
		return "name"
	}
	return "binding"
}

// removeBindingTOML removes a binding entry from TOML config text.
// It finds the [[section]] block where the binding name matches and removes the entire block.
func removeBindingTOML(data []byte, envName string, isTopLevel bool, bindingName, bindingType string) ([]byte, error) {
	content := string(data)
	configKey := bindingTypeToConfigKey(bindingType)
	nameField := bindingNameField(bindingType)

	// Build the TOML array-of-tables header pattern
	var headerPrefix string
	if isTopLevel {
		headerPrefix = configKey
	} else {
		headerPrefix = fmt.Sprintf("env.%s.%s", envName, configKey)
	}

	// Pattern to match the array-of-tables header: [[kv_namespaces]] or [[env.staging.kv_namespaces]]
	headerPattern := fmt.Sprintf(`(?m)^\[\[%s\]\]`, regexp.QuoteMeta(headerPrefix))
	headerRe := regexp.MustCompile(headerPattern)

	// Pattern to match the binding name field within a block
	namePattern := fmt.Sprintf(`(?m)^\s*%s\s*=\s*['""]%s['""]`, regexp.QuoteMeta(nameField), regexp.QuoteMeta(bindingName))
	nameRe := regexp.MustCompile(namePattern)

	// Find all occurrences of the header and check which one contains our binding name
	matches := headerRe.FindAllStringIndex(content, -1)
	if len(matches) == 0 {
		return nil, fmt.Errorf("no %s bindings found in config", configKey)
	}

	for _, match := range matches {
		blockStart := match[0]
		// Find the end of this block: next [[ or [ header, or EOF
		blockEnd := len(content)
		remaining := content[match[1]:]
		nextHeader := regexp.MustCompile(`(?m)^\[`).FindStringIndex(remaining)
		if nextHeader != nil {
			blockEnd = match[1] + nextHeader[0]
		}

		block := content[blockStart:blockEnd]
		if nameRe.MatchString(block) {
			// Found the matching block — remove it
			// Extend start backwards to consume a preceding blank line
			removeStart := blockStart
			if removeStart >= 2 && content[removeStart-1] == '\n' && content[removeStart-2] == '\n' {
				removeStart--
			} else if removeStart >= 1 && content[removeStart-1] == '\n' {
				removeStart--
			}

			result := content[:removeStart] + content[blockEnd:]

			// Clean up multiple consecutive blank lines
			multiBlank := regexp.MustCompile(`\n{3,}`)
			result = multiBlank.ReplaceAllString(result, "\n\n")
			result = strings.TrimRight(result, "\n\t ") + "\n"

			return []byte(result), nil
		}
	}

	return nil, fmt.Errorf("binding %q not found in %s", bindingName, configKey)
}

// removeBindingJSON removes a binding entry from JSON/JSONC config.
func removeBindingJSON(data []byte, isTopLevel bool, envName, bindingName, bindingType string) ([]byte, error) {
	clean := jsonc.ToJSON(data)
	configKey := bindingTypeToConfigKey(bindingType)
	nameField := bindingNameField(bindingType)

	var arrayPath string
	if isTopLevel {
		arrayPath = configKey
	} else {
		arrayPath = "env." + envName + "." + configKey
	}

	// Find the index of the binding entry in the array
	arr := gjson.GetBytes(clean, arrayPath)
	if !arr.Exists() || !arr.IsArray() {
		return nil, fmt.Errorf("no %s bindings found in config", configKey)
	}

	deleteIdx := -1
	arr.ForEach(func(key, value gjson.Result) bool {
		if value.Get(nameField).String() == bindingName {
			deleteIdx = int(key.Int())
			return false // stop iteration
		}
		return true
	})

	if deleteIdx < 0 {
		return nil, fmt.Errorf("binding %q not found in %s", bindingName, configKey)
	}

	deletePath := fmt.Sprintf("%s.%d", arrayPath, deleteIdx)
	result, err := sjson.DeleteBytes(clean, deletePath)
	if err != nil {
		return nil, fmt.Errorf("failed to update JSON config: %w", err)
	}

	var pretty bytes.Buffer
	if err := json.Indent(&pretty, result, "", "  "); err != nil {
		return result, nil
	}
	return append(pretty.Bytes(), '\n'), nil
}

// SetVar adds or updates an environment variable in a wrangler config file.
// envName is the target environment ("default" or "" for top-level, otherwise the named env).
func SetVar(configPath, envName, varName, value string) error {
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
		result, err = setVarTOML(data, envName, isTopLevel, varName, value)
	case ".json", ".jsonc":
		result, err = setVarJSON(data, isTopLevel, envName, varName, value)
	default:
		return fmt.Errorf("unsupported config format: %s", ext)
	}
	if err != nil {
		return err
	}

	return os.WriteFile(absPath, result, 0644)
}

// setVarTOML adds or updates a var in a TOML config.
func setVarTOML(data []byte, envName string, isTopLevel bool, varName, value string) ([]byte, error) {
	content := string(data)

	// Determine the section header we're looking for
	var sectionHeader string
	if isTopLevel {
		sectionHeader = "[vars]"
	} else {
		sectionHeader = fmt.Sprintf("[env.%s.vars]", envName)
	}

	// Try to find the existing [vars] section
	sectionPattern := fmt.Sprintf(`(?m)^%s\s*$`, regexp.QuoteMeta(sectionHeader))
	sectionRe := regexp.MustCompile(sectionPattern)
	sectionLoc := sectionRe.FindStringIndex(content)

	if sectionLoc != nil {
		// Section exists — find the end of it (next [ header or EOF)
		afterSection := content[sectionLoc[1]:]
		nextHeaderRe := regexp.MustCompile(`(?m)^\[`)
		nextLoc := nextHeaderRe.FindStringIndex(afterSection)

		sectionEnd := len(content)
		if nextLoc != nil {
			sectionEnd = sectionLoc[1] + nextLoc[0]
		}

		sectionBody := content[sectionLoc[1]:sectionEnd]

		// Check if the var already exists in this section
		varPattern := fmt.Sprintf(`(?m)^%s\s*=\s*.*$`, regexp.QuoteMeta(varName))
		varRe := regexp.MustCompile(varPattern)
		varLoc := varRe.FindStringIndex(sectionBody)

		if varLoc != nil {
			// Replace existing value
			absStart := sectionLoc[1] + varLoc[0]
			absEnd := sectionLoc[1] + varLoc[1]
			newLine := fmt.Sprintf("%s = %q", varName, value)
			result := content[:absStart] + newLine + content[absEnd:]
			return []byte(result), nil
		}

		// Var doesn't exist — append it at the end of the section
		insertIdx := sectionEnd
		// Walk backwards past trailing blank lines to insert right after the last var
		for insertIdx > sectionLoc[1] && (content[insertIdx-1] == '\n' || content[insertIdx-1] == '\r') {
			insertIdx--
		}
		newLine := fmt.Sprintf("\n%s = %q\n", varName, value)
		// Add back the newlines we consumed
		trailingNewlines := content[insertIdx:sectionEnd]
		result := content[:insertIdx] + newLine + trailingNewlines + content[sectionEnd:]
		return []byte(result), nil
	}

	// Section doesn't exist — create it
	newSection := fmt.Sprintf("\n%s\n%s = %q\n", sectionHeader, varName, value)

	if isTopLevel {
		// Insert before [env.*] sections, or at EOF
		insertIdx := findTopLevelInsertPoint(content)
		result := content[:insertIdx] + newSection + content[insertIdx:]
		return []byte(result), nil
	}

	// For env-specific vars, insert into the env section
	result, err := insertIntoEnvSection(content, envName, newSection)
	if err != nil {
		return nil, err
	}
	return []byte(result), nil
}

// setVarJSON adds or updates a var in a JSON/JSONC config.
func setVarJSON(data []byte, isTopLevel bool, envName, varName, value string) ([]byte, error) {
	clean := jsonc.ToJSON(data)

	var path string
	if isTopLevel {
		path = "vars." + varName
	} else {
		path = "env." + envName + ".vars." + varName
	}

	result, err := sjson.SetBytes(clean, path, value)
	if err != nil {
		return nil, fmt.Errorf("failed to update JSON config: %w", err)
	}

	var pretty bytes.Buffer
	if err := json.Indent(&pretty, result, "", "  "); err != nil {
		return result, nil
	}
	return append(pretty.Bytes(), '\n'), nil
}

// RemoveVar removes an environment variable from a wrangler config file.
// envName is the target environment ("default" or "" for top-level, otherwise the named env).
func RemoveVar(configPath, envName, varName string) error {
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
		result, err = removeVarTOML(data, envName, isTopLevel, varName)
	case ".json", ".jsonc":
		result, err = removeVarJSON(data, isTopLevel, envName, varName)
	default:
		return fmt.Errorf("unsupported config format: %s", ext)
	}
	if err != nil {
		return err
	}

	return os.WriteFile(absPath, result, 0644)
}

// removeVarTOML removes a var line from a TOML config.
func removeVarTOML(data []byte, envName string, isTopLevel bool, varName string) ([]byte, error) {
	content := string(data)

	// Determine the section header
	var sectionHeader string
	if isTopLevel {
		sectionHeader = "[vars]"
	} else {
		sectionHeader = fmt.Sprintf("[env.%s.vars]", envName)
	}

	sectionPattern := fmt.Sprintf(`(?m)^%s\s*$`, regexp.QuoteMeta(sectionHeader))
	sectionRe := regexp.MustCompile(sectionPattern)
	sectionLoc := sectionRe.FindStringIndex(content)

	if sectionLoc == nil {
		return nil, fmt.Errorf("variable %q not found in config", varName)
	}

	// Find section end
	afterSection := content[sectionLoc[1]:]
	nextHeaderRe := regexp.MustCompile(`(?m)^\[`)
	nextLoc := nextHeaderRe.FindStringIndex(afterSection)

	sectionEnd := len(content)
	if nextLoc != nil {
		sectionEnd = sectionLoc[1] + nextLoc[0]
	}

	sectionBody := content[sectionLoc[1]:sectionEnd]

	// Find the var line
	varPattern := fmt.Sprintf(`(?m)^%s\s*=\s*.*\n?`, regexp.QuoteMeta(varName))
	varRe := regexp.MustCompile(varPattern)
	varLoc := varRe.FindStringIndex(sectionBody)

	if varLoc == nil {
		return nil, fmt.Errorf("variable %q not found in config", varName)
	}

	absStart := sectionLoc[1] + varLoc[0]
	absEnd := sectionLoc[1] + varLoc[1]

	result := content[:absStart] + content[absEnd:]

	// Clean up multiple consecutive blank lines
	multiBlank := regexp.MustCompile(`\n{3,}`)
	result = multiBlank.ReplaceAllString(result, "\n\n")
	result = strings.TrimRight(result, "\n\t ") + "\n"

	return []byte(result), nil
}

// removeVarJSON removes a var from a JSON/JSONC config.
func removeVarJSON(data []byte, isTopLevel bool, envName, varName string) ([]byte, error) {
	clean := jsonc.ToJSON(data)

	var path string
	if isTopLevel {
		path = "vars." + varName
	} else {
		path = "env." + envName + ".vars." + varName
	}

	result, err := sjson.DeleteBytes(clean, path)
	if err != nil {
		return nil, fmt.Errorf("failed to update JSON config: %w", err)
	}

	var pretty bytes.Buffer
	if err := json.Indent(&pretty, result, "", "  "); err != nil {
		return result, nil
	}
	return append(pretty.Bytes(), '\n'), nil
}

// AddCron appends a cron expression to the top-level [triggers].crons array.
// Triggers are top-level only in wrangler configs (not per-environment).
func AddCron(configPath, cron string) error {
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
		result, err = addCronTOML(data, cron)
	case ".json", ".jsonc":
		result, err = addCronJSON(data, cron)
	default:
		return fmt.Errorf("unsupported config format: %s", ext)
	}
	if err != nil {
		return err
	}

	return os.WriteFile(absPath, result, 0644)
}

// addCronTOML adds a cron to the [triggers] section in a TOML config.
func addCronTOML(data []byte, cron string) ([]byte, error) {
	content := string(data)

	// Look for existing [triggers] section
	sectionRe := regexp.MustCompile(`(?m)^\[triggers\]\s*$`)
	sectionLoc := sectionRe.FindStringIndex(content)

	if sectionLoc != nil {
		// [triggers] section exists — find the crons = [...] line
		afterSection := content[sectionLoc[1]:]
		nextHeaderRe := regexp.MustCompile(`(?m)^\[`)
		nextLoc := nextHeaderRe.FindStringIndex(afterSection)

		sectionEnd := len(content)
		if nextLoc != nil {
			sectionEnd = sectionLoc[1] + nextLoc[0]
		}

		sectionBody := content[sectionLoc[1]:sectionEnd]

		// Look for crons = [...] in the section
		cronsRe := regexp.MustCompile(`(?m)^crons\s*=\s*\[([^\]]*)\]`)
		cronsLoc := cronsRe.FindStringSubmatchIndex(sectionBody)

		if cronsLoc != nil {
			// crons array exists — append the new cron to it
			// cronsLoc[2..3] is the capture group (contents inside [...])
			arrayContents := sectionBody[cronsLoc[2]:cronsLoc[3]]
			trimmed := strings.TrimSpace(arrayContents)

			var newArrayContents string
			if trimmed == "" {
				newArrayContents = fmt.Sprintf("%q", cron)
			} else {
				newArrayContents = fmt.Sprintf("%s, %q", trimmed, cron)
			}

			newLine := fmt.Sprintf("crons = [%s]", newArrayContents)
			absStart := sectionLoc[1] + cronsLoc[0]
			absEnd := sectionLoc[1] + cronsLoc[1]
			result := content[:absStart] + newLine + content[absEnd:]
			return []byte(result), nil
		}

		// [triggers] exists but no crons line — append crons = [...]
		insertIdx := sectionEnd
		for insertIdx > sectionLoc[1] && (content[insertIdx-1] == '\n' || content[insertIdx-1] == '\r') {
			insertIdx--
		}
		newLine := fmt.Sprintf("\ncrons = [%q]\n", cron)
		trailingNewlines := content[insertIdx:sectionEnd]
		result := content[:insertIdx] + newLine + trailingNewlines + content[sectionEnd:]
		return []byte(result), nil
	}

	// No [triggers] section — create it
	newSection := fmt.Sprintf("\n[triggers]\ncrons = [%q]\n", cron)
	insertIdx := findTopLevelInsertPoint(content)
	result := content[:insertIdx] + newSection + content[insertIdx:]
	return []byte(result), nil
}

// addCronJSON appends a cron to the triggers.crons array in a JSON/JSONC config.
func addCronJSON(data []byte, cron string) ([]byte, error) {
	clean := jsonc.ToJSON(data)

	// Get existing crons array
	existing := gjson.GetBytes(clean, "triggers.crons")
	var crons []string
	if existing.Exists() && existing.IsArray() {
		existing.ForEach(func(_, v gjson.Result) bool {
			crons = append(crons, v.String())
			return true
		})
	}
	crons = append(crons, cron)

	result, err := sjson.SetBytes(clean, "triggers.crons", crons)
	if err != nil {
		return nil, fmt.Errorf("failed to update JSON config: %w", err)
	}

	var pretty bytes.Buffer
	if err := json.Indent(&pretty, result, "", "  "); err != nil {
		return result, nil
	}
	return append(pretty.Bytes(), '\n'), nil
}

// RemoveCron removes a cron expression from the top-level [triggers].crons array.
func RemoveCron(configPath, cron string) error {
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
		result, err = removeCronTOML(data, cron)
	case ".json", ".jsonc":
		result, err = removeCronJSON(data, cron)
	default:
		return fmt.Errorf("unsupported config format: %s", ext)
	}
	if err != nil {
		return err
	}

	return os.WriteFile(absPath, result, 0644)
}

// removeCronTOML removes a cron from the [triggers] section in a TOML config.
func removeCronTOML(data []byte, cron string) ([]byte, error) {
	content := string(data)

	sectionRe := regexp.MustCompile(`(?m)^\[triggers\]\s*$`)
	sectionLoc := sectionRe.FindStringIndex(content)
	if sectionLoc == nil {
		return nil, fmt.Errorf("cron %q not found in config (no [triggers] section)", cron)
	}

	afterSection := content[sectionLoc[1]:]
	nextHeaderRe := regexp.MustCompile(`(?m)^\[`)
	nextLoc := nextHeaderRe.FindStringIndex(afterSection)

	sectionEnd := len(content)
	if nextLoc != nil {
		sectionEnd = sectionLoc[1] + nextLoc[0]
	}

	sectionBody := content[sectionLoc[1]:sectionEnd]

	// Find crons = [...] line
	cronsRe := regexp.MustCompile(`(?m)^crons\s*=\s*\[([^\]]*)\]`)
	cronsLoc := cronsRe.FindStringSubmatchIndex(sectionBody)
	if cronsLoc == nil {
		return nil, fmt.Errorf("cron %q not found in config (no crons array)", cron)
	}

	arrayContents := sectionBody[cronsLoc[2]:cronsLoc[3]]

	// Parse the crons from the array contents
	var newCrons []string
	found := false
	// Split by comma and clean up
	for _, part := range strings.Split(arrayContents, ",") {
		trimmed := strings.TrimSpace(part)
		if trimmed == "" {
			continue
		}
		// Remove surrounding quotes
		unquoted := strings.Trim(trimmed, `"'`)
		if unquoted == cron && !found {
			found = true
			continue // skip this one
		}
		newCrons = append(newCrons, fmt.Sprintf("%q", unquoted))
	}

	if !found {
		return nil, fmt.Errorf("cron %q not found in config", cron)
	}

	if len(newCrons) == 0 {
		// Remove the entire [triggers] section if no crons remain
		// Include leading newline if present
		removeStart := sectionLoc[0]
		if removeStart > 0 && content[removeStart-1] == '\n' {
			removeStart--
		}
		result := content[:removeStart] + content[sectionEnd:]

		// Clean up multiple consecutive blank lines
		multiBlank := regexp.MustCompile(`\n{3,}`)
		result = multiBlank.ReplaceAllString(result, "\n\n")
		result = strings.TrimRight(result, "\n\t ") + "\n"
		return []byte(result), nil
	}

	newLine := fmt.Sprintf("crons = [%s]", strings.Join(newCrons, ", "))
	absStart := sectionLoc[1] + cronsLoc[0]
	absEnd := sectionLoc[1] + cronsLoc[1]
	result := content[:absStart] + newLine + content[absEnd:]
	return []byte(result), nil
}

// removeCronJSON removes a cron from the triggers.crons array in a JSON/JSONC config.
func removeCronJSON(data []byte, cron string) ([]byte, error) {
	clean := jsonc.ToJSON(data)

	existing := gjson.GetBytes(clean, "triggers.crons")
	if !existing.Exists() || !existing.IsArray() {
		return nil, fmt.Errorf("cron %q not found in config (no triggers.crons array)", cron)
	}

	// Find the index of the cron to remove
	idx := -1
	existing.ForEach(func(key, value gjson.Result) bool {
		if value.String() == cron {
			idx = int(key.Int())
			return false
		}
		return true
	})

	if idx < 0 {
		return nil, fmt.Errorf("cron %q not found in config", cron)
	}

	// If this is the last cron, remove the entire triggers object
	count := 0
	existing.ForEach(func(_, _ gjson.Result) bool {
		count++
		return true
	})

	var result []byte
	var err error
	if count <= 1 {
		result, err = sjson.DeleteBytes(clean, "triggers")
	} else {
		result, err = sjson.DeleteBytes(clean, fmt.Sprintf("triggers.crons.%d", idx))
	}
	if err != nil {
		return nil, fmt.Errorf("failed to update JSON config: %w", err)
	}

	var pretty bytes.Buffer
	if err := json.Indent(&pretty, result, "", "  "); err != nil {
		return result, nil
	}
	return append(pretty.Bytes(), '\n'), nil
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
