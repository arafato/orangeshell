package ai

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// skipDirs are directory names that should be skipped when scanning for source files.
var skipDirs = map[string]bool{
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
}

// sourceExts are file extensions to include when scanning for source files.
var sourceExts = map[string]bool{
	".ts":  true,
	".js":  true,
	".tsx": true,
	".jsx": true,
	".mts": true,
	".mjs": true,
}

// maxFileSize is the maximum size of a single file to include (50KB).
const maxFileSize = 50 * 1024

// maxTotalSize is the maximum total size of all files per project (100KB).
const maxTotalSize = 100 * 1024

// ProjectFileInfo describes a single source file discovered in a project.
type ProjectFileInfo struct {
	Path     string // absolute path
	RelPath  string // path relative to project root
	Size     int64  // file size in bytes
	Language string // e.g. "typescript", "javascript"
}

// ProjectFileSummary describes the source files available for a project.
type ProjectFileSummary struct {
	ProjectName string
	ProjectDir  string
	Files       []ProjectFileInfo
	TotalSize   int64 // total size of all files in bytes
}

// ScanProjectFiles scans the project directory for source files.
// It looks for source files in the directory containing the main entry point
// (typically src/), plus the wrangler config and worker-configuration.d.ts.
func ScanProjectFiles(projectDir string, mainEntry string) *ProjectFileSummary {
	summary := &ProjectFileSummary{
		ProjectDir: projectDir,
	}

	// 1. Include the wrangler config file itself
	for _, name := range []string{"wrangler.jsonc", "wrangler.json", "wrangler.toml"} {
		p := filepath.Join(projectDir, name)
		if info, err := os.Stat(p); err == nil && !info.IsDir() && info.Size() <= maxFileSize {
			summary.Files = append(summary.Files, ProjectFileInfo{
				Path:     p,
				RelPath:  name,
				Size:     info.Size(),
				Language: langFromExt(filepath.Ext(name)),
			})
			summary.TotalSize += info.Size()
			break // only one config file
		}
	}

	// 2. Include worker-configuration.d.ts if it exists (has Env type defs)
	wcPath := filepath.Join(projectDir, "worker-configuration.d.ts")
	if info, err := os.Stat(wcPath); err == nil && !info.IsDir() && info.Size() <= maxFileSize {
		summary.Files = append(summary.Files, ProjectFileInfo{
			Path:     wcPath,
			RelPath:  "worker-configuration.d.ts",
			Size:     info.Size(),
			Language: "typescript",
		})
		summary.TotalSize += info.Size()
	}

	// 3. Scan the source directory (the directory containing the main entry point)
	srcDir := projectDir
	if mainEntry != "" {
		mainDir := filepath.Dir(mainEntry)
		if mainDir != "" && mainDir != "." {
			srcDir = filepath.Join(projectDir, mainDir)
		}
	} else {
		// No main entry specified — try src/ before falling back to project root
		candidate := filepath.Join(projectDir, "src")
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			srcDir = candidate
		}
	}

	if info, err := os.Stat(srcDir); err == nil && info.IsDir() {
		scanDir(srcDir, projectDir, summary)
	}

	return summary
}

// scanDir recursively scans a directory for source files, adding them to the summary.
func scanDir(dir, projectDir string, summary *ProjectFileSummary) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}

	for _, entry := range entries {
		name := entry.Name()

		if entry.IsDir() {
			// Skip known non-source directories and hidden directories
			if skipDirs[name] || (len(name) > 0 && name[0] == '.') {
				continue
			}
			scanDir(filepath.Join(dir, name), projectDir, summary)
			continue
		}

		ext := filepath.Ext(name)
		if !sourceExts[ext] {
			continue
		}

		info, err := entry.Info()
		if err != nil || info.Size() > maxFileSize {
			continue
		}

		// Check total size budget
		if summary.TotalSize+info.Size() > maxTotalSize {
			continue
		}

		absPath := filepath.Join(dir, name)
		relPath, _ := filepath.Rel(projectDir, absPath)

		summary.Files = append(summary.Files, ProjectFileInfo{
			Path:     absPath,
			RelPath:  relPath,
			Size:     info.Size(),
			Language: langFromExt(ext),
		})
		summary.TotalSize += info.Size()
	}
}

// ReadProjectFiles reads the contents of all files in the summary.
// Returns a slice of FileContextData suitable for inclusion in the system prompt.
func ReadProjectFiles(summary *ProjectFileSummary) []FileContextData {
	if summary == nil || len(summary.Files) == 0 {
		return nil
	}

	var result []FileContextData
	for _, f := range summary.Files {
		content, err := os.ReadFile(f.Path)
		if err != nil {
			continue
		}
		result = append(result, FileContextData{
			Path:     f.Path,
			Language: f.Language,
			Content:  string(content),
		})
	}
	return result
}

// FileContextData holds the content of a single source file for prompt inclusion.
type FileContextData struct {
	Path     string // absolute file path
	Language string // programming language for code fence
	Content  string // file content
}

// FormatFileSummary returns a human-readable summary string for a project's source files.
// e.g. "src/ (5 files, ~12KB)"
func FormatFileSummary(summary *ProjectFileSummary) string {
	if summary == nil || len(summary.Files) == 0 {
		return "no source files"
	}
	sizeStr := formatSize(summary.TotalSize)
	return fmt.Sprintf("%d files, ~%s", len(summary.Files), sizeStr)
}

func formatSize(bytes int64) string {
	if bytes < 1024 {
		return fmt.Sprintf("%dB", bytes)
	}
	return fmt.Sprintf("%dKB", bytes/1024)
}

func langFromExt(ext string) string {
	switch strings.ToLower(ext) {
	case ".ts", ".mts":
		return "typescript"
	case ".tsx":
		return "tsx"
	case ".js", ".mjs":
		return "javascript"
	case ".jsx":
		return "jsx"
	case ".toml":
		return "toml"
	case ".json", ".jsonc":
		return "json"
	default:
		return ""
	}
}
