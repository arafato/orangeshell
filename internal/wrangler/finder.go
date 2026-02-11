package wrangler

import (
	"os"
	"path/filepath"
	"sort"
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

// ProjectInfo holds discovery results for a single wrangler project.
type ProjectInfo struct {
	ConfigPath string // absolute path to wrangler config file
	Dir        string // absolute path to the project directory
}

// skipDirs are directory names that should never contain wrangler projects.
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

// maxDiscoverDepth limits how deep DiscoverProjects walks the tree.
const maxDiscoverDepth = 5

// DiscoverProjects walks the directory tree from root, finding all directories
// containing a wrangler config file. Skips known non-project dirs (node_modules,
// .git, dist, etc.) and limits depth to 5 levels. Returns results sorted by
// directory path.
func DiscoverProjects(root string) []ProjectInfo {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil
	}

	var projects []ProjectInfo
	discoverWalk(absRoot, absRoot, 0, &projects)

	sort.Slice(projects, func(i, j int) bool {
		return projects[i].Dir < projects[j].Dir
	})
	return projects
}

// discoverWalk recursively scans directories for wrangler configs.
func discoverWalk(dir, root string, depth int, projects *[]ProjectInfo) {
	if depth > maxDiscoverDepth {
		return
	}

	// Check if this directory has a wrangler config
	if configPath := FindConfig(dir); configPath != "" {
		*projects = append(*projects, ProjectInfo{
			ConfigPath: configPath,
			Dir:        dir,
		})
		// Don't recurse into a project directory — a project is a leaf
		return
	}

	// Read directory entries
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		if skipDirs[name] {
			continue
		}
		// Skip hidden directories (other than those in skipDirs which are already handled)
		if len(name) > 0 && name[0] == '.' {
			continue
		}
		discoverWalk(filepath.Join(dir, name), root, depth+1, projects)
	}
}
