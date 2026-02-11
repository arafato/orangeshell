package wrangler

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/tidwall/jsonc"
)

// WranglerConfig represents a parsed wrangler configuration file.
type WranglerConfig struct {
	Path         string                  // absolute path to config file
	Format       string                  // "toml", "json", or "jsonc"
	Name         string                  // worker name (top-level)
	Main         string                  // entry point
	CompatDate   string                  // compatibility_date
	CompatFlags  []string                // compatibility_flags
	Routes       []RouteConfig           // routes (top-level)
	Bindings     []Binding               // all bindings (top-level)
	Vars         map[string]string       // environment variables (top-level, names only for display)
	Environments map[string]*Environment // named environments
}

// Environment represents a named environment override in the config.
type Environment struct {
	Name        string            // override worker name (may be empty → inherited)
	CompatDate  string            // override compatibility_date
	CompatFlags []string          // override compatibility_flags
	Routes      []RouteConfig     // environment-specific routes
	Bindings    []Binding         // environment-specific bindings (non-inheritable)
	Vars        map[string]string // environment-specific vars (non-inheritable)
}

// RouteConfig holds a route pattern and optional zone.
type RouteConfig struct {
	Pattern  string
	ZoneName string
}

// Binding represents a normalized resource binding.
type Binding struct {
	Name       string // JS binding name (e.g. "MY_KV")
	Type       string // normalized type: kv_namespace, r2_bucket, d1, service, etc.
	ResourceID string // the identifying value (namespace_id, bucket_name, database_id, etc.)
}

// NavService returns the dashboard service name for cross-linking, or empty if not navigable.
func (b Binding) NavService() string {
	switch b.Type {
	case "kv_namespace":
		return "KV"
	case "r2_bucket":
		return "R2"
	case "d1":
		return "D1"
	case "service":
		return "Workers"
	default:
		return ""
	}
}

// TypeLabel returns a short human-readable label for the binding type.
func (b Binding) TypeLabel() string {
	switch b.Type {
	case "kv_namespace":
		return "KV"
	case "r2_bucket":
		return "R2"
	case "d1":
		return "D1"
	case "service":
		return "Service"
	case "durable_object_namespace":
		return "DO"
	case "queue_producer":
		return "Queue (producer)"
	case "queue_consumer":
		return "Queue (consumer)"
	case "ai":
		return "AI"
	case "vectorize":
		return "Vectorize"
	case "hyperdrive":
		return "Hyperdrive"
	case "analytics_engine":
		return "Analytics"
	case "secret_text":
		return "Secret"
	case "plain_text":
		return "Var"
	default:
		return b.Type
	}
}

// Parse reads and parses a wrangler configuration file (TOML or JSON/JSONC).
func Parse(path string) (*WranglerConfig, error) {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("invalid path: %w", err)
	}

	data, err := os.ReadFile(absPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read config: %w", err)
	}

	ext := strings.ToLower(filepath.Ext(absPath))
	switch ext {
	case ".toml":
		cfg, err := parseTOML(data)
		if err != nil {
			return nil, err
		}
		cfg.Path = absPath
		cfg.Format = "toml"
		return cfg, nil
	case ".json", ".jsonc":
		cfg, err := parseJSON(data)
		if err != nil {
			return nil, err
		}
		cfg.Path = absPath
		cfg.Format = ext[1:] // "json" or "jsonc"
		return cfg, nil
	default:
		return nil, fmt.Errorf("unsupported config format: %s", ext)
	}
}

// --- Internal raw types for deserialization ---

// rawConfig is the intermediate representation that both TOML and JSON parse into.
type rawConfig struct {
	Name            string            `toml:"name" json:"name"`
	Main            string            `toml:"main" json:"main"`
	CompatDate      string            `toml:"compatibility_date" json:"compatibility_date"`
	CompatFlags     []string          `toml:"compatibility_flags" json:"compatibility_flags"`
	Route           *rawRoute         `toml:"route" json:"route"`
	Routes          []rawRoute        `toml:"routes" json:"routes"`
	WorkersDev      *bool             `toml:"workers_dev" json:"workers_dev"`
	Vars            map[string]any    `toml:"vars" json:"vars"`
	KVNamespaces    []rawKV           `toml:"kv_namespaces" json:"kv_namespaces"`
	R2Buckets       []rawR2           `toml:"r2_buckets" json:"r2_buckets"`
	D1Databases     []rawD1           `toml:"d1_databases" json:"d1_databases"`
	Services        []rawService      `toml:"services" json:"services"`
	DurableObjects  *rawDO            `toml:"durable_objects" json:"durable_objects"`
	Queues          *rawQueues        `toml:"queues" json:"queues"`
	AI              *rawAI            `toml:"ai" json:"ai"`
	Vectorize       []rawVectorize    `toml:"vectorize" json:"vectorize"`
	Hyperdrive      []rawHyperdrive   `toml:"hyperdrive" json:"hyperdrive"`
	AnalyticsEngine []rawAnalytics    `toml:"analytics_engine_datasets" json:"analytics_engine_datasets"`
	Env             map[string]rawEnv `toml:"env" json:"env"`
}

type rawEnv struct {
	Name            string          `toml:"name" json:"name"`
	CompatDate      string          `toml:"compatibility_date" json:"compatibility_date"`
	CompatFlags     []string        `toml:"compatibility_flags" json:"compatibility_flags"`
	Route           *rawRoute       `toml:"route" json:"route"`
	Routes          []rawRoute      `toml:"routes" json:"routes"`
	Vars            map[string]any  `toml:"vars" json:"vars"`
	KVNamespaces    []rawKV         `toml:"kv_namespaces" json:"kv_namespaces"`
	R2Buckets       []rawR2         `toml:"r2_buckets" json:"r2_buckets"`
	D1Databases     []rawD1         `toml:"d1_databases" json:"d1_databases"`
	Services        []rawService    `toml:"services" json:"services"`
	DurableObjects  *rawDO          `toml:"durable_objects" json:"durable_objects"`
	Queues          *rawQueues      `toml:"queues" json:"queues"`
	AI              *rawAI          `toml:"ai" json:"ai"`
	Vectorize       []rawVectorize  `toml:"vectorize" json:"vectorize"`
	Hyperdrive      []rawHyperdrive `toml:"hyperdrive" json:"hyperdrive"`
	AnalyticsEngine []rawAnalytics  `toml:"analytics_engine_datasets" json:"analytics_engine_datasets"`
}

type rawRoute struct {
	Pattern  string `toml:"pattern" json:"pattern"`
	ZoneName string `toml:"zone_name" json:"zone_name"`
}

type rawKV struct {
	Binding string `toml:"binding" json:"binding"`
	ID      string `toml:"id" json:"id"`
}

type rawR2 struct {
	Binding    string `toml:"binding" json:"binding"`
	BucketName string `toml:"bucket_name" json:"bucket_name"`
}

type rawD1 struct {
	Binding    string `toml:"binding" json:"binding"`
	DatabaseID string `toml:"database_id" json:"database_id"`
	Name       string `toml:"database_name" json:"database_name"`
}

type rawService struct {
	Binding string `toml:"binding" json:"binding"`
	Service string `toml:"service" json:"service"`
}

type rawDO struct {
	Bindings []rawDOBinding `toml:"bindings" json:"bindings"`
}

type rawDOBinding struct {
	Name      string `toml:"name" json:"name"`
	ClassName string `toml:"class_name" json:"class_name"`
}

type rawQueues struct {
	Producers []rawQueueProducer `toml:"producers" json:"producers"`
	Consumers []rawQueueConsumer `toml:"consumers" json:"consumers"`
}

type rawQueueProducer struct {
	Binding string `toml:"binding" json:"binding"`
	Queue   string `toml:"queue" json:"queue"`
}

type rawQueueConsumer struct {
	Queue string `toml:"queue" json:"queue"`
}

type rawAI struct {
	Binding string `toml:"binding" json:"binding"`
}

type rawVectorize struct {
	Binding   string `toml:"binding" json:"binding"`
	IndexName string `toml:"index_name" json:"index_name"`
}

type rawHyperdrive struct {
	Binding string `toml:"binding" json:"binding"`
	ID      string `toml:"id" json:"id"`
}

type rawAnalytics struct {
	Binding string `toml:"binding" json:"binding"`
	Dataset string `toml:"dataset" json:"dataset"`
}

// --- Parsers ---

func parseTOML(data []byte) (*WranglerConfig, error) {
	var raw rawConfig
	if err := toml.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("failed to parse TOML: %w", err)
	}
	return normalizeRaw(&raw), nil
}

func parseJSON(data []byte) (*WranglerConfig, error) {
	// Strip JSONC comments before parsing
	clean := jsonc.ToJSON(data)
	var raw rawConfig
	if err := json.Unmarshal(clean, &raw); err != nil {
		return nil, fmt.Errorf("failed to parse JSON: %w", err)
	}
	return normalizeRaw(&raw), nil
}

// normalizeRaw converts the raw deserialized config into the clean WranglerConfig.
func normalizeRaw(raw *rawConfig) *WranglerConfig {
	cfg := &WranglerConfig{
		Name:         raw.Name,
		Main:         raw.Main,
		CompatDate:   raw.CompatDate,
		CompatFlags:  raw.CompatFlags,
		Routes:       normalizeRoutes(raw.Route, raw.Routes),
		Bindings:     extractBindings(raw),
		Vars:         normalizeVars(raw.Vars),
		Environments: make(map[string]*Environment),
	}

	for envName, rawEnv := range raw.Env {
		env := &Environment{
			Name:        rawEnv.Name,
			CompatDate:  rawEnv.CompatDate,
			CompatFlags: rawEnv.CompatFlags,
			Routes:      normalizeRoutes(rawEnv.Route, rawEnv.Routes),
			Bindings:    extractEnvBindings(&rawEnv),
			Vars:        normalizeVars(rawEnv.Vars),
		}
		cfg.Environments[envName] = env
	}

	return cfg
}

func normalizeRoutes(single *rawRoute, multi []rawRoute) []RouteConfig {
	var routes []RouteConfig
	if single != nil && single.Pattern != "" {
		routes = append(routes, RouteConfig{Pattern: single.Pattern, ZoneName: single.ZoneName})
	}
	for _, r := range multi {
		if r.Pattern != "" {
			routes = append(routes, RouteConfig{Pattern: r.Pattern, ZoneName: r.ZoneName})
		}
	}
	return routes
}

func normalizeVars(vars map[string]any) map[string]string {
	if len(vars) == 0 {
		return nil
	}
	result := make(map[string]string, len(vars))
	for k, v := range vars {
		result[k] = fmt.Sprintf("%v", v)
	}
	return result
}

// extractBindings collects all bindings from the top-level config.
func extractBindings(raw *rawConfig) []Binding {
	return collectBindings(
		raw.KVNamespaces, raw.R2Buckets, raw.D1Databases, raw.Services,
		raw.DurableObjects, raw.Queues, raw.AI,
		raw.Vectorize, raw.Hyperdrive, raw.AnalyticsEngine,
	)
}

// extractEnvBindings collects all bindings from an environment config.
func extractEnvBindings(raw *rawEnv) []Binding {
	return collectBindings(
		raw.KVNamespaces, raw.R2Buckets, raw.D1Databases, raw.Services,
		raw.DurableObjects, raw.Queues, raw.AI,
		raw.Vectorize, raw.Hyperdrive, raw.AnalyticsEngine,
	)
}

func collectBindings(
	kv []rawKV, r2 []rawR2, d1 []rawD1, svcs []rawService,
	do *rawDO, queues *rawQueues, ai *rawAI,
	vectorize []rawVectorize, hyperdrive []rawHyperdrive, analytics []rawAnalytics,
) []Binding {
	var bindings []Binding

	for _, b := range kv {
		bindings = append(bindings, Binding{Name: b.Binding, Type: "kv_namespace", ResourceID: b.ID})
	}
	for _, b := range r2 {
		bindings = append(bindings, Binding{Name: b.Binding, Type: "r2_bucket", ResourceID: b.BucketName})
	}
	for _, b := range d1 {
		id := b.DatabaseID
		if id == "" {
			id = b.Name
		}
		bindings = append(bindings, Binding{Name: b.Binding, Type: "d1", ResourceID: id})
	}
	for _, b := range svcs {
		bindings = append(bindings, Binding{Name: b.Binding, Type: "service", ResourceID: b.Service})
	}
	if do != nil {
		for _, b := range do.Bindings {
			bindings = append(bindings, Binding{Name: b.Name, Type: "durable_object_namespace", ResourceID: b.ClassName})
		}
	}
	if queues != nil {
		for _, b := range queues.Producers {
			bindings = append(bindings, Binding{Name: b.Binding, Type: "queue_producer", ResourceID: b.Queue})
		}
		for _, b := range queues.Consumers {
			bindings = append(bindings, Binding{Name: "consumer", Type: "queue_consumer", ResourceID: b.Queue})
		}
	}
	if ai != nil && ai.Binding != "" {
		bindings = append(bindings, Binding{Name: ai.Binding, Type: "ai", ResourceID: "Workers AI"})
	}
	for _, b := range vectorize {
		bindings = append(bindings, Binding{Name: b.Binding, Type: "vectorize", ResourceID: b.IndexName})
	}
	for _, b := range hyperdrive {
		bindings = append(bindings, Binding{Name: b.Binding, Type: "hyperdrive", ResourceID: b.ID})
	}
	for _, b := range analytics {
		bindings = append(bindings, Binding{Name: b.Binding, Type: "analytics_engine", ResourceID: b.Dataset})
	}

	return bindings
}

// ResolvedEnvName returns the effective worker name for an environment.
// For the default/top-level environment, returns the top-level name.
// For named environments, returns the environment's explicit name override if set,
// otherwise follows wrangler's convention: "<top-level-name>-<env-name>".
func (c *WranglerConfig) ResolvedEnvName(envName string) string {
	if envName == "" || envName == "default" {
		return c.Name
	}
	if env, ok := c.Environments[envName]; ok && env.Name != "" {
		return env.Name
	}
	// Wrangler convention: named envs without an explicit name get "<name>-<env>"
	if c.Name != "" {
		return c.Name + "-" + envName
	}
	return envName
}

// ResolvedCompatDate returns the effective compatibility date for an environment.
func (c *WranglerConfig) ResolvedCompatDate(envName string) string {
	if envName == "" {
		return c.CompatDate
	}
	if env, ok := c.Environments[envName]; ok && env.CompatDate != "" {
		return env.CompatDate
	}
	return c.CompatDate
}

// EnvNames returns all environment names in sorted order, with "default" first.
func (c *WranglerConfig) EnvNames() []string {
	names := []string{"default"}
	for name := range c.Environments {
		names = append(names, name)
	}
	return names
}

// EnvBindings returns the bindings for an environment. For "default", returns top-level bindings.
func (c *WranglerConfig) EnvBindings(envName string) []Binding {
	if envName == "" || envName == "default" {
		return c.Bindings
	}
	if env, ok := c.Environments[envName]; ok {
		return env.Bindings
	}
	return nil
}

// EnvRoutes returns the routes for an environment. For "default", returns top-level routes.
func (c *WranglerConfig) EnvRoutes(envName string) []RouteConfig {
	if envName == "" || envName == "default" {
		return c.Routes
	}
	if env, ok := c.Environments[envName]; ok {
		return env.Routes
	}
	return nil
}

// EnvVars returns the vars for an environment. For "default", returns top-level vars.
func (c *WranglerConfig) EnvVars(envName string) map[string]string {
	if envName == "" || envName == "default" {
		return c.Vars
	}
	if env, ok := c.Environments[envName]; ok {
		return env.Vars
	}
	return nil
}
