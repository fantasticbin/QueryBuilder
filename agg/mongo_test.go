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
	Region string  `bson:"region"`
	Total  int64   `bson:"total"`
	Amount float64 `bson:"amount_avg"`
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
