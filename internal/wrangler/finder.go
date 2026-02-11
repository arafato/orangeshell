package wrangler

import (
	"os"
	"path/filepath"
)

// configFileNames lists the wrangler config filenames in priority order.
// wrangler.jsonc is recommended for new projects (as of Wrangler v3.91.0+).
var configFileNames = []string{
	"wrangler.jsonc",
	"wrangler.json",
	"wrangler.toml",
}

// FindConfig scans a directory for a wrangler configuration file.
// Returns the absolute path to the first matching file, or empty string if none found.
func FindConfig(dir string) string {
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return ""
	}
	for _, name := range configFileNames {
		path := filepath.Join(absDir, name)
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			return path
		}
	}
	return ""
}

// FindConfigUp scans the given directory and then one level up for a wrangler config.
// This handles monorepo patterns where orangeshell might be run from a subdirectory.
func FindConfigUp(dir string) string {
	if path := FindConfig(dir); path != "" {
		return path
	}
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return ""
	}
	parent := filepath.Dir(absDir)
	if parent == absDir {
		return "" // already at root
	}
	return FindConfig(parent)
}
