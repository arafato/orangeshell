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

// D1QueryResult holds the result of executing a SQL query against a D1 database.
type D1QueryResult struct {
	Output    string // formatted ASCII table or "Query OK" for mutations
	Meta      string // "Rows: 2 | Duration: 0.5ms"
	ChangedDB bool   // true if the query mutated the DB (triggers schema refresh)
}

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

// Get fetches metadata for a single D1 database by UUID.
// Schema is loaded separately via QuerySchemaRendered (lazy/async).
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

	// Compact metadata — these will be rendered as 2 rows in the D1 detail view
	detail.Fields = append(detail.Fields,
		DetailField{Label: "Database ID", Value: db.UUID},
		DetailField{Label: "Name", Value: db.Name},
		DetailField{Label: "Created", Value: db.CreatedAt.Format(time.RFC3339)},
		DetailField{Label: "Version", Value: db.Version},
		DetailField{Label: "File Size", Value: formatFileSize(db.FileSize)},
		DetailField{Label: "Tables", Value: fmt.Sprintf("%.0f", db.NumTables)},
		DetailField{Label: "Replication", Value: string(db.ReadReplication.Mode)},
	)

	return detail, nil
}

// SearchItems returns the cached list of D1 databases for fuzzy search.
func (s *D1Service) SearchItems() []Resource {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cached
}

// ExecuteQuery runs a SQL query against a D1 database and returns formatted results.
func (s *D1Service) ExecuteQuery(id, sql string) (*D1QueryResult, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	resp, err := s.client.D1.Database.Raw(ctx, id, d1.DatabaseRawParams{
		AccountID: cloudflare.F(s.accountID),
		Body: d1.DatabaseRawParamsBodyD1SingleQuery{
			Sql: cloudflare.F(sql),
		},
	})
	if err != nil {
		return nil, err
	}

	results := resp.Result
	if len(results) == 0 {
		return &D1QueryResult{Output: "No results", Meta: ""}, nil
	}

	r := results[0]
	meta := formatQueryMeta(r.Meta)

	// If the query didn't return columns, it was a mutation (CREATE, INSERT, etc.)
	if len(r.Results.Columns) == 0 {
		output := "Query OK"
		if r.Meta.Changes > 0 {
			output = fmt.Sprintf("Query OK, %.0f row(s) affected", r.Meta.Changes)
		}
		return &D1QueryResult{
			Output:    output,
			Meta:      meta,
			ChangedDB: r.Meta.ChangedDB,
		}, nil
	}

	// Format as ASCII table
	output := formatASCIITable(r.Results.Columns, r.Results.Rows)

	return &D1QueryResult{
		Output:    output,
		Meta:      meta,
		ChangedDB: r.Meta.ChangedDB,
	}, nil
}

// QuerySchema introspects the database schema and returns structured table data.
func (s *D1Service) QuerySchema(id string) ([]SchemaTable, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	return s.querySchema(ctx, id)
}

// --- Query helpers ---

func formatQueryMeta(meta d1.DatabaseRawResponseMeta) string {
	parts := []string{}
	if meta.RowsRead > 0 {
		parts = append(parts, fmt.Sprintf("Read: %.0f", meta.RowsRead))
	}
	if meta.RowsWritten > 0 {
		parts = append(parts, fmt.Sprintf("Written: %.0f", meta.RowsWritten))
	}
	if meta.Duration > 0 {
		parts = append(parts, fmt.Sprintf("%.1fms", meta.Duration))
	}
	if meta.Changes > 0 {
		parts = append(parts, fmt.Sprintf("Changes: %.0f", meta.Changes))
	}
	return strings.Join(parts, "  ")
}

// formatASCIITable renders columns and rows as an aligned ASCII table.
func formatASCIITable(columns []string, rows [][]interface{}) string {
	if len(columns) == 0 {
		return "(empty result)"
	}

	// Convert all values to strings and calculate column widths
	colWidths := make([]int, len(columns))
	for i, c := range columns {
		colWidths[i] = len(c)
	}

	strRows := make([][]string, len(rows))
	for i, row := range rows {
		strRow := make([]string, len(columns))
		for j := 0; j < len(columns); j++ {
			var val string
			if j < len(row) {
				val = cellToString(row[j])
			}
			strRow[j] = val
			if len(val) > colWidths[j] {
				colWidths[j] = len(val)
			}
		}
		strRows[i] = strRow
	}

	// Cap column widths to prevent excessively wide tables
	maxColWidth := 30
	for i := range colWidths {
		if colWidths[i] > maxColWidth {
			colWidths[i] = maxColWidth
		}
	}

	var b strings.Builder

	// Header
	b.WriteString(renderTableRow(columns, colWidths))
	b.WriteString("\n")

	// Separator
	sepParts := make([]string, len(columns))
	for i, w := range colWidths {
		sepParts[i] = strings.Repeat("─", w)
	}
	b.WriteString("├─")
	b.WriteString(strings.Join(sepParts, "─┼─"))
	b.WriteString("─┤\n")

	// Data rows
	for _, row := range strRows {
		b.WriteString(renderTableRow(row, colWidths))
		b.WriteString("\n")
	}

	// Row count
	b.WriteString(fmt.Sprintf("(%d row%s)", len(rows), pluralS(len(rows))))

	return b.String()
}

func renderTableRow(values []string, widths []int) string {
	parts := make([]string, len(values))
	for i, v := range values {
		w := widths[i]
		if len(v) > w {
			v = v[:w-1] + "…"
		}
		parts[i] = fmt.Sprintf("%-*s", w, v)
	}
	return "│ " + strings.Join(parts, " │ ") + " │"
}

func cellToString(v interface{}) string {
	if v == nil {
		return "NULL"
	}
	switch val := v.(type) {
	case string:
		return val
	case float64:
		if val == float64(int64(val)) {
			return fmt.Sprintf("%.0f", val)
		}
		return fmt.Sprintf("%g", val)
	case bool:
		if val {
			return "true"
		}
		return "false"
	default:
		return fmt.Sprintf("%v", val)
	}
}

func pluralS(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
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

// SchemaTable represents a database table with its columns and foreign keys.
type SchemaTable struct {
	Name    string
	Columns []SchemaColumn
	FKs     []SchemaFK
}

// SchemaColumn represents a single column in a table.
type SchemaColumn struct {
	Name    string
	Type    string
	NotNull bool
	PK      bool
}

// SchemaFK represents a foreign key relationship.
type SchemaFK struct {
	FromCol string
	ToTable string
	ToCol   string
}

// querySchema introspects a D1 database's schema by querying sqlite_master and PRAGMAs.
func (s *D1Service) querySchema(ctx context.Context, databaseID string) ([]SchemaTable, error) {
	// Step 1: Get all user table names
	tableNames, err := s.queryD1(ctx, databaseID,
		"SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%' AND name NOT LIKE '_cf_%' ORDER BY name")
	if err != nil {
		return nil, fmt.Errorf("failed to query tables: %w", err)
	}

	if len(tableNames) == 0 {
		return nil, nil
	}

	var tables []SchemaTable

	for _, row := range tableNames {
		tableName, _ := row["name"].(string)
		if tableName == "" {
			continue
		}

		table := SchemaTable{Name: tableName}

		// Step 2: Get columns via PRAGMA table_info
		colRows, err := s.queryD1(ctx, databaseID,
			fmt.Sprintf("PRAGMA table_info('%s')", tableName))
		if err == nil {
			for _, cr := range colRows {
				col := SchemaColumn{
					Name: strVal(cr, "name"),
					Type: strVal(cr, "type"),
				}
				if numVal(cr, "notnull") == 1 {
					col.NotNull = true
				}
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
				fk := SchemaFK{
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

	results := resp.Result
	if len(results) == 0 {
		return nil, nil
	}

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
func renderSchema(tables []SchemaTable) string {
	if len(tables) == 0 {
		return "No tables found"
	}

	var b strings.Builder

	// Build a lookup of FK relationships for the relations summary
	var allFKs []string

	for _, t := range tables {
		b.WriteString(fmt.Sprintf("%s\n", t.Name))

		// Build FK lookup for this table
		fkMap := make(map[string]string)
		for _, fk := range t.FKs {
			ref := fmt.Sprintf("-> %s.%s", fk.ToTable, fk.ToCol)
			fkMap[fk.FromCol] = ref
			allFKs = append(allFKs, fmt.Sprintf("  %s.%s -> %s.%s", t.Name, fk.FromCol, fk.ToTable, fk.ToCol))
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
			branch := "├─"
			if i == len(t.Columns)-1 {
				branch = "└─"
			}

			tag := "  "
			if c.PK {
				tag = "PK"
			} else if _, isFK := fkMap[c.Name]; isFK {
				tag = "FK"
			}

			colType := c.Type
			if colType == "" {
				colType = "ANY"
			}

			notNull := ""
			if c.NotNull && !c.PK {
				notNull = " NOT NULL"
			}

			fkRef := ""
			if ref, ok := fkMap[c.Name]; ok {
				fkRef = "  " + ref
			}

			line := fmt.Sprintf("%s %s %-*s  %-8s%s%s",
				branch, tag, maxNameLen, c.Name, colType, notNull, fkRef)
			b.WriteString(line + "\n")
		}
		b.WriteString("\n")
	}

	// Relations summary
	if len(allFKs) > 0 {
		b.WriteString("Relations\n")
		for _, fk := range allFKs {
			b.WriteString(fk + "\n")
		}
	}

	return strings.TrimRight(b.String(), "\n")
}
