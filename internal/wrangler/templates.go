package wrangler

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	templatesJSONURL = "https://raw.githubusercontent.com/cloudflare/templates/main/templates.json"
	packageJSONBase  = "https://raw.githubusercontent.com/cloudflare/templates/main/"
	templateTreeBase = "https://github.com/cloudflare/templates/tree/main/"

	// Maximum concurrent package.json fetches.
	maxConcurrentFetches = 10
)

// TemplateInfo describes a single Cloudflare template from the cloudflare/templates repo.
type TemplateInfo struct {
	Name        string // directory name, e.g. "vite-react-template"
	Label       string // display name from cloudflare.label, or prettified Name
	Description string // from package.json description field
	Published   bool   // from cloudflare.publish (production-ready)
	URL         string // e.g. https://github.com/cloudflare/templates/tree/main/vite-react-template
}

// templatesJSON is the shape of templates.json at the repo root.
type templatesJSON struct {
	Templates map[string]json.RawMessage `json:"templates"`
}

// packageJSON is the subset of a template's package.json we care about.
type packageJSON struct {
	Description string `json:"description"`
	Cloudflare  struct {
		Label   string `json:"label"`
		Publish *bool  `json:"publish"`
	} `json:"cloudflare"`
}

// FetchTemplates fetches the list of available Cloudflare templates.
// It first fetches templates.json to get all template names, then
// concurrently fetches each template's package.json for metadata.
func FetchTemplates(ctx context.Context) ([]TemplateInfo, error) {
	client := &http.Client{Timeout: 15 * time.Second}

	// Phase 1: Fetch templates.json to get all template names.
	names, err := fetchTemplateNames(ctx, client)
	if err != nil {
		return nil, fmt.Errorf("fetching template index: %w", err)
	}

	if len(names) == 0 {
		return nil, fmt.Errorf("no templates found in index")
	}

	// Phase 2: Fetch each template's package.json concurrently.
	templates := make([]TemplateInfo, len(names))
	var wg sync.WaitGroup
	sem := make(chan struct{}, maxConcurrentFetches)

	for i, name := range names {
		templates[i] = TemplateInfo{
			Name:  name,
			Label: prettifyName(name),
			URL:   templateTreeBase + name,
		}

		wg.Add(1)
		go func(idx int, tmplName string) {
			defer wg.Done()

			// Acquire semaphore slot, respecting context cancellation.
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				return
			}

			pkg, fetchErr := fetchPackageJSON(ctx, client, tmplName)
			if fetchErr != nil {
				// Graceful degradation: keep the template with just the name.
				return
			}

			if pkg.Description != "" {
				templates[idx].Description = pkg.Description
			}
			if pkg.Cloudflare.Label != "" {
				templates[idx].Label = pkg.Cloudflare.Label
			}
			if pkg.Cloudflare.Publish != nil && *pkg.Cloudflare.Publish {
				templates[idx].Published = true
			}
		}(i, name)
	}

	wg.Wait()

	// Sort: published first (alphabetically by label), then unpublished (alphabetically by label).
	sort.Slice(templates, func(i, j int) bool {
		if templates[i].Published != templates[j].Published {
			return templates[i].Published
		}
		return strings.ToLower(templates[i].Label) < strings.ToLower(templates[j].Label)
	})

	return templates, nil
}

// fetchTemplateNames fetches templates.json and returns sorted template directory names.
func fetchTemplateNames(ctx context.Context, client *http.Client) ([]string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, templatesJSONURL, nil)
	if err != nil {
		return nil, err
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("templates.json returned HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20)) // 1 MB cap
	if err != nil {
		return nil, err
	}

	var idx templatesJSON
	if err := json.Unmarshal(body, &idx); err != nil {
		return nil, fmt.Errorf("parsing templates.json: %w", err)
	}

	names := make([]string, 0, len(idx.Templates))
	for name := range idx.Templates {
		names = append(names, name)
	}
	sort.Strings(names)
	return names, nil
}

// fetchPackageJSON fetches and parses a single template's package.json.
func fetchPackageJSON(ctx context.Context, client *http.Client, name string) (packageJSON, error) {
	url := packageJSONBase + name + "/package.json"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return packageJSON{}, err
	}

	resp, err := client.Do(req)
	if err != nil {
		return packageJSON{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return packageJSON{}, fmt.Errorf("HTTP %d for %s", resp.StatusCode, url)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20)) // 1 MB cap
	if err != nil {
		return packageJSON{}, err
	}

	var pkg packageJSON
	if err := json.Unmarshal(body, &pkg); err != nil {
		return packageJSON{}, fmt.Errorf("parsing package.json for %s: %w", name, err)
	}

	return pkg, nil
}

// prettifyName converts "vite-react-template" to "Vite React Template".
func prettifyName(name string) string {
	name = strings.TrimSuffix(name, "-template")
	parts := strings.Split(name, "-")
	for i, p := range parts {
		if len(p) > 0 {
			parts[i] = strings.ToUpper(p[:1]) + p[1:]
		}
	}
	return strings.Join(parts, " ")
}
