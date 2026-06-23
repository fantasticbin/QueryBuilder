package agg

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/fantasticbin/QueryBuilder/v2/core"
	"github.com/olivere/elastic/v7"
)

type elasticSummary struct {
	Region string   `json:"region"`
	Total  int64    `json:"total"`
	Amount *float64 `json:"amount_sum"`
}

func TestElasticSearchBuilderExplain(t *testing.T) {
	t.Parallel()

	data := core.NewDBProxyWithAdapters(core.NewElasticSearchAdapter(&elastic.Client{}))
	builder := NewElasticSearchBuilder[elasticSummary](data, "orders", Spec{
		Groups: []Group{{Field: "region.keyword", Alias: "region", Descending: true}},
		Metrics: []Metric{
			{Func: Count, Alias: "total"},
			{Func: Sum, Field: "amount", Alias: "amount_sum"},
		},
		Limit: 30,
	})
	builder.SetFilter(elastic.NewTermQuery("status", "paid"))

	explanation, err := builder.Explain(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, fragment := range []string{
		`"index": "orders"`,
		`"composite"`,
		`"region.keyword"`,
		`"order": "desc"`,
		`"size": 30`,
		`"sum"`,
		`"status"`,
	} {
		if !strings.Contains(explanation, fragment) {
			t.Fatalf("expected explanation to contain %q: %s", fragment, explanation)
		}
	}
}

func TestElasticSearchBuilderDecodeResult(t *testing.T) {
	t.Parallel()

	data := core.NewDBProxyWithAdapters(core.NewElasticSearchAdapter(&elastic.Client{}))
	builder := NewElasticSearchBuilder[elasticSummary](data, "orders", Spec{
		Groups:  []Group{{Field: "region.keyword", Alias: "region"}},
		Metrics: []Metric{{Func: Count, Alias: "total"}, {Func: Sum, Field: "amount", Alias: "amount_sum"}},
	})

	root := json.RawMessage(`{
		"doc_count": 3,
		"_querybuilder_groups": {
			"buckets": [{
				"key": {"region": "east"},
				"doc_count": 2,
				"amount_sum": {"value": 42.5}
			}]
		}
	}`)
	result, err := builder.decodeResult(&elastic.SearchResult{
		Aggregations: elastic.Aggregations{elasticRootAggregation: root},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Rows) != 1 {
		t.Fatalf("expected one row, got %d", len(result.Rows))
	}
	row := result.Rows[0]
	if row.Region != "east" || row.Total != 2 || row.Amount == nil || *row.Amount != 42.5 {
		t.Fatalf("unexpected decoded row: %+v", row)
	}
}

func TestElasticSearchBuilderDecodeScalarEmptyValues(t *testing.T) {
	t.Parallel()

	data := core.NewDBProxyWithAdapters(core.NewElasticSearchAdapter(&elastic.Client{}))
	builder := NewElasticSearchBuilder[elasticSummary](data, "orders", Spec{
		Metrics: []Metric{{Func: Count, Alias: "total"}, {Func: Sum, Field: "amount", Alias: "amount_sum"}},
	})
	root := json.RawMessage(`{"doc_count":0,"amount_sum":{"value":null}}`)
	result, err := builder.decodeResult(&elastic.SearchResult{
		Aggregations: elastic.Aggregations{elasticRootAggregation: root},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Rows) != 1 || result.Rows[0].Total != 0 || result.Rows[0].Amount != nil {
		t.Fatalf("unexpected scalar row: %+v", result.Rows)
	}
}
