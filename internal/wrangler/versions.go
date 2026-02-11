package wrangler

import (
	"encoding/json"
	"fmt"
	"time"
)

// Version represents a single Worker version from `wrangler versions list --json`.
type Version struct {
	ID          string    // Version UUID
	Number      int       // Sequential version number
	CreatedOn   time.Time // When this version was created
	Source      string    // e.g. "wrangler"
	AuthorEmail string    // Who created this version
	TriggeredBy string    // e.g. "upload", "version_upload"
}

// ShortID returns the first 8 characters of the version ID.
func (v Version) ShortID() string {
	if len(v.ID) >= 8 {
		return v.ID[:8]
	}
	return v.ID
}

// RelativeTime returns a human-readable relative time string.
func (v Version) RelativeTime() string {
	d := time.Since(v.CreatedOn)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		m := int(d.Minutes())
		if m == 1 {
			return "1 min ago"
		}
		return fmt.Sprintf("%d mins ago", m)
	case d < 24*time.Hour:
		h := int(d.Hours())
		if h == 1 {
			return "1 hour ago"
		}
		return fmt.Sprintf("%d hours ago", h)
	default:
		days := int(d.Hours() / 24)
		if days == 1 {
			return "1 day ago"
		}
		return fmt.Sprintf("%d days ago", days)
	}
}

// rawVersion matches the JSON shape from `wrangler versions list --json`.
type rawVersion struct {
	ID       string `json:"id"`
	Number   int    `json:"number"`
	Metadata struct {
		CreatedOn   string `json:"created_on"`
		Source      string `json:"source"`
		AuthorID    string `json:"author_id"`
		AuthorEmail string `json:"author_email"`
		HasPreview  bool   `json:"has_preview"`
	} `json:"metadata"`
	Annotations map[string]string `json:"annotations"`
}

// ParseVersionsJSON parses the JSON output of `wrangler versions list --json`.
// Returns versions sorted by the order they appear in the JSON (newest first).
func ParseVersionsJSON(data []byte) ([]Version, error) {
	var raw []rawVersion
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("failed to parse versions JSON: %w", err)
	}

	versions := make([]Version, 0, len(raw))
	for _, r := range raw {
		createdOn, err := time.Parse(time.RFC3339Nano, r.Metadata.CreatedOn)
		if err != nil {
			// Try RFC3339 as fallback
			createdOn, err = time.Parse(time.RFC3339, r.Metadata.CreatedOn)
			if err != nil {
				createdOn = time.Time{}
			}
		}

		triggeredBy := r.Annotations["workers/triggered_by"]

		versions = append(versions, Version{
			ID:          r.ID,
			Number:      r.Number,
			CreatedOn:   createdOn,
			Source:      r.Metadata.Source,
			AuthorEmail: r.Metadata.AuthorEmail,
			TriggeredBy: triggeredBy,
		})
	}

	return versions, nil
}
