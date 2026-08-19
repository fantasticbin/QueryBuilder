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

type elasticAdvancedSummary struct {
	Region     string   `json:"region"`
	Total      int64    `json:"total"`
	PaidTotal  int64    `json:"paid_total"`
	PaidAmount *float64 `json:"paid_amount"`
}

func TestElasticSearchBuilderExplainAdvancedSpec(t *testing.T) {
	t.Parallel()

	data := core.NewDBProxyWithAdapters(core.NewElasticSearchAdapter(&elastic.Client{}))
	builder := NewElasticSearchBuilder[elasticAdvancedSummary](data, "orders")
	builder.GroupBy("region.keyword", "region").
		Count("total").
		CountIf("paid_total", "status = ?", "paid").
		SumIf("amount", "paid_amount", "status = ?", "paid").
		Having("paid_amount >= ?", 100).
		OrderByDesc("paid_amount").
		SetLimit(5)

	explanation, err := builder.Explain(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, fragment := range []string{
		`"paid_total"`,
		`"filter"`,
		`"term"`,
		`"paid_amount"`,
		`"client_post_processing"`,
		`"full_scan": true`,
		`"havings": true`,
		`"orders": true`,
		`"limit": 5`,
		`"size": 5000`,
	} {
		if !strings.Contains(explanation, fragment) {
			t.Fatalf("expected explanation to contain %q: %s", fragment, explanation)
		}
	}
	if !strings.Contains(explanation, `"bucket_selector"`) {
		t.Fatalf("expected HAVING to attach bucket_selector: %s", explanation)
	}
	if strings.Contains(explanation, `"bucket_sort"`) {
		t.Fatalf("expected explanation not to contain bucket_sort: %s", explanation)
	}
}

func TestElasticSearchBuilderExplainGroupAliasOrderUsesComposite(t *testing.T) {
	t.Parallel()

	data := core.NewDBProxyWithAdapters(core.NewElasticSearchAdapter(&elastic.Client{}))
	builder := NewElasticSearchBuilder[elasticAdvancedSummary](data, "orders")
	builder.GroupBy("region.keyword", "region").
		GroupBy("channel.keyword", "channel").
		Count("total").
		OrderByDesc("region").
		SetNeedTotal(false).
		SetLimit(5)

	if builder.needsElasticClientPostProcessing() {
		t.Fatalf("group-prefix ordering should be handled by composite source order")
	}
	explanation, err := builder.Explain(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, fragment := range []string{`"order": "desc"`, `"size": 5`} {
		if !strings.Contains(explanation, fragment) {
			t.Fatalf("expected explanation to contain %q: %s", fragment, explanation)
		}
	}
	for _, fragment := range []string{`"client_post_processing"`, `"bucket_sort"`} {
		if strings.Contains(explanation, fragment) {
			t.Fatalf("expected explanation not to contain %q: %s", fragment, explanation)
		}
	}
}

func TestElasticSearchBuilderExplainHavingOnlyAvoidsFullScan(t *testing.T) {
	t.Parallel()

	data := core.NewDBProxyWithAdapters(core.NewElasticSearchAdapter(&elastic.Client{}))
	builder := NewElasticSearchBuilder[elasticAdvancedSummary](data, "orders")
	builder.GroupBy("region.keyword", "region").
		Count("total").
		Having("total >= ?", 2).
		SetNeedTotal(false).
		SetLimit(5)

	if !builder.needsElasticClientPostProcessing() {
		t.Fatalf("having still needs client-side filtering before limit")
	}
	if builder.needsElasticFullClientPostProcessing() {
		t.Fatalf("having-only filtering should not require collecting all buckets")
	}
	explanation, err := builder.Explain(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, fragment := range []string{`"client_post_processing"`, `"full_scan": false`, `"havings": true`, `"bucket_selector"`, `"size": 5000`} {
		if !strings.Contains(explanation, fragment) {
			t.Fatalf("expected explanation to contain %q: %s", fragment, explanation)
		}
	}
	if strings.Contains(explanation, `"bucket_sort"`) {
		t.Fatalf("expected having-only explanation not to contain bucket_sort: %s", explanation)
	}
}

func TestElasticSearchBuilderExplainExactTotalMarksFullScan(t *testing.T) {
	t.Parallel()

	data := core.NewDBProxyWithAdapters(core.NewElasticSearchAdapter(&elastic.Client{}))
	builder := NewElasticSearchBuilder[elasticAdvancedSummary](data, "orders")
	builder.GroupBy("region.keyword", "region").
		Count("total").
		SetLimit(5)

	if !builder.needsElasticCompleteCompositeScan() || !builder.needsElasticExactTotalScan() {
		t.Fatal("default needTotal should scan all composite pages for an exact total")
	}
	explanation, err := builder.Explain(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, fragment := range []string{`"full_scan": true`, `"total": true`} {
		if !strings.Contains(explanation, fragment) {
			t.Fatalf("expected explanation to contain %q: %s", fragment, explanation)
		}
	}
}
func TestElasticSearchBuilderDecodeConditionalMetrics(t *testing.T) {
	t.Parallel()

	data := core.NewDBProxyWithAdapters(core.NewElasticSearchAdapter(&elastic.Client{}))
	builder := NewElasticSearchBuilder[elasticAdvancedSummary](data, "orders")
	builder.GroupBy("region.keyword", "region").
		Count("total").
		CountIf("paid_total", "status = ?", "paid").
		SumIf("amount", "paid_amount", "status = ?", "paid")

	root := json.RawMessage(`{
		"doc_count": 4,
		"_querybuilder_groups": {
			"buckets": [{
				"key": {"region": "east"},
				"doc_count": 4,
				"paid_total": {"doc_count": 3},
				"paid_amount": {"doc_count": 3, "paid_amount": {"value": 42.5}}
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
	if row.Region != "east" || row.Total != 4 || row.PaidTotal != 3 || row.PaidAmount == nil || *row.PaidAmount != 42.5 {
		t.Fatalf("unexpected decoded row: %+v", row)
	}
}

func TestElasticSearchBuilderDecodeAdvancedSpecPostProcessing(t *testing.T) {
	t.Parallel()

	data := core.NewDBProxyWithAdapters(core.NewElasticSearchAdapter(&elastic.Client{}))
	builder := NewElasticSearchBuilder[elasticAdvancedSummary](data, "orders")
	builder.GroupBy("region.keyword", "region").
		Count("total").
		Sum("amount", "paid_amount").
		Having("paid_amount >= ?", 100).
		OrderByDesc("paid_amount").
		SetLimit(1)

	root := json.RawMessage(`{
		"doc_count": 9,
		"_querybuilder_groups": {
			"buckets": [
				{"key": {"region": "east"}, "doc_count": 3, "paid_amount": {"value": 90}},
				{"key": {"region": "west"}, "doc_count": 2, "paid_amount": {"value": 200}},
				{"key": {"region": "north"}, "doc_count": 4, "paid_amount": {"value": 150}}
			]
		}
	}`)
	result, err := builder.decodeResult(&elastic.SearchResult{
		Aggregations: elastic.Aggregations{elasticRootAggregation: root},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Rows) != 1 || result.Total != 2 {
		t.Fatalf("expected one row with total 2 after post-processing, got %+v", result)
	}
	row := result.Rows[0]
	if row.Region != "west" || row.Total != 2 || row.PaidAmount == nil || *row.PaidAmount != 200 {
		t.Fatalf("unexpected post-processed row: %+v", row)
	}
}

func TestElasticConditionNotEqualRequiresExistingField(t *testing.T) {
	t.Parallel()

	source, err := elasticConditionQuery(Condition{Field: "status", Op: Ne, Value: "paid"}).Source()
	if err != nil {
		t.Fatalf("building query source: %v", err)
	}
	encoded, err := json.Marshal(source)
	if err != nil {
		t.Fatalf("encoding query source: %v", err)
	}
	query := string(encoded)
	for _, fragment := range []string{`"exists"`, `"field":"status"`, `"must_not"`, `"term"`} {
		if !strings.Contains(query, fragment) {
			t.Fatalf("expected not-equal query to contain %q: %s", fragment, query)
		}
	}
}

func TestElasticSearchBuilderExplainRichConditionsAndDateGroup(t *testing.T) {
	t.Parallel()

	data := core.NewDBProxyWithAdapters(core.NewElasticSearchAdapter(&elastic.Client{}))
	builder := NewElasticSearchBuilder[elasticAdvancedSummary](data, "orders")
	builder.GroupByDateWithTimeZone("created_at", "created_day", TimeIntervalDay, "Asia/Shanghai").
		CountIf("paid_total", "status IN ?", []string{"paid", "settled"}).
		SumIf("amount", "mid_amount", "amount BETWEEN ? AND ?", Range{Start: 10, End: 20}).
		CountIf("named_total", "customer.name LIKE ?", "A%").
		SetLimit(5)

	explanation, err := builder.Explain(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, fragment := range []string{
		`"date_histogram"`,
		`"calendar_interval": "day"`,
		`"time_zone": "Asia/Shanghai"`,
		`"terms"`,
		`"range"`,
		`"wildcard"`,
		`"flags": 6`,
	} {
		if !strings.Contains(explanation, fragment) {
			t.Fatalf("expected explanation to contain %q: %s", fragment, explanation)
		}
	}
}

func TestElasticSearchBuilderDecodeResultOffsetPagination(t *testing.T) {
	t.Parallel()

	data := core.NewDBProxyWithAdapters(core.NewElasticSearchAdapter(&elastic.Client{}))
	builder := NewElasticSearchBuilder[elasticSummary](data, "orders")
	builder.GroupBy("region.keyword", "region").
		Count("total").
		SetStart(1).
		SetLimit(1)

	root := json.RawMessage(`{
		"doc_count": 9,
		"_querybuilder_groups": {
			"buckets": [
				{"key": {"region": "east"}, "doc_count": 3},
				{"key": {"region": "west"}, "doc_count": 2},
				{"key": {"region": "north"}, "doc_count": 4}
			]
		}
	}`)
	result, err := builder.decodeResult(&elastic.SearchResult{
		Aggregations: elastic.Aggregations{elasticRootAggregation: root},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Rows) != 1 || result.Total != 3 {
		t.Fatalf("expected one paged row with total 3, got %+v", result)
	}
	if result.Rows[0].Region != "west" || result.Rows[0].Total != 2 {
		t.Fatalf("unexpected paged row: %+v", result.Rows[0])
	}
}

func TestElasticSearchBuilderDecodeHavingOnlyOffsetPagination(t *testing.T) {
	t.Parallel()

	data := core.NewDBProxyWithAdapters(core.NewElasticSearchAdapter(&elastic.Client{}))
	builder := NewElasticSearchBuilder[elasticAdvancedSummary](data, "orders")
	builder.GroupBy("region.keyword", "region").
		Count("total").
		Having("total >= ?", 2).
		SetStart(1).
		SetLimit(1)

	root := json.RawMessage(`{
		"doc_count": 10,
		"_querybuilder_groups": {
			"buckets": [
				{"key": {"region": "east"}, "doc_count": 1},
				{"key": {"region": "west"}, "doc_count": 3},
				{"key": {"region": "north"}, "doc_count": 2},
				{"key": {"region": "south"}, "doc_count": 4}
			]
		}
	}`)
	result, err := builder.decodeResult(&elastic.SearchResult{
		Aggregations: elastic.Aggregations{elasticRootAggregation: root},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Rows) != 1 || result.Total != 3 {
		t.Fatalf("expected one paged row with total 3 after HAVING, got %+v", result)
	}
	if result.Rows[0].Region != "north" || result.Rows[0].Total != 2 {
		t.Fatalf("unexpected HAVING paged row: %+v", result.Rows[0])
	}
}

func TestElasticSearchBuilderDecodeFullPostProcessingOffsetPagination(t *testing.T) {
	t.Parallel()

	data := core.NewDBProxyWithAdapters(core.NewElasticSearchAdapter(&elastic.Client{}))
	builder := NewElasticSearchBuilder[elasticAdvancedSummary](data, "orders")
	builder.GroupBy("region.keyword", "region").
		Count("total").
		Sum("amount", "paid_amount").
		Having("paid_amount >= ?", 100).
		OrderByDesc("paid_amount").
		SetStart(1).
		SetLimit(1)

	root := json.RawMessage(`{
		"doc_count": 12,
		"_querybuilder_groups": {
			"buckets": [
				{"key": {"region": "east"}, "doc_count": 3, "paid_amount": {"value": 90}},
				{"key": {"region": "west"}, "doc_count": 2, "paid_amount": {"value": 200}},
				{"key": {"region": "north"}, "doc_count": 4, "paid_amount": {"value": 150}},
				{"key": {"region": "south"}, "doc_count": 3, "paid_amount": {"value": 120}}
			]
		}
	}`)
	result, err := builder.decodeResult(&elastic.SearchResult{
		Aggregations: elastic.Aggregations{elasticRootAggregation: root},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Rows) != 1 || result.Total != 3 {
		t.Fatalf("expected one paged row with total 3 after full post-processing, got %+v", result)
	}
	row := result.Rows[0]
	if row.Region != "north" || row.PaidAmount == nil || *row.PaidAmount != 150 {
		t.Fatalf("unexpected full post-processing paged row: %+v", row)
	}
}

func TestElasticSearchBuilderDecodeResultSkipsTotalWhenNeedTotalFalse(t *testing.T) {
	t.Parallel()

	data := core.NewDBProxyWithAdapters(core.NewElasticSearchAdapter(&elastic.Client{}))
	builder := NewElasticSearchBuilder[elasticSummary](data, "orders")
	builder.GroupBy("region.keyword", "region").
		Count("total").
		SetNeedTotal(false).
		SetLimit(100)

	root := json.RawMessage(`{
		"doc_count": 9,
		"_querybuilder_groups": {
			"buckets": [
				{"key": {"region": "east"}, "doc_count": 3},
				{"key": {"region": "west"}, "doc_count": 2},
				{"key": {"region": "north"}, "doc_count": 4}
			]
		}
	}`)
	result, err := builder.decodeResult(&elastic.SearchResult{
		Aggregations: elastic.Aggregations{elasticRootAggregation: root},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Rows) != 3 {
		t.Fatalf("expected 3 rows, got %d", len(result.Rows))
	}
	if result.Total != 0 {
		t.Fatalf("expected Total 0 when needTotal=false, got %d", result.Total)
	}
}

func TestElasticSearchBuilderDecodeResultCapsTotalWithTotalLimit(t *testing.T) {
	t.Parallel()

	data := core.NewDBProxyWithAdapters(core.NewElasticSearchAdapter(&elastic.Client{}))
	builder := NewElasticSearchBuilder[elasticSummary](data, "orders")
	builder.GroupBy("region.keyword", "region").
		Count("total").
		SetTotalLimit(2).
		SetLimit(100)

	root := json.RawMessage(`{
		"doc_count": 9,
		"_querybuilder_groups": {
			"buckets": [
				{"key": {"region": "east"}, "doc_count": 3},
				{"key": {"region": "west"}, "doc_count": 2},
				{"key": {"region": "north"}, "doc_count": 4}
			]
		}
	}`)
	result, err := builder.decodeResult(&elastic.SearchResult{
		Aggregations: elastic.Aggregations{elasticRootAggregation: root},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Rows) != 3 {
		t.Fatalf("expected 3 rows, got %d", len(result.Rows))
	}
	if result.Total != 2 {
		t.Fatalf("expected capped Total 2, got %d", result.Total)
	}
}

// TestElasticSearchBuilderDecodeResultTotalLimitCapsNotBoundsPage 锁定评审中描述的
// 有界总数语义（P2-3 #2）：Total 是一个上限值，当上限小于请求页大小时，返回页中的行数可能
// 超过 Total。调用方应把 Total 视为“>=”而非精确值。
func TestElasticSearchBuilderDecodeResultTotalLimitCapsNotBoundsPage(t *testing.T) {
	t.Parallel()

	data := core.NewDBProxyWithAdapters(core.NewElasticSearchAdapter(&elastic.Client{}))
	builder := NewElasticSearchBuilder[elasticSummary](data, "orders")
	builder.GroupBy("region.keyword", "region").
		Count("total").
		SetLimit(10).
		SetTotalLimit(2)

	root := json.RawMessage(`{
		"doc_count": 50,
		"_querybuilder_groups": {
			"buckets": [
				{"key": {"region": "a"}, "doc_count": 1},
				{"key": {"region": "b"}, "doc_count": 1},
				{"key": {"region": "c"}, "doc_count": 1},
				{"key": {"region": "d"}, "doc_count": 1},
				{"key": {"region": "e"}, "doc_count": 1}
			]
		}
	}`)
	result, err := builder.decodeResult(&elastic.SearchResult{
		Aggregations: elastic.Aggregations{elasticRootAggregation: root},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Rows) != 5 {
		t.Fatalf("expected page of 5 rows (bounded by Limit, not Total), got %d", len(result.Rows))
	}
	if result.Total != 2 {
		t.Fatalf("expected capped Total 2, got %d", result.Total)
	}
}
