package wrangler

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// ScanWorkflowClasses scans the project source files for exported Workflow class
// definitions and returns their class names. This enables the binding form to offer
// a picker of discovered classes instead of requiring manual input.
//
// Supported patterns:
//   - TypeScript/JavaScript: export class MyWorkflow extends WorkflowEntrypoint
//   - Python: class MyWorkflow(WorkflowEntrypoint):
//
// Known limitations (not detected):
//   - Re-exports: export { MyWorkflow } from './workflows'
//   - Barrel/index re-exports
//   - Renamed imports: import { WorkflowEntrypoint as WF } then extends WF
//   - Dynamic class names or factory patterns
//   - Python __all__ exports
func ScanWorkflowClasses(projectDir, mainEntry string) []string {
	srcDir := resolveSourceDir(projectDir, mainEntry)
	var classes []string
	scanDirForWorkflows(srcDir, projectDir, &classes)
	return classes
}

// resolveSourceDir determines the source directory to scan, replicating the
// same logic used by the AI tab's file scanner.
func resolveSourceDir(projectDir, mainEntry string) string {
	if mainEntry != "" {
		dir := filepath.Dir(mainEntry)
		if dir != "." && dir != "" {
			candidate := filepath.Join(projectDir, dir)
			if info, err := os.Stat(candidate); err == nil && info.IsDir() {
				return candidate
			}
		}
	}
	// Try src/ subdirectory
	candidate := filepath.Join(projectDir, "src")
	if info, err := os.Stat(candidate); err == nil && info.IsDir() {
		return candidate
	}
	return projectDir
}

// workflowSourceExts are file extensions that may contain Workflow class definitions.
var workflowSourceExts = map[string]bool{
	".ts":  true,
	".js":  true,
	".mts": true,
	".mjs": true,
	".tsx": true,
	".jsx": true,
	".py":  true,
}

// workflowSkipDirs are directories to skip during scanning.
var workflowSkipDirs = map[string]bool{
	"node_modules": true,
	".git":         true,
	"dist":         true,
	".wrangler":    true,
	"build":        true,
	"coverage":     true,
	".turbo":       true,
	".next":        true,
	".output":      true,
	"vendor":       true,
	"__pycache__":  true,
	".venv":        true,
	"venv":         true,
}

// Regex patterns for Workflow class detection.
var (
	// TypeScript/JavaScript: export class MyWorkflow extends WorkflowEntrypoint
	jsWorkflowRe = regexp.MustCompile(`export\s+class\s+(\w+)\s+extends\s+WorkflowEntrypoint`)
	// Python: class MyWorkflow(WorkflowEntrypoint):
	pyWorkflowRe = regexp.MustCompile(`class\s+(\w+)\s*\(\s*WorkflowEntrypoint\s*\)\s*:`)
)

const maxWorkflowScanFileSize = 100 * 1024 // 100KB per file

func scanDirForWorkflows(dir, projectDir string, classes *[]string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}

	for _, entry := range entries {
		name := entry.Name()

		if entry.IsDir() {
			// Skip hidden directories and known skip dirs
			if len(name) > 0 && name[0] == '.' {
				continue
			}
			if workflowSkipDirs[name] {
				continue
			}
			scanDirForWorkflows(filepath.Join(dir, name), projectDir, classes)
			continue
		}

		ext := strings.ToLower(filepath.Ext(name))
		if !workflowSourceExts[ext] {
			continue
		}

		info, err := entry.Info()
		if err != nil || info.Size() > maxWorkflowScanFileSize {
			continue
		}

		filePath := filepath.Join(dir, name)
		data, err := os.ReadFile(filePath)
		if err != nil {
			continue
		}

		content := string(data)

		var re *regexp.Regexp
		if ext == ".py" {
			re = pyWorkflowRe
		} else {
			re = jsWorkflowRe
		}

		matches := re.FindAllStringSubmatch(content, -1)
		for _, match := range matches {
			if len(match) >= 2 {
				*classes = append(*classes, match[1])
			}
		}
	}
}
