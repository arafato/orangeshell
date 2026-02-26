package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// AnalyticsClient makes GraphQL requests to the Cloudflare Analytics API.
// Uses the same auth pattern as BuildsClient (raw HTTP, not the SDK).
type AnalyticsClient struct {
	accountID string
	authEmail string // for X-Auth-Email + X-Auth-Key auth
	authKey   string
	authToken string // for Bearer token auth
	http      *http.Client
}

// NewAnalyticsClient creates an AnalyticsClient from the given credentials.
func NewAnalyticsClient(accountID, authEmail, authKey, authToken string) *AnalyticsClient {
	return &AnalyticsClient{
		accountID: accountID,
		authEmail: authEmail,
		authKey:   authKey,
		authToken: authToken,
		http:      &http.Client{Timeout: 30 * time.Second},
	}
}

// TimeRange defines a time window for analytics queries.
type TimeRange struct {
	Label    string        // Display label (e.g. "1h", "6h", "24h", "7d", "30d")
	Duration time.Duration // How far back to query
	GroupBy  string        // GraphQL groupBy field (e.g. "datetimeMinute", "datetimeHour")
}

// Predefined time ranges for the analytics dashboard.
var TimeRanges = []TimeRange{
	{Label: "1h", Duration: 1 * time.Hour, GroupBy: "datetimeMinute"},
	{Label: "6h", Duration: 6 * time.Hour, GroupBy: "datetimeFiveMinutes"},
	{Label: "24h", Duration: 24 * time.Hour, GroupBy: "datetimeFifteenMinutes"},
	{Label: "7d", Duration: 7 * 24 * time.Hour, GroupBy: "datetimeHour"},
	{Label: "30d", Duration: 30 * 24 * time.Hour, GroupBy: "datetimeHour"},
}

// WorkerMetrics holds the aggregated analytics data for a single worker.
type WorkerMetrics struct {
	ScriptName string
	TimeRange  TimeRange

	// Totals
	TotalRequests    int64
	TotalErrors      int64
	TotalSubrequests int64

	// CPU time quantiles (microseconds)
	CPUTimeP50 float64
	CPUTimeP99 float64

	// Time-series buckets (sorted chronologically)
	Buckets []MetricsBucket

	// Status breakdown (aggregated)
	StatusCounts map[string]int64 // e.g. "success" → 1234, "scriptThrewException" → 5

	// Errors (last N exceptions from the status breakdown — derived from buckets)
	Errors []MetricsError
}

// MetricsBucket holds metrics for a single time bucket.
type MetricsBucket struct {
	Datetime    time.Time
	Requests    int64
	Errors      int64
	Subrequests int64
	CPUTimeP50  float64
	CPUTimeP99  float64
	Status      string // dimension value: success, scriptThrewException, etc.
}

// MetricsError represents a single error occurrence.
type MetricsError struct {
	Datetime time.Time
	Status   string
	Count    int64
}

// graphQL request/response types

type graphqlRequest struct {
	Query     string                 `json:"query"`
	Variables map[string]interface{} `json:"variables"`
}

type graphqlResponse struct {
	Data   json.RawMessage `json:"data"`
	Errors []graphqlError  `json:"errors"`
}

type graphqlError struct {
	Message string `json:"message"`
}

// Internal response shape for workersInvocationsAdaptive
type analyticsData struct {
	Viewer struct {
		Accounts []struct {
			WorkersInvocationsAdaptive []adaptiveBucket `json:"workersInvocationsAdaptive"`
		} `json:"accounts"`
	} `json:"viewer"`
}

type adaptiveBucket struct {
	Dimensions struct {
		Datetime   string `json:"datetime"`
		ScriptName string `json:"scriptName"`
		Status     string `json:"status"`
	} `json:"dimensions"`
	Sum struct {
		Requests    int64 `json:"requests"`
		Errors      int64 `json:"errors"`
		Subrequests int64 `json:"subrequests"`
	} `json:"sum"`
	Quantiles struct {
		CPUTimeP50 float64 `json:"cpuTimeP50"`
		CPUTimeP99 float64 `json:"cpuTimeP99"`
	} `json:"quantiles"`
}

const analyticsQuery = `
query WorkerAnalytics($accountTag: String!, $scriptName: String!, $since: Time!, $until: Time!) {
  viewer {
    accounts(filter: {accountTag: $accountTag}) {
      workersInvocationsAdaptive(
        filter: {
          scriptName: $scriptName,
          datetime_geq: $since,
          datetime_leq: $until
        }
        orderBy: [datetime_ASC]
        limit: 10000
      ) {
        dimensions {
          datetime
          scriptName
          status
        }
        sum {
          requests
          errors
          subrequests
        }
        quantiles {
          cpuTimeP50
          cpuTimeP99
        }
      }
    }
  }
}
`

// FetchWorkerMetrics queries the Cloudflare GraphQL Analytics API for a single worker's metrics.
func (c *AnalyticsClient) FetchWorkerMetrics(ctx context.Context, scriptName string, tr TimeRange) (*WorkerMetrics, error) {
	now := time.Now().UTC()
	since := now.Add(-tr.Duration)

	variables := map[string]interface{}{
		"accountTag": c.accountID,
		"scriptName": scriptName,
		"since":      since.Format(time.RFC3339),
		"until":      now.Format(time.RFC3339),
	}

	body, err := c.doGraphQL(ctx, analyticsQuery, variables)
	if err != nil {
		return nil, err
	}

	var data analyticsData
	if err := json.Unmarshal(body, &data); err != nil {
		return nil, fmt.Errorf("parsing analytics response: %w", err)
	}

	if len(data.Viewer.Accounts) == 0 {
		return &WorkerMetrics{ScriptName: scriptName, TimeRange: tr}, nil
	}

	buckets := data.Viewer.Accounts[0].WorkersInvocationsAdaptive
	return buildMetrics(scriptName, tr, buckets), nil
}

func buildMetrics(scriptName string, tr TimeRange, raw []adaptiveBucket) *WorkerMetrics {
	m := &WorkerMetrics{
		ScriptName:   scriptName,
		TimeRange:    tr,
		StatusCounts: make(map[string]int64),
	}

	var totalCPU50, totalCPU99 float64
	var cpuCount int

	for _, b := range raw {
		dt, _ := time.Parse(time.RFC3339, b.Dimensions.Datetime)

		m.TotalRequests += b.Sum.Requests
		m.TotalErrors += b.Sum.Errors
		m.TotalSubrequests += b.Sum.Subrequests

		if b.Quantiles.CPUTimeP50 > 0 || b.Quantiles.CPUTimeP99 > 0 {
			totalCPU50 += b.Quantiles.CPUTimeP50
			totalCPU99 += b.Quantiles.CPUTimeP99
			cpuCount++
		}

		status := b.Dimensions.Status
		if status != "" {
			m.StatusCounts[status] += b.Sum.Requests
		}

		m.Buckets = append(m.Buckets, MetricsBucket{
			Datetime:    dt,
			Requests:    b.Sum.Requests,
			Errors:      b.Sum.Errors,
			Subrequests: b.Sum.Subrequests,
			CPUTimeP50:  b.Quantiles.CPUTimeP50,
			CPUTimeP99:  b.Quantiles.CPUTimeP99,
			Status:      status,
		})

		// Collect error buckets
		if status != "" && status != "success" && b.Sum.Requests > 0 {
			m.Errors = append(m.Errors, MetricsError{
				Datetime: dt,
				Status:   status,
				Count:    b.Sum.Requests,
			})
		}
	}

	if cpuCount > 0 {
		m.CPUTimeP50 = totalCPU50 / float64(cpuCount)
		m.CPUTimeP99 = totalCPU99 / float64(cpuCount)
	}

	// Keep only last 20 errors (truncate oldest if too many)
	if len(m.Errors) > 20 {
		m.Errors = m.Errors[len(m.Errors)-20:]
	}

	return m
}

func (c *AnalyticsClient) doGraphQL(ctx context.Context, query string, variables map[string]interface{}) (json.RawMessage, error) {
	reqBody := graphqlRequest{
		Query:     query,
		Variables: variables,
	}

	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshaling GraphQL request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.cloudflare.com/client/v4/graphql", bytes.NewReader(jsonBody))
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/json")
	if c.authToken != "" {
		req.Header.Set("Authorization", "Bearer "+c.authToken)
	} else {
		req.Header.Set("X-Auth-Email", c.authEmail)
		req.Header.Set("X-Auth-Key", c.authKey)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("analytics API request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading analytics API response: %w", err)
	}

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return nil, &AuthError{StatusCode: resp.StatusCode, Body: truncateBody(body, 200)}
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("analytics API returned %d: %s", resp.StatusCode, truncateBody(body, 200))
	}

	var gqlResp graphqlResponse
	if err := json.Unmarshal(body, &gqlResp); err != nil {
		return nil, fmt.Errorf("parsing GraphQL envelope: %w", err)
	}

	if len(gqlResp.Errors) > 0 {
		msg := gqlResp.Errors[0].Message
		if isGraphQLAuthError(msg) {
			return nil, &AuthError{StatusCode: resp.StatusCode, Body: msg}
		}
		return nil, fmt.Errorf("GraphQL error: %s", msg)
	}

	return gqlResp.Data, nil
}

// isGraphQLAuthError returns true if the GraphQL error message indicates
// insufficient permissions (the GraphQL API returns HTTP 200 with error body).
func isGraphQLAuthError(msg string) bool {
	return msg == "not authorized for that account" ||
		msg == "not authorized" ||
		strings.Contains(msg, "not authorized")
}
