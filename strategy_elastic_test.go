package builder

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/olivere/elastic/v7"
)

type elasticEntity struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type roundTripper func(*http.Request) (*http.Response, error)

func (rt roundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	return rt(req)
}

func TestQueryElasticListStrategy(t *testing.T) {
	hits := []map[string]any{
		{
			"_index": "test",
			"_type":  "_doc",
			"_id":    "1",
			"_source": map[string]any{
				"id":   "1",
				"name": "Alice",
			},
		},
		{
			"_index": "test",
			"_type":  "_doc",
			"_id":    "2",
			"_source": map[string]any{
				"id":   "2",
				"name": "Bob",
			},
		},
	}

	rt := roundTripper(func(r *http.Request) (*http.Response, error) {
		if got, want := r.URL.Path, "/test/_search"; got != want {
			t.Fatalf("unexpected path: %s", got)
		}

		if r.Method != http.MethodGet {
			t.Fatalf("unexpected method: %s", r.Method)
		}

		resp := map[string]any{
			"took":      1,
			"timed_out": false,
			"_shards": map[string]any{
				"total":      1,
				"successful": 1,
				"skipped":    0,
				"failed":     0,
			},
			"hits": map[string]any{
				"total": map[string]any{
					"value":    2,
					"relation": "eq",
				},
				"max_score": nil,
				"hits":      hits,
			},
		}

		buf := bytes.NewBuffer(nil)
		if err := json.NewEncoder(buf).Encode(resp); err != nil {
			return nil, err
		}

		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(buf),
		}, nil
	})

	client, err := elastic.NewClient(
		elastic.SetURL("http://example.com"),
		elastic.SetSniff(false),
		elastic.SetHealthcheck(false),
		elastic.SetHttpClient(&http.Client{Transport: rt}),
	)
	if err != nil {
		t.Fatalf("failed to create elastic client: %v", err)
	}

	b := &builder[elasticEntity]{
		data:           NewDBProxy(nil, nil).SetElastic(client, "test"),
		start:          0,
		limit:          2,
		needTotal:      true,
		needPagination: true,
	}

	b.SetFilter(func(context.Context) (any, error) {
		return elastic.NewMatchAllQuery(), nil
	})

	b.SetSort(func() any {
		return []elastic.Sorter{elastic.NewFieldSort("id").Asc()}
	})

	strategy := NewQueryElasticListStrategy[elasticEntity]()

	list, total, err := strategy.QueryList(context.Background(), b)
	if err != nil {
		t.Fatalf("QueryList returned error: %v", err)
	}

	if total != 2 {
		t.Fatalf("unexpected total: %d", total)
	}

	if len(list) != 2 {
		t.Fatalf("unexpected result size: %d", len(list))
	}

	if list[0].Name != "Alice" || list[1].Name != "Bob" {
		t.Fatalf("unexpected results: %+v", list)
	}
}
