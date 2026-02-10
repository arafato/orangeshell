package service

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	cloudflare "github.com/cloudflare/cloudflare-go/v6"
	"github.com/cloudflare/cloudflare-go/v6/d1"
)

// D1Service implements the Service interface for Cloudflare D1 SQL databases.
type D1Service struct {
	client    *cloudflare.Client
	accountID string

	// Cache for search and detail lookups
	mu        sync.Mutex
	cached    []Resource
	cachedRaw map[string]d1.DatabaseListResponse // keyed by database UUID
	cacheTime time.Time
}

// NewD1Service creates a D1 service.
func NewD1Service(client *cloudflare.Client, accountID string) *D1Service {
	return &D1Service{
		client:    client,
		accountID: accountID,
	}
}

func (s *D1Service) Name() string { return "D1" }

// List fetches all D1 databases from the Cloudflare API.
func (s *D1Service) List() ([]Resource, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	pager := s.client.D1.Database.ListAutoPaging(ctx, d1.DatabaseListParams{
		AccountID: cloudflare.F(s.accountID),
	})

	var resources []Resource
	rawMap := make(map[string]d1.DatabaseListResponse)
	for pager.Next() {
		db := pager.Current()

		summary := formatD1Summary(db)
		resources = append(resources, Resource{
			ID:          db.UUID,
			Name:        db.Name,
			ServiceType: "D1",
			ModifiedAt:  db.CreatedAt,
			Summary:     summary,
		})
		rawMap[db.UUID] = db
	}
	if err := pager.Err(); err != nil {
		return nil, fmt.Errorf("failed to list D1 databases: %w", err)
	}

	// Update cache
	s.mu.Lock()
	s.cached = resources
	s.cachedRaw = rawMap
	s.cacheTime = time.Now()
	s.mu.Unlock()

	return resources, nil
}

// Get fetches full details for a single D1 database by UUID.
func (s *D1Service) Get(id string) (*ResourceDetail, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	db, err := s.client.D1.Database.Get(ctx, id, d1.DatabaseGetParams{
		AccountID: cloudflare.F(s.accountID),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get D1 database %s: %w", id, err)
	}

	detail := &ResourceDetail{
		Resource: Resource{
			ID:          db.UUID,
			Name:        db.Name,
			ServiceType: "D1",
			ModifiedAt:  db.CreatedAt,
		},
	}

	detail.Fields = append(detail.Fields, DetailField{
		Label: "Database ID",
		Value: db.UUID,
	})
	detail.Fields = append(detail.Fields, DetailField{
		Label: "Name",
		Value: db.Name,
	})
	detail.Fields = append(detail.Fields, DetailField{
		Label: "Created",
		Value: db.CreatedAt.Format(time.RFC3339),
	})
	detail.Fields = append(detail.Fields, DetailField{
		Label: "Version",
		Value: db.Version,
	})
	detail.Fields = append(detail.Fields, DetailField{
		Label: "File Size",
		Value: formatFileSize(db.FileSize),
	})
	detail.Fields = append(detail.Fields, DetailField{
		Label: "Tables",
		Value: fmt.Sprintf("%.0f", db.NumTables),
	})
	detail.Fields = append(detail.Fields, DetailField{
		Label: "Read Replication",
		Value: string(db.ReadReplication.Mode),
	})

	// Query the database schema and render a diagram
	schema, schemaErr := s.querySchema(ctx, id)
	if schemaErr != nil {
		detail.ExtraContent = fmt.Sprintf("\n  ── Schema ─────────────────────\n\n  Could not load schema: %s", schemaErr)
	} else {
		detail.ExtraContent = renderSchema(schema)
	}

	return detail, nil
}

// SearchItems returns the cached list of D1 databases for fuzzy search.
func (s *D1Service) SearchItems() []Resource {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cached
}

func formatD1Summary(db d1.DatabaseListResponse) string {
	parts := []string{}

	if !db.CreatedAt.IsZero() {
		parts = append(parts, fmt.Sprintf("created %s", timeAgo(db.CreatedAt)))
	}

	if db.Version != "" {
		parts = append(parts, fmt.Sprintf("v%s", db.Version))
	}

	return joinParts(parts)
}

// formatFileSize converts bytes to a human-readable string.
func formatFileSize(bytes float64) string {
	switch {
	case bytes < 1024:
		return fmt.Sprintf("%.0f B", bytes)
	case bytes < 1024*1024:
		return fmt.Sprintf("%.1f KB", bytes/1024)
	case bytes < 1024*1024*1024:
		return fmt.Sprintf("%.1f MB", bytes/(1024*1024))
	default:
		return fmt.Sprintf("%.2f GB", bytes/(1024*1024*1024))
	}
}

// --- D1 Schema Introspection ---

// schemaTable represents a database table with its columns and foreign keys.
type schemaTable struct {
	Name    string
	Columns []schemaColumn
	FKs     []schemaFK
}

// schemaColumn represents a single column in a table.
type schemaColumn struct {
	Name    string
	Type    string
	NotNull bool
	PK      bool
}

// schemaFK represents a foreign key relationship.
type schemaFK struct {
	FromCol string
	ToTable string
	ToCol   string
}

// querySchema introspects a D1 database's schema by querying sqlite_master and PRAGMAs.
func (s *D1Service) querySchema(ctx context.Context, databaseID string) ([]schemaTable, error) {
	// Step 1: Get all user table names
	tableNames, err := s.queryD1(ctx, databaseID,
		"SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%' AND name NOT LIKE '_cf_%' ORDER BY name")
	if err != nil {
		return nil, fmt.Errorf("failed to query tables: %w", err)
	}

	if len(tableNames) == 0 {
		return nil, nil
	}

	var tables []schemaTable

	for _, row := range tableNames {
		tableName, _ := row["name"].(string)
		if tableName == "" {
			continue
		}

		table := schemaTable{Name: tableName}

		// Step 2: Get columns via PRAGMA table_info
		colRows, err := s.queryD1(ctx, databaseID,
			fmt.Sprintf("PRAGMA table_info('%s')", tableName))
		if err == nil {
			for _, cr := range colRows {
				col := schemaColumn{
					Name: strVal(cr, "name"),
					Type: strVal(cr, "type"),
				}
				// notnull: 0 or 1
				if numVal(cr, "notnull") == 1 {
					col.NotNull = true
				}
				// pk: 0 means not PK, >0 means PK (column index in composite PK)
				if numVal(cr, "pk") > 0 {
					col.PK = true
				}
				table.Columns = append(table.Columns, col)
			}
		}

		// Step 3: Get foreign keys via PRAGMA foreign_key_list
		fkRows, err := s.queryD1(ctx, databaseID,
			fmt.Sprintf("PRAGMA foreign_key_list('%s')", tableName))
		if err == nil {
			for _, fr := range fkRows {
				fk := schemaFK{
					FromCol: strVal(fr, "from"),
					ToTable: strVal(fr, "table"),
					ToCol:   strVal(fr, "to"),
				}
				if fk.FromCol != "" && fk.ToTable != "" {
					table.FKs = append(table.FKs, fk)
				}
			}
		}

		tables = append(tables, table)
	}

	return tables, nil
}

// queryD1 executes a single SQL query against a D1 database and returns rows as maps.
func (s *D1Service) queryD1(ctx context.Context, databaseID, sql string) ([]map[string]interface{}, error) {
	resp, err := s.client.D1.Database.Query(ctx, databaseID, d1.DatabaseQueryParams{
		AccountID: cloudflare.F(s.accountID),
		Body: d1.DatabaseQueryParamsBodyD1SingleQuery{
			Sql: cloudflare.F(sql),
		},
	})
	if err != nil {
		return nil, err
	}

	// The response is a paginated list of QueryResult; we expect one result set.
	results := resp.Result
	if len(results) == 0 {
		return nil, nil
	}

	// results[0].Results is []interface{}, each element is map[string]interface{}
	var rows []map[string]interface{}
	for _, r := range results[0].Results {
		if m, ok := r.(map[string]interface{}); ok {
			rows = append(rows, m)
		}
	}
	return rows, nil
}

// strVal safely extracts a string value from a row map.
func strVal(row map[string]interface{}, key string) string {
	v, ok := row[key]
	if !ok || v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprintf("%v", v)
}

// numVal safely extracts a numeric value from a row map.
func numVal(row map[string]interface{}, key string) float64 {
	v, ok := row[key]
	if !ok || v == nil {
		return 0
	}
	if f, ok := v.(float64); ok {
		return f
	}
	return 0
}

// renderSchema produces an ASCII tree diagram of the database schema.
func renderSchema(tables []schemaTable) string {
	if len(tables) == 0 {
		return "\n  ── Schema ─────────────────────\n\n  No tables found"
	}

	var b strings.Builder
	b.WriteString("\n  ── Schema ─────────────────────\n")

	// Build a lookup of FK relationships for the relations summary
	var allFKs []string

	for _, t := range tables {
		b.WriteString(fmt.Sprintf("\n  %s\n", t.Name))

		// Build FK lookup for this table: fromCol → "→ toTable.toCol"
		fkMap := make(map[string]string)
		for _, fk := range t.FKs {
			ref := fmt.Sprintf("→ %s.%s", fk.ToTable, fk.ToCol)
			fkMap[fk.FromCol] = ref
			allFKs = append(allFKs, fmt.Sprintf("  %s.%s → %s.%s", t.Name, fk.FromCol, fk.ToTable, fk.ToCol))
		}

		// Calculate max column name width for alignment
		maxNameLen := 0
		for _, c := range t.Columns {
			if len(c.Name) > maxNameLen {
				maxNameLen = len(c.Name)
			}
		}
		if maxNameLen < 4 {
			maxNameLen = 4
		}

		for i, c := range t.Columns {
			// Tree branch character
			branch := "├─"
			if i == len(t.Columns)-1 {
				branch = "└─"
			}

			// PK/FK tag
			tag := "  "
			if c.PK {
				tag = "PK"
			} else if _, isFK := fkMap[c.Name]; isFK {
				tag = "FK"
			}

			// Column type
			colType := c.Type
			if colType == "" {
				colType = "ANY"
			}

			// NOT NULL annotation
			notNull := ""
			if c.NotNull && !c.PK {
				notNull = " NOT NULL"
			}

			// FK reference
			fkRef := ""
			if ref, ok := fkMap[c.Name]; ok {
				fkRef = "  " + ref
			}

			line := fmt.Sprintf("  %s %s %-*s  %-8s%s%s",
				branch, tag, maxNameLen, c.Name, colType, notNull, fkRef)
			b.WriteString(line + "\n")
		}
	}

	// Relations summary
	if len(allFKs) > 0 {
		b.WriteString("\n  Relations\n")
		for _, fk := range allFKs {
			b.WriteString(fmt.Sprintf("  %s\n", fk))
		}
	}

	return b.String()
}
