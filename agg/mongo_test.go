package agg

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/fantasticbin/QueryBuilder/v2/core"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

type mongoSummary struct {
	Region     string  `bson:"region"`
	Total      int64   `bson:"total"`
	BuyerCount int64   `bson:"buyer_count"`
	Amount     float64 `bson:"amount_avg"`
}

func TestMongoBuilderExplain(t *testing.T) {
	t.Parallel()

	data := core.NewDBProxyWithAdapters(core.NewMongoAdapter(&mongo.Collection{}))
	builder := NewMongoBuilder[mongoSummary](data, Spec{
		Groups: []Group{
			{Field: "region", Alias: "region", Descending: true},
			{Field: "channel", Alias: "channel"},
		},
		Metrics: []Metric{
			{Func: Count, Alias: "total"},
			{Func: Avg, Field: "amount", Alias: "amount_avg"},
		},
		Limit: 25,
	})
	builder.SetFilter(bson.D{{Key: "status", Value: "paid"}})

	explanation, err := builder.Explain(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(explanation), &payload); err != nil {
		t.Fatalf("decoding explanation: %v", err)
	}
	pipeline, ok := payload["pipeline"].([]any)
	if !ok {
		t.Fatalf("expected pipeline array, got %T", payload["pipeline"])
	}
	if len(pipeline) != 5 {
		t.Fatalf("expected five pipeline stages, got %d: %s", len(pipeline), explanation)
	}

	group := pipeline[1].(map[string]any)["$group"].(map[string]any)
	if _, ok := group["total"].(map[string]any)["$sum"]; !ok {
		t.Fatalf("expected count accumulator: %s", explanation)
	}
	if _, ok := group["amount_avg"].(map[string]any)["$avg"]; !ok {
		t.Fatalf("expected avg accumulator: %s", explanation)
	}
	sort := pipeline[3].(map[string]any)["$sort"].(map[string]any)
	regionOrder := sort["region"].(map[string]any)["$numberInt"]
	channelOrder := sort["channel"].(map[string]any)["$numberInt"]
	if regionOrder != "-1" || channelOrder != "1" {
		t.Fatalf("unexpected sort stage: %v", sort)
	}
}

func TestMongoBuilderExplainDistinctMetrics(t *testing.T) {
	t.Parallel()

	data := core.NewDBProxyWithAdapters(core.NewMongoAdapter(&mongo.Collection{}))
	builder := NewMongoBuilder[mongoSummary](data, Spec{
		Groups: []Group{{Field: "region", Alias: "region", Descending: true}},
		Metrics: []Metric{
			{Func: Count, Alias: "total"},
			{Func: Count, Field: "customer.id", Alias: "buyer_count", Distinct: true},
			{Func: Sum, Field: "amount", Alias: "unique_amount_sum", Distinct: true},
			{Func: Avg, Field: "amount", Alias: "amount_avg"},
		},
		Limit: 10,
	})
	builder.SetFilter(bson.D{{Key: "status", Value: "paid"}})

	explanation, err := builder.Explain(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(explanation), &payload); err != nil {
		t.Fatalf("decoding explanation: %v", err)
	}
	pipeline := payload["pipeline"].([]any)
	if len(pipeline) != 9 {
		t.Fatalf("expected nine pipeline stages, got %d: %s", len(pipeline), explanation)
	}

	facet := pipeline[1].(map[string]any)["$facet"].(map[string]any)
	if _, ok := facet[mongoBaseFacet].([]any); !ok {
		t.Fatalf("expected base facet: %s", explanation)
	}
	distinct := facet["_distinct_1"].([]any)
	if len(distinct) != 4 {
		t.Fatalf("expected four distinct facet stages, got %d: %v", len(distinct), distinct)
	}
	match := distinct[0].(map[string]any)["$match"].(map[string]any)
	if _, ok := match["customer.id"]; !ok {
		t.Fatalf("expected distinct field non-empty match: %v", match)
	}
	if _, ok := distinct[1].(map[string]any)["$group"]; !ok {
		t.Fatalf("expected first distinct group stage: %v", distinct[1])
	}
	if _, ok := distinct[2].(map[string]any)["$group"]; !ok {
		t.Fatalf("expected second distinct group stage: %v", distinct[2])
	}
	distinctSum := facet["_distinct_2"].([]any)
	sumGroup := distinctSum[2].(map[string]any)["$group"].(map[string]any)
	if sumGroup["unique_amount_sum"].(map[string]any)["$sum"] != "$_id.value" {
		t.Fatalf("expected sum distinct to add unique values: %v", sumGroup)
	}

	project := pipeline[2].(map[string]any)["$project"].(map[string]any)
	concat := project[mongoRowsField].(map[string]any)["$concatArrays"].([]any)
	if len(concat) != 3 || concat[0] != "$"+mongoBaseFacet || concat[1] != "$_distinct_1" || concat[2] != "$_distinct_2" {
		t.Fatalf("unexpected concat arrays: %v", concat)
	}

	merge := pipeline[5].(map[string]any)["$group"].(map[string]any)
	if _, ok := merge["total"].(map[string]any)["$max"]; !ok {
		t.Fatalf("expected regular metrics to use max during facet merge: %v", merge["total"])
	}
	buyerCount := merge["buyer_count"].(map[string]any)["$sum"].(map[string]any)
	if _, ok := buyerCount["$ifNull"]; !ok {
		t.Fatalf("expected distinct count to default missing values to zero: %v", buyerCount)
	}
	uniqueAmountSum := merge["unique_amount_sum"].(map[string]any)["$sum"].(map[string]any)
	if _, ok := uniqueAmountSum["$ifNull"]; !ok {
		t.Fatalf("expected sum distinct to preserve negative values while defaulting missing branches: %v", uniqueAmountSum)
	}
	sort := pipeline[7].(map[string]any)["$sort"].(map[string]any)
	if sort["region"].(map[string]any)["$numberInt"] != "-1" {
		t.Fatalf("unexpected sort stage: %v", sort)
	}
}

func TestMongoBuilderNestedFilterCloneIsolation(t *testing.T) {
	t.Parallel()

	data := core.NewDBProxyWithAdapters(core.NewMongoAdapter(&mongo.Collection{}))
	original := NewMongoBuilder[mongoSummary](data, Spec{
		Metrics: []Metric{{Func: Count, Alias: "total"}},
	})
	original.SetFilter(bson.D{{
		Key: "status",
		Value: bson.D{{
			Key:   "$in",
			Value: bson.A{"paid", "settled"},
		}},
	}})

	cloned := original.Clone()
	clonedNested := cloned.filter[0].Value.(bson.D)
	clonedValues := clonedNested[0].Value.(bson.A)
	clonedValues[0] = "refunded"
	clonedNested[0].Key = "$nin"

	originalNested := original.filter[0].Value.(bson.D)
	originalValues := originalNested[0].Value.(bson.A)
	if originalNested[0].Key != "$in" || originalValues[0] != "paid" {
		t.Fatalf("nested filter was shared between clone and original: %#v", original.filter)
	}
}
