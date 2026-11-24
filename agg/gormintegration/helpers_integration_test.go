//go:build integration

// 本文件复制自 agg/es_agg_integration_test.go 的数据集与断言辅助函数，保证各后端使用同一份测试数据
package gormintegration_test

import (
	"fmt"
	"math"
	"testing"
	"time"
)

type esAggDoc struct {
	ID         int      `json:"id"`
	Region     *string  `json:"region,omitempty"`
	Channel    string   `json:"channel"`
	Status     string   `json:"status"`
	CustomerID string   `json:"customer_id"`
	Amount     *float64 `json:"amount,omitempty"`
	Quantity   int      `json:"quantity"`
	CreatedAt  string   `json:"created_at"`
	Bio        string   `json:"bio"`
	SKU        string   `json:"sku"`
}

type regionRow struct {
	Region        string   `json:"region"`
	Total         int64    `json:"total"`
	AmountSum     *float64 `json:"amount_sum"`
	Buyers        int64    `json:"buyers"`
	UniqueAmount  *float64 `json:"unique_amount"`
	PaidTotal     int64    `json:"paid_total"`
	PaidAmount    *float64 `json:"paid_amount"`
	PaidBuyers    int64    `json:"paid_buyers"`
	UniquePaidAmt *float64 `json:"unique_paid_amount"`
}

type scalarRow struct {
	Total     int64    `json:"total"`
	Buyers    int64    `json:"buyers"`
	UniqueAmt *float64 `json:"unique_amount"`
}

type dayRow struct {
	Day   string   `json:"day"`
	Total int64    `json:"total"`
	Sum   *float64 `json:"amount_sum"`
}

func ptrStr(s string) *string   { return &s }
func ptrF64(v float64) *float64 { return &v }
func abs64(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}

func makeAggDocs() []esAggDoc {
	base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	regions := []string{"east", "west", "north"}
	channels := []string{"web", "app"}
	docs := make([]esAggDoc, 0, 36)
	for i := 1; i <= 36; i++ {
		region := regions[(i-1)%3]
		d := esAggDoc{
			ID:         i,
			Region:     ptrStr(region),
			Channel:    channels[(i-1)%2],
			Status:     map[bool]string{true: "paid", false: "pending"}[i%2 == 1],
			CustomerID: fmt.Sprintf("c%02d", ((i-1)%8)+1),
			Amount:     ptrF64(float64(i) * 10.5),
			Quantity:   (i % 5) + 1,
			CreatedAt:  base.AddDate(0, 0, (i-1)%10).Format(time.RFC3339),
			Bio:        "loves elasticsearch aggregation queries",
			SKU:        fmt.Sprintf("sku-%d", ((i-1)%6)+1),
		}
		if i%9 == 0 {
			d.Amount = nil
		}
		if i%12 == 0 {
			d.Region = nil
		}
		docs = append(docs, d)
	}
	return docs
}

func docsWithRegion(docs []esAggDoc) []esAggDoc {
	out := make([]esAggDoc, 0, len(docs))
	for _, d := range docs {
		if d.Region != nil {
			out = append(out, d)
		}
	}
	return out
}
func filterDocs(docs []esAggDoc, pred func(esAggDoc) bool) []esAggDoc {
	out := make([]esAggDoc, 0)
	for _, d := range docs {
		if pred(d) {
			out = append(out, d)
		}
	}
	return out
}
func amounts(docs []esAggDoc) []float64 {
	var out []float64
	for _, d := range docs {
		if d.Amount != nil {
			out = append(out, *d.Amount)
		}
	}
	return out
}
func sumF(vals []float64) float64 {
	var s float64
	for _, v := range vals {
		s += v
	}
	return s
}
func uniqueCustomers(docs []esAggDoc) int {
	set := map[string]struct{}{}
	for _, d := range docs {
		set[d.CustomerID] = struct{}{}
	}
	return len(set)
}
func uniqueAmounts(docs []esAggDoc) float64 {
	set := map[float64]struct{}{}
	var s float64
	for _, d := range docs {
		if d.Amount == nil {
			continue
		}
		if _, ok := set[*d.Amount]; ok {
			continue
		}
		set[*d.Amount] = struct{}{}
		s += *d.Amount
	}
	return s
}
func almostEqual(t *testing.T, got, want, tol float64, label string) {
	t.Helper()
	if math.Abs(got-want) > tol {
		t.Fatalf("%s: got=%v want=%v", label, got, want)
	}
}
func ptrAlmostEqual(t *testing.T, got *float64, want, tol float64, label string) {
	t.Helper()
	if got == nil {
		t.Fatalf("%s: nil want %v", label, want)
	}
	almostEqual(t, *got, want, tol, label)
}
