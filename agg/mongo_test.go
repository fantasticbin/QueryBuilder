package agg

import (
	"context"
	"encoding/json"
	"strings"
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
	builder := NewMongoBuilder[mongoSummary](data)
	builder.GroupByDesc("region", "region").
		GroupBy("channel", "channel").
		Count("total").
		Avg("amount", "amount_avg").
		SetLimit(25)
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
	builder := NewMongoBuilder[mongoSummary](data)
	builder.GroupByDesc("region", "region").
		Count("total").
		CountDistinct("customer.id", "buyer_count").
		SumDistinct("amount", "unique_amount_sum").
		Avg("amount", "amount_avg").
		SetLimit(10)
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
	if len(pipeline) != 5 {
		t.Fatalf("expected five pipeline stages, got %d: %s", len(pipeline), explanation)
	}
	if strings.Contains(explanation, `"$facet"`) {
		t.Fatalf("distinct metrics should not use $facet: %s", explanation)
	}

	group := pipeline[1].(map[string]any)["$group"].(map[string]any)
	if _, ok := group["buyer_count"].(map[string]any)["$addToSet"]; !ok {
		t.Fatalf("expected distinct count to use $addToSet: %v", group["buyer_count"])
	}
	if _, ok := group["unique_amount_sum"].(map[string]any)["$addToSet"]; !ok {
		t.Fatalf("expected distinct sum to use $addToSet: %v", group["unique_amount_sum"])
	}

	project := pipeline[2].(map[string]any)["$project"].(map[string]any)
	if _, ok := project["buyer_count"].(map[string]any)["$size"]; !ok {
		t.Fatalf("expected distinct count projection $size: %v", project["buyer_count"])
	}
	if _, ok := project["unique_amount_sum"].(map[string]any)["$sum"]; !ok {
		t.Fatalf("expected distinct sum projection $sum: %v", project["unique_amount_sum"])
	}
	sort := pipeline[3].(map[string]any)["$sort"].(map[string]any)
	if sort["region"].(map[string]any)["$numberInt"] != "-1" {
		t.Fatalf("unexpected sort stage: %v", sort)
	}
}

func TestMongoBuilderNestedFilterCloneIsolation(t *testing.T) {
	t.Parallel()

	data := core.NewDBProxyWithAdapters(core.NewMongoAdapter(&mongo.Collection{}))
	original := NewMongoBuilder[mongoSummary](data)
	original.Count("total")
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

func TestMongoBuilderExplainAdvancedSpec(t *testing.T) {
	t.Parallel()

	data := core.NewDBProxyWithAdapters(core.NewMongoAdapter(&mongo.Collection{}))
	builder := NewMongoBuilder[mongoSummary](data)
	builder.GroupBy("region", "region").
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
	var payload map[string]any
	if err := json.Unmarshal([]byte(explanation), &payload); err != nil {
		t.Fatalf("decoding explanation: %v", err)
	}
	pipeline := payload["pipeline"].([]any)
	if strings.Contains(explanation, `"$facet"`) {
		t.Fatalf("conditional metrics should not use $facet: %s", explanation)
	}
	group := pipeline[1].(map[string]any)["$group"].(map[string]any)
	if _, ok := group["paid_total"].(map[string]any)["$sum"].(map[string]any)["$cond"]; !ok {
		t.Fatalf("expected conditional count to use $cond: %v", group["paid_total"])
	}
	postHaving := pipeline[len(pipeline)-3].(map[string]any)["$match"].(map[string]any)
	if _, ok := postHaving["paid_amount"]; !ok {
		t.Fatalf("expected having match on paid_amount: %v", postHaving)
	}
	sort := pipeline[len(pipeline)-2].(map[string]any)["$sort"].(map[string]any)
	if sort["paid_amount"].(map[string]any)["$numberInt"] != "-1" {
		t.Fatalf("unexpected sort stage: %v", sort)
	}
}

func TestMongoExistsAlignsWithIsNotNull(t *testing.T) {
	t.Parallel()

	builder := NewMongoBuilder[mongoSummary](nil)
	exists := builder.buildConditionMatch(Condition{Field: "paid_at", Op: Exists})
	notNull := builder.buildConditionMatch(Condition{Field: "paid_at", Op: IsNotNull})
	if exists[0].Key != "paid_at" {
		t.Fatalf("expected field match, got %#v", exists)
	}
	operators := exists[0].Value.(bson.D)
	if operators[0].Key != "$exists" || operators[0].Value != true || operators[1].Key != "$ne" || operators[1].Value != nil {
		t.Fatalf("EXISTS should require present non-null field, got %#v", operators)
	}
	if len(exists) != len(notNull) || exists[0].Key != notNull[0].Key {
		t.Fatalf("EXISTS and IS NOT NULL should match, exists=%#v notNull=%#v", exists, notNull)
	}

	missing := builder.buildConditionMatch(Condition{Field: "paid_at", Op: NotExists})
	isNull := builder.buildConditionMatch(Condition{Field: "paid_at", Op: IsNull})
	if missing[0].Key != "$or" || isNull[0].Key != "$or" {
		t.Fatalf("NOT EXISTS / IS NULL should treat missing or null as empty, got %#v / %#v", missing, isNull)
	}
}

func TestMongoLikeAndNotLikeSharePresentAndToString(t *testing.T) {
	t.Parallel()

	like := mongoConditionExpr(Condition{Field: "customer.name", Op: Like, Value: "A%"})
	notLike := mongoConditionExpr(Condition{Field: "customer.name", Op: NotLike, Value: "A%"})
	likeDoc, ok := like.(bson.D)
	if !ok || len(likeDoc) != 1 || likeDoc[0].Key != "$and" {
		t.Fatalf("LIKE should require present field, got %#v", like)
	}
	notLikeDoc, ok := notLike.(bson.D)
	if !ok || len(notLikeDoc) != 1 || notLikeDoc[0].Key != "$and" {
		t.Fatalf("NOT LIKE should require present field, got %#v", notLike)
	}

	likeEncoded, err := bson.MarshalExtJSON(likeDoc, true, false)
	if err != nil {
		t.Fatalf("encode LIKE: %v", err)
	}
	notLikeEncoded, err := bson.MarshalExtJSON(notLikeDoc, true, false)
	if err != nil {
		t.Fatalf("encode NOT LIKE: %v", err)
	}
	for _, payload := range []string{string(likeEncoded), string(notLikeEncoded)} {
		if strings.Contains(payload, "$ifNull") {
			t.Fatalf("null must not be coerced to empty string: %s", payload)
		}
		if !strings.Contains(payload, `"$toString"`) || !strings.Contains(payload, `"$regexMatch"`) {
			t.Fatalf("expected $toString+$regexMatch, got %s", payload)
		}
	}
}

func TestMongoConditionNotEqualExcludesMissingAndNull(t *testing.T) {
	t.Parallel()

	builder := NewMongoBuilder[mongoSummary](nil)
	match := builder.buildConditionMatch(Condition{Field: "status", Op: Ne, Value: "paid"})
	if len(match) != 1 || match[0].Key != "$and" {
		t.Fatalf("expected $and condition, got %#v", match)
	}
	clauses := match[0].Value.(bson.A)
	if len(clauses) != 2 {
		t.Fatalf("expected field existence and expression clauses, got %#v", clauses)
	}
	fieldClause := clauses[0].(bson.D)
	if fieldClause[0].Key != "status" {
		t.Fatalf("expected status field clause, got %#v", fieldClause)
	}
	operators := fieldClause[0].Value.(bson.D)
	if operators[0].Key != "$exists" || operators[0].Value != true || operators[1].Key != "$ne" || operators[1].Value != nil {
		t.Fatalf("unexpected field operators: %#v", operators)
	}
	exprClause := clauses[1].(bson.D)
	if exprClause[0].Key != "$expr" {
		t.Fatalf("expected expression clause, got %#v", exprClause)
	}
}
func TestMongoHavingNotEqualExcludesMissingAndNull(t *testing.T) {
	t.Parallel()

	builder := NewMongoBuilder[mongoSummary](nil)
	match := builder.buildHavingClause(Having{Alias: "paid_amount", Op: Ne, Value: 100})
	if len(match) != 1 || match[0].Key != "$and" {
		t.Fatalf("expected $and having condition, got %#v", match)
	}
	clauses := match[0].Value.(bson.A)
	if len(clauses) != 2 {
		t.Fatalf("expected field existence and comparison clauses, got %#v", clauses)
	}
	fieldClause := clauses[0].(bson.D)
	if fieldClause[0].Key != "paid_amount" {
		t.Fatalf("expected paid_amount field clause, got %#v", fieldClause)
	}
	operators := fieldClause[0].Value.(bson.D)
	if operators[0].Key != "$exists" || operators[0].Value != true || operators[1].Key != "$ne" || operators[1].Value != nil {
		t.Fatalf("unexpected field operators: %#v", operators)
	}
	comparisonClause := clauses[1].(bson.D)
	comparison := comparisonClause[0].Value.(bson.D)
	if comparisonClause[0].Key != "paid_amount" || comparison[0].Key != "$ne" || comparison[0].Value != 100 {
		t.Fatalf("unexpected comparison clause: %#v", comparisonClause)
	}
}

func TestMongoBuilderExplainRichConditionsAndDateGroup(t *testing.T) {
	t.Parallel()

	data := core.NewDBProxyWithAdapters(core.NewMongoAdapter(&mongo.Collection{}))
	builder := NewMongoBuilder[mongoSummary](data)
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
		`"$dateTrunc"`,
		`"timezone": "Asia/Shanghai"`,
		`"$in"`,
		`"$regexMatch"`,
		`"flags": {`,
		`"$numberLong": "6"`,
	} {
		if !strings.Contains(explanation, fragment) {
			t.Fatalf("expected explanation to contain %q: %s", fragment, explanation)
		}
	}
}

func TestMongoBuilderExplainWeekDateGroupStartsOnMonday(t *testing.T) {
	t.Parallel()

	data := core.NewDBProxyWithAdapters(core.NewMongoAdapter(&mongo.Collection{}))
	builder := NewMongoBuilder[mongoSummary](data)
	builder.GroupByDate("created_at", "created_week", TimeIntervalWeek).
		Count("total")

	explanation, err := builder.Explain(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, fragment := range []string{
		`"unit": "week"`,
		`"startOfWeek": "monday"`,
	} {
		if !strings.Contains(explanation, fragment) {
			t.Fatalf("expected explanation to contain %q: %s", fragment, explanation)
		}
	}
}

func TestMongoBuilderExplainOffsetPaginationAndTotalPipeline(t *testing.T) {
	t.Parallel()

	data := core.NewDBProxyWithAdapters(core.NewMongoAdapter(&mongo.Collection{}))
	builder := NewMongoBuilder[mongoSummary](data)
	builder.GroupBy("region", "region").
		Count("total").
		Having("total >= ?", 3).
		SetStart(10).
		SetLimit(5)
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
	wantRows := []string{"$match", "$group", "$project", "$match", "$sort", "$skip", "$limit"}
	if len(pipeline) != len(wantRows) {
		t.Fatalf("expected %d row pipeline stages, got %d: %s", len(wantRows), len(pipeline), explanation)
	}
	for i, want := range wantRows {
		if got := mongoTestStageKey(t, pipeline[i]); got != want {
			t.Fatalf("stage %d: expected %s, got %s", i, want, got)
		}
	}
	if got := mongoTestNumberString(pipeline[5].(map[string]any)["$skip"]); got != "10" {
		t.Fatalf("expected $skip 10, got %v", pipeline[5])
	}
	if got := mongoTestNumberString(pipeline[6].(map[string]any)["$limit"]); got != "5" {
		t.Fatalf("expected $limit 5, got %v", pipeline[6])
	}

	totalPipeline := payload["total_pipeline"].([]any)
	wantTotal := []string{"$match", "$group", "$project", "$match", "$count"}
	if len(totalPipeline) != len(wantTotal) {
		t.Fatalf("expected %d total pipeline stages, got %d: %s", len(wantTotal), len(totalPipeline), explanation)
	}
	for i, want := range wantTotal {
		if got := mongoTestStageKey(t, totalPipeline[i]); got != want {
			t.Fatalf("total stage %d: expected %s, got %s", i, want, got)
		}
	}
}

func mongoTestStageKey(t *testing.T, stage any) string {
	t.Helper()
	mapped, ok := stage.(map[string]any)
	if !ok || len(mapped) != 1 {
		t.Fatalf("expected single-key stage, got %#v", stage)
	}
	for key := range mapped {
		return key
	}
	return ""
}

func mongoTestNumberString(value any) string {
	mapped, ok := value.(map[string]any)
	if !ok {
		return ""
	}
	for _, key := range []string{"$numberLong", "$numberInt", "$numberDouble"} {
		if number, ok := mapped[key].(string); ok {
			return number
		}
	}
	return ""
}

func TestMongoBuilderExplainExcludesTotalPipelineWhenNeedTotalFalse(t *testing.T) {
	t.Parallel()

	data := core.NewDBProxyWithAdapters(core.NewMongoAdapter(&mongo.Collection{}))
	builder := NewMongoBuilder[mongoSummary](data)
	builder.GroupBy("region", "region").
		Count("total").
		Having("total >= ?", 3).
		SetNeedTotal(false).
		SetLimit(5)

	explanation, err := builder.Explain(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(explanation), &payload); err != nil {
		t.Fatalf("decoding explanation: %v", err)
	}
	if _, ok := payload["total_pipeline"]; ok {
		t.Fatal("expected total_pipeline to be absent when needTotal=false")
	}
	if _, ok := payload["pipeline"]; !ok {
		t.Fatal("expected pipeline to always be present")
	}
}

func TestMongoBuilderExplainCapsTotalPipelineWithTotalLimit(t *testing.T) {
	t.Parallel()

	data := core.NewDBProxyWithAdapters(core.NewMongoAdapter(&mongo.Collection{}))
	capped := NewMongoBuilder[mongoSummary](data)
	capped.GroupBy("region", "region").
		Count("total").
		Having("total >= ?", 3).
		SetLimit(5).
		SetTotalLimit(10)

	cappedExplanation, err := capped.Explain(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var cappedPayload map[string]any
	if err := json.Unmarshal([]byte(cappedExplanation), &cappedPayload); err != nil {
		t.Fatalf("decoding explanation: %v", err)
	}
	cappedTotal, ok := cappedPayload["total_pipeline"].([]any)
	if !ok {
		t.Fatal("expected total_pipeline to be present when needTotal defaults true")
	}
	if !mongoTestPipelineHasStage(cappedTotal, "$limit") {
		t.Fatalf("expected capped total_pipeline to contain $limit, got %s", cappedExplanation)
	}

	uncapped := NewMongoBuilder[mongoSummary](data)
	uncapped.GroupBy("region", "region").
		Count("total").
		Having("total >= ?", 3).
		SetLimit(5)

	uncappedExplanation, err := uncapped.Explain(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var uncappedPayload map[string]any
	if err := json.Unmarshal([]byte(uncappedExplanation), &uncappedPayload); err != nil {
		t.Fatalf("decoding explanation: %v", err)
	}
	uncappedTotal, ok := uncappedPayload["total_pipeline"].([]any)
	if !ok {
		t.Fatal("expected total_pipeline to be present when needTotal defaults true")
	}
	if mongoTestPipelineHasStage(uncappedTotal, "$limit") {
		t.Fatalf("expected uncapped total_pipeline not to contain $limit, got %s", uncappedExplanation)
	}
}

func mongoTestPipelineHasStage(pipeline []any, stageKey string) bool {
	for _, stage := range pipeline {
		if mapped, ok := stage.(map[string]any); ok {
			if _, has := mapped[stageKey]; has {
				return true
			}
		}
	}
	return false
}
