package version

import (
	"fmt"
	"runtime/debug"
)

// Injected at build time via -ldflags.
var (
	version = ""
	commit  = ""
	date    = ""
)

// GetVersion returns the version string (e.g. "v0.1.0").
func GetVersion() string {
	v, _, _ := getVersionInfo()
	return v
}

// GetShort returns a compact version string like "v0.1.0 (abc1234)".
func GetShort() string {
	v, c, _ := getVersionInfo()
	if c != "" && c != "unknown" {
		return fmt.Sprintf("%s (%s)", v, c)
	}
	return v
}

// GetFull returns a multi-line version string with all build metadata.
func GetFull() string {
	v, c, d := getVersionInfo()
	return fmt.Sprintf("orangeshell %s\nCommit: %s\nBuilt:  %s", v, c, d)
}

func getVersionInfo() (string, string, string) {
	// If injected at build time, use those values
	if version != "" && commit != "" && date != "" {
		return version, commit, date
	}

	// Fallback: extract from Go build info (works with `go install`)
	v, c, d := "dev", "unknown", "unknown"
	if info, ok := debug.ReadBuildInfo(); ok {
		if info.Main.Version != "" && info.Main.Version != "(devel)" {
			v = info.Main.Version
		}
		for _, s := range info.Settings {
			switch s.Key {
			case "vcs.revision":
				if len(s.Value) >= 7 {
					c = s.Value[:7]
				} else {
					c = s.Value
				}
			case "vcs.time":
				d = s.Value
			}
		}
	}
	return v, c, d
}
