package search

import (
	"testing"

	"github.com/oarafat/orangeshell/internal/service"
)

func TestSetItemsRefilters(t *testing.T) {
	m := New()

	// Simulate: user opens search, types "foo"
	m.Reset()
	m.query = "foo"
	m.filter()

	if len(m.results) != 0 {
		t.Fatalf("expected 0 results with no items, got %d", len(m.results))
	}

	// Simulate: Workers loaded (no match for "foo")
	m.SetItems([]service.Resource{
		{ID: "worker1", Name: "my-worker", ServiceType: "Workers"},
	})

	if len(m.results) != 0 {
		t.Fatalf("expected 0 results for 'foo' against 'my-worker', got %d", len(m.results))
	}

	// Simulate: KV loaded (match for "foo")
	m.SetItems([]service.Resource{
		{ID: "worker1", Name: "my-worker", ServiceType: "Workers"},
		{ID: "kv1", Name: "foobar", ServiceType: "KV"},
	})

	if len(m.results) != 1 {
		t.Fatalf("expected 1 result for 'foo' after KV added, got %d", len(m.results))
	}
	if m.results[0].Name != "foobar" {
		t.Fatalf("expected 'foobar', got %q", m.results[0].Name)
	}

	// Simulate: R2 loaded (another match)
	m.SetItems([]service.Resource{
		{ID: "worker1", Name: "my-worker", ServiceType: "Workers"},
		{ID: "kv1", Name: "foobar", ServiceType: "KV"},
		{ID: "r2-1", Name: "foo-bucket", ServiceType: "R2"},
	})

	if len(m.results) != 2 {
		t.Fatalf("expected 2 results after R2 added, got %d", len(m.results))
	}
}

func TestSetItemsPreservesQuery(t *testing.T) {
	m := New()
	m.query = "bla"
	m.filter()

	// Initially empty
	if len(m.results) != 0 {
		t.Fatalf("expected 0 results, got %d", len(m.results))
	}

	// Add items incrementally
	m.SetItems([]service.Resource{
		{ID: "w1", Name: "blabla", ServiceType: "Workers"},
	})
	if len(m.results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(m.results))
	}

	m.SetItems([]service.Resource{
		{ID: "w1", Name: "blabla", ServiceType: "Workers"},
		{ID: "r1", Name: "blablubber", ServiceType: "R2"},
	})
	if len(m.results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(m.results))
	}
}

func TestFilterWithFooBucket(t *testing.T) {
	m := New()
	m.query = "f"

	items := []service.Resource{
		{ID: "my-worker", Name: "my-worker", ServiceType: "Workers"},
		{ID: "ns1", Name: "foobar", ServiceType: "KV"},
		{ID: "bucket1", Name: "foo-bucket", ServiceType: "R2"},
		{ID: "bucket2", Name: "bar-bucket", ServiceType: "R2"},
	}

	m.SetItems(items)

	if len(m.results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(m.results))
	}
}
