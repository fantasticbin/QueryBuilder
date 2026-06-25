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
	Region          string   `json:"region"`
	Total           int64    `json:"total"`
	BuyerCount      int64    `json:"buyer_count"`
	UniqueAmountSum *float64 `json:"unique_amount_sum"`
	Amount          *float64 `json:"amount_sum"`
}

func TestElasticSearchBuilderExplain(t *testing.T) {
	t.Parallel()

	data := core.NewDBProxyWithAdapters(core.NewElasticSearchAdapter(&elastic.Client{}))
	builder := NewElasticSearchBuilder[elasticSummary](data, "orders")
	builder.GroupByDesc("region.keyword", "region").
		Count("total").
		CountDistinct("customer.id", "buyer_count").
		Sum("amount", "amount_sum").
		SumDistinct("amount", "unique_amount_sum").
		SetLimit(30)
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
		`"cardinality"`,
		`"customer.id"`,
		`"sum"`,
		`"scripted_metric"`,
		`state.values = [:]`,
		`containsKey`,
		`"params"`,
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
	builder := NewElasticSearchBuilder[elasticSummary](data, "orders")
	builder.GroupBy("region.keyword", "region").
		Count("total").
		CountDistinct("customer.id", "buyer_count").
		Sum("amount", "amount_sum").
		SumDistinct("amount", "unique_amount_sum")

	root := json.RawMessage(`{
		"doc_count": 3,
		"_querybuilder_groups": {
			"buckets": [{
				"key": {"region": "east"},
				"doc_count": 2,
				"buyer_count": {"value": 2},
				"amount_sum": {"value": 42.5},
				"unique_amount_sum": {"value": 40.0}
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
	if row.Region != "east" || row.Total != 2 || row.BuyerCount != 2 || row.UniqueAmountSum == nil || *row.UniqueAmountSum != 40 || row.Amount == nil || *row.Amount != 42.5 {
		t.Fatalf("unexpected decoded row: %+v", row)
	}
}

func TestElasticSearchBuilderDecodeScalarEmptyValues(t *testing.T) {
	t.Parallel()

	data := core.NewDBProxyWithAdapters(core.NewElasticSearchAdapter(&elastic.Client{}))
	builder := NewElasticSearchBuilder[elasticSummary](data, "orders")
	builder.Count("total").Sum("amount", "amount_sum")
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
