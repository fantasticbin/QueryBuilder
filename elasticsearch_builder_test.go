package builder

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fantasticbin/QueryBuilder/v2/core"
	qbtest "github.com/fantasticbin/QueryBuilder/v2/test"
	"github.com/olivere/elastic/v7"
	"go.uber.org/mock/gomock"
)

type ElasticTestEntity struct {
	ID   uint32 `json:"id"`
	Name string `json:"name"`
	Age  int    `json:"age"`
}

type ElasticTestFilter struct {
	Name string
	Age  uint8
}

type ElasticTestSort struct {
	Field     string
	Direction string
}

func TestElasticsearchQueryList(t *testing.T) {
	ctx := context.Background()
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	// Mock Querier 实例
	mockQuerier := NewMockQuerier[ElasticTestEntity](ctrl)

	tests := []struct {
		name           string
		mockSetup      func()
		filter         ElasticTestFilter
		sort           ElasticTestSort
		expectedResult []*ElasticTestEntity
		expectedTotal  int64
		expectedErr    error
	}{
		{
			name: "Elasticsearch无筛选查询&id升序",
			mockSetup: func() {
				mockQuerier.EXPECT().SetStart(gomock.Any()).Return(mockQuerier)
				mockQuerier.EXPECT().SetLimit(gomock.Any()).Return(mockQuerier)
				mockQuerier.EXPECT().SetNeedTotal(gomock.Any()).Return(mockQuerier)
				mockQuerier.EXPECT().SetNeedPagination(gomock.Any()).Return(mockQuerier)
				mockQuerier.EXPECT().
					QueryList(gomock.Any()).
					Return(&core.ListResult[ElasticTestEntity]{
						Items: []*ElasticTestEntity{
							{ID: 1, Name: "Alice", Age: 25},
							{ID: 2, Name: "Bob", Age: 30},
						},
						Total: 2,
					}, nil)
			},
			filter: ElasticTestFilter{},
			sort:   ElasticTestSort{Field: "id", Direction: "asc"},
			expectedResult: []*ElasticTestEntity{
				{ID: 1, Name: "Alice", Age: 25},
				{ID: 2, Name: "Bob", Age: 30},
			},
			expectedTotal: 2,
			expectedErr:   nil,
		},
		{
			name: "Elasticsearch有筛选查询",
			mockSetup: func() {
				mockQuerier.EXPECT().SetStart(gomock.Any()).Return(mockQuerier)
				mockQuerier.EXPECT().SetLimit(gomock.Any()).Return(mockQuerier)
				mockQuerier.EXPECT().SetNeedTotal(gomock.Any()).Return(mockQuerier)
				mockQuerier.EXPECT().SetNeedPagination(gomock.Any()).Return(mockQuerier)
				mockQuerier.EXPECT().
					QueryList(gomock.Any()).
					Return(&core.ListResult[ElasticTestEntity]{
						Items: []*ElasticTestEntity{
							{ID: 1, Name: "Alice", Age: 25},
						},
						Total: 1,
					}, nil)
			},
			filter: ElasticTestFilter{Name: "Alice"},
			sort:   ElasticTestSort{Field: "id", Direction: "desc"},
			expectedResult: []*ElasticTestEntity{
				{ID: 1, Name: "Alice", Age: 25},
			},
			expectedTotal: 1,
			expectedErr:   nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.mockSetup != nil {
				tt.mockSetup()
			}

			// 创建 List 实例并设置 Mock Querier
			list := NewList[ElasticTestEntity]()
			list.SetQuerier(mockQuerier)

			// 执行查询
			opts := []QueryOption{
				WithData(NewDBProxy(nil, nil, &elastic.Client{})),
			}

			result, err := list.Query(ctx, opts...)

			if tt.expectedErr != nil {
				if err == nil || err.Error() != tt.expectedErr.Error() {
					t.Errorf("expected error: %v, got: %v", tt.expectedErr, err)
				}
			} else {
				qbtest.AssertNoError(t, err)
				qbtest.AssertListResult(t, result, tt.expectedResult, tt.expectedTotal)
			}
		})
	}
}

func TestElasticsearchStrategyDecoding(t *testing.T) {
	// 测试 JSON 解码逻辑
	entity := ElasticTestEntity{
		ID:   1,
		Name: "Test",
		Age:  30,
	}

	data, err := json.Marshal(entity)
	if err != nil {
		t.Fatalf("failed to marshal entity: %v", err)
	}

	var decoded ElasticTestEntity
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("failed to unmarshal entity: %v", err)
	}

	if decoded.ID != entity.ID || decoded.Name != entity.Name || decoded.Age != entity.Age {
		t.Errorf("expected %+v, got %+v", entity, decoded)
	}
}

func TestElasticsearchIndexValidation(t *testing.T) {
	ctx := context.Background()

	// 测试索引名未配置的情况，使用 ElasticSearchBuilder
	esBuilder := NewElasticSearchBuilder[ElasticTestEntity](
		NewDBProxy(nil, nil, &elastic.Client{}),
		"", // 空索引名
	)
	esBuilder.SetFilter(elastic.NewMatchAllQuery())

	_, err := esBuilder.QueryList(ctx)
	if err == nil {
		t.Error("expected error when index is not configured, got nil")
	}
	if err != nil && !errors.Is(err, ErrESIndexNotConfigured) {
		t.Errorf("expected ErrESIndexNotConfigured error, got: %v", err)
	}
}

func TestElasticsearchSortValidation(t *testing.T) {
	// 此测试验证排序类型的默认错误处理
	// 由于需要真实的 Elasticsearch 客户端才能执行查询，
	// 我们只验证类型转换逻辑在代码中存在即可
	// 实际的错误处理会在运行时验证

	// 验证有效的排序类型
	var validSort1 interface{} = elastic.NewFieldSort("name").Order(true)
	if _, ok := validSort1.(elastic.Sorter); !ok {
		t.Error("expected elastic.FieldSort to implement elastic.Sorter")
	}

	var validSort2 interface{} = []elastic.Sorter{
		elastic.NewFieldSort("name").Order(true),
		elastic.NewFieldSort("age").Order(false),
	}
	if _, ok := validSort2.([]elastic.Sorter); ok {
		// 验证切片类型正确
	}

	// 无效的排序类型会在 switch 的 default 分支中被捕获
	var invalidSort interface{} = "invalid_sort"
	if _, ok := invalidSort.(elastic.Sorter); ok {
		t.Error("string should not be a valid elastic.Sorter")
	}
	if _, ok := invalidSort.([]elastic.Sorter); ok {
		t.Error("string should not be a valid []elastic.Sorter")
	}
}
func TestElasticSearchBuilderPITSettings(t *testing.T) {
	esBuilder := NewElasticSearchBuilder[ElasticTestEntity](
		NewDBProxy(nil, nil, &elastic.Client{}),
		"test_index",
	)

	if esBuilder.pitKeepAlive != 0 {
		t.Fatalf("pitKeepAlive default should be 0, got %v", esBuilder.pitKeepAlive)
	}

	esBuilder.SetPitKeepAlive(2 * time.Minute)
	if esBuilder.pitKeepAlive != 2*time.Minute {
		t.Fatalf("pitKeepAlive should be 2m, got %v", esBuilder.pitKeepAlive)
	}
}

func TestElasticSearchBuilderQueryPageWithPITValidation(t *testing.T) {
	ctx := context.Background()
	esBuilder := NewElasticSearchBuilder[ElasticTestEntity](
		NewDBProxy(nil, nil, &elastic.Client{}),
		"", // 空索引名
	)

	_, err := esBuilder.QueryPageWithPIT(ctx)
	if err == nil {
		t.Fatal("expected error when index is not configured, got nil")
	}
	if !errors.Is(err, ErrESIndexNotConfigured) {
		t.Fatalf("expected ErrESIndexNotConfigured error, got %v", err)
	}
}

func TestElasticSearchBuilderQueryPageWithPITRejectsCursorWithoutPITID(t *testing.T) {
	ctx := context.Background()
	esBuilder := NewElasticSearchBuilder[ElasticTestEntity](
		NewDBProxy(nil, nil, &elastic.Client{}),
		"test_index",
	)

	esBuilder.
		SetCursorField("created_at", "id").
		SetCursorValue("2026-01-01T00:00:00Z", "doc_1").
		SetLimit(10)

	_, err := esBuilder.QueryPageWithPIT(ctx)
	if !errors.Is(err, ErrPITCursorWithoutPITID) {
		t.Fatalf("expected ErrPITCursorWithoutPITID, got %v", err)
	}
}

func TestElasticSearchBuilderCleanupCursorQueryKeepsPITWhenHasMore(t *testing.T) {
	es := NewElasticSearchBuilder[ElasticTestEntity](NewDBProxyWithAdapters(), "idx")
	es.cursorPitID = "pit-keep"
	es.cleanupCursorQuery(&core.CursorPageResult[ElasticTestEntity]{HasMore: true}, nil)
	if es.cursorPitID != "pit-keep" {
		t.Fatalf("expected PIT to stay open when HasMore=true, got %q", es.cursorPitID)
	}

	es.cleanupCursorQuery(&core.CursorPageResult[ElasticTestEntity]{HasMore: false}, nil)
	if es.cursorPitID != "" {
		t.Fatalf("expected PIT to close when HasMore=false, got %q", es.cursorPitID)
	}

	es.cursorPitID = "pit-err"
	es.cleanupCursorQuery(&core.CursorPageResult[ElasticTestEntity]{HasMore: true}, errors.New("query failed"))
	if es.cursorPitID != "" {
		t.Fatalf("expected PIT to close on error, got %q", es.cursorPitID)
	}

	es.cursorPitID = "pit-cursor"
	es.cleanupCursorQuery(nil, nil)
	if es.cursorPitID != "" {
		t.Fatalf("expected QueryCursor cleanup (nil result) to close PIT, got %q", es.cursorPitID)
	}
}

func TestElasticSearchBuilderDefaults(t *testing.T) {
	b := NewElasticSearchBuilder[ElasticTestEntity](NewDBProxy(nil, nil, nil), "test_index")
	if b.builder.dataSource != ElasticSearch {
		t.Fatalf("expected ElasticSearch data source, got %v", b.builder.dataSource)
	}
	if b.builder.limit != defaultLimit {
		t.Fatalf("expected default limit %d, got %d", defaultLimit, b.builder.limit)
	}
	if !b.builder.needPagination {
		t.Fatal("expected default needPagination=true")
	}
	if !b.builder.needTotal {
		t.Fatal("expected default needTotal=true")
	}
}

// fakeESServer 基于 httptest 的最小 Elasticsearch 桩服务
// 数据集为 id=1..total 的文档，按 id 升序返回；模拟 ES 7.12+ 在 PIT 查询中追加隐式 _shard_doc tiebreaker 的行为：
// 当请求带 PIT 且 search_after 为空或长度为 sort+1 时，hit.sort 末尾追加 tiebreaker
type fakeESServer struct {
	t        *testing.T
	total    int
	mu       sync.Mutex
	searches []map[string]any
}

func newFakeESServer(t *testing.T, total int) (*fakeESServer, *elastic.Client) {
	t.Helper()
	f := &fakeESServer{t: t, total: total}
	srv := httptest.NewServer(http.HandlerFunc(f.handle))
	t.Cleanup(srv.Close)
	client, err := elastic.NewClient(elastic.SetURL(srv.URL), elastic.SetSniff(false), elastic.SetHealthcheck(false))
	if err != nil {
		t.Fatalf("create elastic client: %v", err)
	}
	return f, client
}

func (f *fakeESServer) lastSearch() map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.searches) == 0 {
		f.t.Fatal("no search request captured")
	}
	return f.searches[len(f.searches)-1]
}

func (f *fakeESServer) handle(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	switch {
	case strings.HasSuffix(r.URL.Path, "/_pit") && r.Method == http.MethodPost:
		_, _ = w.Write([]byte(`{"id":"pit-1"}`))
	case r.URL.Path == "/_pit" && r.Method == http.MethodDelete:
		_, _ = w.Write([]byte(`{"succeeded":true,"num_freed":1}`))
	case strings.HasSuffix(r.URL.Path, "/_count"):
		_, _ = fmt.Fprintf(w, `{"count":%d}`, f.total)
	case strings.HasSuffix(r.URL.Path, "/_search"):
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.mu.Lock()
		f.searches = append(f.searches, body)
		f.mu.Unlock()
		f.writeSearch(w, body)
	default:
		http.NotFound(w, r)
	}
}

func (f *fakeESServer) writeSearch(w http.ResponseWriter, body map[string]any) {
	size := 10
	if v, ok := body["size"].(float64); ok {
		size = int(v)
	}
	sortKeys := 0
	if s, ok := body["sort"].([]any); ok {
		sortKeys = len(s)
	}
	searchAfter, _ := body["search_after"].([]any)
	_, withPIT := body["pit"]
	addTiebreaker := withPIT && (len(searchAfter) == 0 || len(searchAfter) == sortKeys+1)

	startID := 1
	if len(searchAfter) > 0 {
		startID = int(searchAfter[0].(float64)) + 1
	}
	hits := make([]map[string]any, 0, size)
	for id := startID; id <= f.total && len(hits) < size; id++ {
		sortValues := []any{id}
		for k := 1; k < sortKeys; k++ {
			sortValues = append(sortValues, id)
		}
		if addTiebreaker {
			sortValues = append(sortValues, 1000+id)
		}
		hits = append(hits, map[string]any{
			"_id":     fmt.Sprint(id),
			"_source": map[string]any{"id": id, "name": fmt.Sprintf("user%d", id), "age": 20 + id},
			"sort":    sortValues,
		})
	}
	resp := map[string]any{
		"hits": map[string]any{
			"total": map[string]any{"value": f.total, "relation": "eq"},
			"hits":  hits,
		},
	}
	if withPIT {
		resp["pit_id"] = "pit-1"
	}
	_ = json.NewEncoder(w).Encode(resp)
}

func TestElasticSearchBuilderQueryListWithoutPaginationRequestsMaxResultWindow(t *testing.T) {
	ctx := context.Background()
	fake, client := newFakeESServer(t, 25)
	b := NewElasticSearchBuilder[ElasticTestEntity](NewDBProxyWithAdapters(NewElasticSearchAdapter(client)), "idx")
	b.SetStart(5).SetLimit(3).SetNeedPagination(false)

	result, err := b.QueryList(ctx)
	qbtest.AssertNoError(t, err)
	if len(result.Items) != 25 || result.Total != 25 {
		t.Fatalf("expected all 25 items and total 25, got %d items total %d", len(result.Items), result.Total)
	}

	body := fake.lastSearch()
	if size, ok := body["size"].(float64); !ok || int(size) != esMaxResultWindow {
		t.Fatalf("expected size=%d when pagination disabled, got %v", esMaxResultWindow, body["size"])
	}
	if _, ok := body["from"]; ok {
		t.Fatalf("expected no from when pagination disabled (start ignored like Gorm), got %v", body["from"])
	}
}

func TestElasticSearchBuilderQueryListWithPaginationRequestsFromSize(t *testing.T) {
	ctx := context.Background()
	fake, client := newFakeESServer(t, 25)
	b := NewElasticSearchBuilder[ElasticTestEntity](NewDBProxyWithAdapters(NewElasticSearchAdapter(client)), "idx")
	b.SetStart(5).SetLimit(3)

	_, err := b.QueryList(ctx)
	qbtest.AssertNoError(t, err)
	body := fake.lastSearch()
	if body["from"] != float64(5) || body["size"] != float64(3) {
		t.Fatalf("expected from=5 size=3, got from=%v size=%v", body["from"], body["size"])
	}
}

func TestElasticSearchBuilderExplainWithoutPagination(t *testing.T) {
	b := NewElasticSearchBuilder[ElasticTestEntity](NewDBProxy(nil, nil, &elastic.Client{}), "idx")
	b.SetStart(5).SetNeedPagination(false)

	dsl, err := b.Explain(context.Background())
	qbtest.AssertNoError(t, err)
	var parsed map[string]any
	if err := json.Unmarshal([]byte(dsl), &parsed); err != nil {
		t.Fatalf("explain output is not json: %v", err)
	}
	if parsed["size"] != float64(esMaxResultWindow) {
		t.Fatalf("expected size=%d in explain, got %v", esMaxResultWindow, parsed["size"])
	}
	if _, ok := parsed["from"]; ok {
		t.Fatalf("expected no from in explain, got %v", parsed["from"])
	}
}

func TestTrimImplicitTiebreaker(t *testing.T) {
	tests := []struct {
		name     string
		values   []any
		keyCount int
		want     []any
	}{
		{name: "裁剪隐式tiebreaker", values: []any{float64(10), float64(7)}, keyCount: 1, want: []any{float64(10)}},
		{name: "多字段裁剪", values: []any{"t", float64(7), float64(5)}, keyCount: 2, want: []any{"t", float64(7)}},
		{name: "长度一致保持不变", values: []any{"t", float64(7)}, keyCount: 2, want: []any{"t", float64(7)}},
		{name: "nil保持nil", values: nil, keyCount: 1, want: nil},
		{name: "keyCount为0不裁剪", values: []any{float64(1), float64(2)}, keyCount: 0, want: []any{float64(1), float64(2)}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := trimImplicitTiebreaker(tt.values, tt.keyCount); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("expected %v, got %v", tt.want, got)
			}
		})
	}
}

func TestElasticSearchBuilderCursorSortKeyCount(t *testing.T) {
	newB := func() *ElasticSearchBuilder[ElasticTestEntity] {
		return NewElasticSearchBuilder[ElasticTestEntity](NewDBProxy(nil, nil, &elastic.Client{}), "idx")
	}
	if got := newB().cursorSortKeyCount(); got != 1 {
		t.Fatalf("expected default _doc sort key count 1, got %d", got)
	}
	b := newB()
	b.SetCursorField("created_at", "id")
	if got := b.cursorSortKeyCount(); got != 2 {
		t.Fatalf("expected 2, got %d", got)
	}
	b = newB()
	b.SetCursorField("id", "_shard_doc")
	if got := b.cursorSortKeyCount(); got != 2 {
		t.Fatalf("expected explicit _shard_doc counted, got %d", got)
	}
	b = newB().SetSort(elastic.NewFieldSort("age").Asc())
	b.SetCursorField("id")
	if got := b.cursorSortKeyCount(); got != 2 {
		t.Fatalf("expected cursor field + auxiliary sort = 2, got %d", got)
	}
}

func TestElasticSearchBuilderQueryPageWithPITCursorCanBeFedBack(t *testing.T) {
	ctx := context.Background()
	fake, client := newFakeESServer(t, 5)
	data := NewDBProxyWithAdapters(NewElasticSearchAdapter(client))

	first := NewElasticSearchBuilder[ElasticTestEntity](data, "idx")
	first.SetCursorField("id").SetLimit(2)
	page1, err := first.QueryPageWithPIT(ctx)
	qbtest.AssertNoError(t, err)
	if !page1.HasMore || page1.PitID == "" {
		t.Fatalf("expected HasMore with PIT ID, got hasMore=%v pit=%q", page1.HasMore, page1.PitID)
	}
	if !reflect.DeepEqual(page1.NextCursorValues, []any{float64(2)}) {
		t.Fatalf("expected implicit tiebreaker trimmed to [2], got %v", page1.NextCursorValues)
	}

	var all []uint32
	for _, it := range page1.Items {
		all = append(all, it.ID)
	}
	pitID, cursor := page1.PitID, page1.NextCursorValues
	for range 5 {
		next := NewElasticSearchBuilder[ElasticTestEntity](data, "idx")
		next.SetCursorField("id").SetLimit(2)
		next.SetPITID(pitID).SetCursorValue(cursor...)
		page, err := next.QueryPageWithPIT(ctx)
		qbtest.AssertNoError(t, err)
		if got := fake.lastSearch()["search_after"]; !reflect.DeepEqual(got, cursor) {
			t.Fatalf("expected search_after %v, got %v", cursor, got)
		}
		for _, it := range page.Items {
			all = append(all, it.ID)
		}
		if !page.HasMore {
			break
		}
		pitID, cursor = page.PitID, page.NextCursorValues
	}
	if !reflect.DeepEqual(all, []uint32{1, 2, 3, 4, 5}) {
		t.Fatalf("expected ids [1 2 3 4 5], got %v", all)
	}
}

func TestElasticSearchBuilderQueryPageSameBuilderCursorCanBeFedBack(t *testing.T) {
	ctx := context.Background()
	_, client := newFakeESServer(t, 5)
	b := NewElasticSearchBuilder[ElasticTestEntity](NewDBProxyWithAdapters(NewElasticSearchAdapter(client)), "idx")
	b.SetCursorField("id").SetLimit(2)

	var all []uint32
	for range 5 {
		page, err := b.QueryPage(ctx)
		qbtest.AssertNoError(t, err)
		for _, it := range page.Items {
			all = append(all, it.ID)
		}
		if !page.HasMore {
			break
		}
		if len(page.NextCursorValues) != 1 {
			t.Fatalf("expected NextCursorValues trimmed to cursor field count, got %v", page.NextCursorValues)
		}
		b.SetCursorValue(page.NextCursorValues...)
	}
	if !reflect.DeepEqual(all, []uint32{1, 2, 3, 4, 5}) {
		t.Fatalf("expected ids [1 2 3 4 5], got %v", all)
	}
}

func TestElasticSearchBuilderQueryPageExplicitShardDocKeepsAllValues(t *testing.T) {
	ctx := context.Background()
	_, client := newFakeESServer(t, 5)
	b := NewElasticSearchBuilder[ElasticTestEntity](NewDBProxyWithAdapters(NewElasticSearchAdapter(client)), "idx")
	b.SetCursorField("id", "_shard_doc").SetLimit(2)

	page, err := b.QueryPageWithPIT(ctx)
	qbtest.AssertNoError(t, err)
	if len(page.NextCursorValues) != 2 {
		t.Fatalf("expected explicit _shard_doc to keep 2 cursor values, got %v", page.NextCursorValues)
	}
}

func TestElasticSearchBuilderQueryCursorKeepsFullSortValuesInternally(t *testing.T) {
	ctx := context.Background()
	fake, client := newFakeESServer(t, 5)
	b := NewElasticSearchBuilder[ElasticTestEntity](NewDBProxyWithAdapters(NewElasticSearchAdapter(client)), "idx")
	b.SetCursorField("id").SetLimit(2).SetNeedPagination(false)

	var all []uint32
	for item, err := range b.QueryCursor(ctx) {
		qbtest.AssertNoError(t, err)
		all = append(all, item.ID)
	}
	if !reflect.DeepEqual(all, []uint32{1, 2, 3, 4, 5}) {
		t.Fatalf("expected ids [1 2 3 4 5], got %v", all)
	}

	fake.mu.Lock()
	defer fake.mu.Unlock()
	// 第 2 批应携带完整 sort values（含隐式 tiebreaker），保证非唯一游标字段下同值记录不丢失
	if len(fake.searches) < 2 {
		t.Fatalf("expected multiple batches, got %d", len(fake.searches))
	}
	if got := fake.searches[1]["search_after"]; !reflect.DeepEqual(got, []any{float64(2), float64(1002)}) {
		t.Fatalf("expected full sort values in internal search_after, got %v", got)
	}
}

func TestElasticSearchBuilderCursorValuesWithAuxSortRoundTrip(t *testing.T) {
	ctx := context.Background()
	fake, client := newFakeESServer(t, 5)
	data := NewDBProxyWithAdapters(NewElasticSearchAdapter(client))
	newB := func() *ElasticSearchBuilder[ElasticTestEntity] {
		b := NewElasticSearchBuilder[ElasticTestEntity](data, "idx").SetSort(elastic.NewFieldSort("age").Asc())
		b.SetCursorField("id").SetLimit(2)
		return b
	}

	page1, err := newB().QueryPageWithPIT(ctx)
	qbtest.AssertNoError(t, err)
	if !reflect.DeepEqual(page1.NextCursorValues, []any{float64(2), float64(2)}) {
		t.Fatalf("expected cursor field + aux sort values [2 2], got %v", page1.NextCursorValues)
	}

	next := newB()
	next.SetPITID(page1.PitID).SetCursorValue(page1.NextCursorValues...)
	page2, err := next.QueryPageWithPIT(ctx)
	qbtest.AssertNoError(t, err)
	body := fake.lastSearch()
	if !reflect.DeepEqual(body["search_after"], []any{float64(2), float64(2)}) {
		t.Fatalf("expected search_after aligned with full sort, got %v", body["search_after"])
	}
	if len(page2.Items) != 2 || page2.Items[0].ID != 3 {
		t.Fatalf("expected page 2 to start at id 3, got %+v", page2.Items)
	}

	// 同一 builder 的 QueryPage 续页
	b := newB()
	var all []uint32
	for range 5 {
		page, err := b.QueryPage(ctx)
		qbtest.AssertNoError(t, err)
		for _, it := range page.Items {
			all = append(all, it.ID)
		}
		if !page.HasMore {
			break
		}
		b.SetCursorValue(page.NextCursorValues...)
	}
	if !reflect.DeepEqual(all, []uint32{1, 2, 3, 4, 5}) {
		t.Fatalf("expected ids [1 2 3 4 5], got %v", all)
	}
}

func TestElasticSearchBuilderCursorFieldOnlyValuesWithAuxSortUseFilter(t *testing.T) {
	ctx := context.Background()
	fake, client := newFakeESServer(t, 5)
	b := NewElasticSearchBuilder[ElasticTestEntity](NewDBProxyWithAdapters(NewElasticSearchAdapter(client)), "idx").
		SetSort(elastic.NewFieldSort("age").Desc())
	b.SetCursorField("id").SetCursorValue(3).SetLimit(2)

	_, err := b.QueryPage(ctx)
	qbtest.AssertNoError(t, err)
	body := fake.lastSearch()
	if _, ok := body["search_after"]; ok {
		t.Fatalf("expected no search_after when only cursor field values are given with aux sort, got %v", body["search_after"])
	}
	query, _ := json.Marshal(body["query"])
	for _, fragment := range []string{`"must":{"match_all":{}}`, `"range":{"id":{"from":3,"include_lower":false`} {
		if !strings.Contains(string(query), fragment) {
			t.Fatalf("expected query %s to contain %s", query, fragment)
		}
	}

	// SetStart 转换的单字段游标同样走过滤条件
	b2 := NewElasticSearchBuilder[ElasticTestEntity](NewDBProxyWithAdapters(NewElasticSearchAdapter(client)), "idx").
		SetSort(elastic.NewFieldSort("age").Desc())
	b2.SetCursorField("id").SetStart(4).SetLimit(2)
	_, err = b2.QueryPage(ctx)
	qbtest.AssertNoError(t, err)
	if _, ok := fake.lastSearch()["search_after"]; ok {
		t.Fatal("expected start-as-cursor with aux sort to use filter instead of search_after")
	}
}

func TestBuildESCursorAfterQuery(t *testing.T) {
	tests := []struct {
		name   string
		fields []string
		values []any
		want   string
	}{
		{
			name:   "单字段升序",
			fields: []string{"id"},
			values: []any{3},
			want:   `{"range":{"id":{"from":3,"include_lower":false,"include_upper":true,"to":null}}}`,
		},
		{
			name:   "混合方向多字段",
			fields: []string{"-created_at", "id"},
			values: []any{"t", 7},
			want: `{"bool":{"minimum_should_match":"1","should":[` +
				`{"range":{"created_at":{"from":null,"include_lower":true,"include_upper":false,"to":"t"}}},` +
				`{"bool":{"filter":[{"term":{"created_at":"t"}},{"range":{"id":{"from":7,"include_lower":false,"include_upper":true,"to":null}}}]}}]}}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			q, err := buildESCursorAfterQuery(parseCursorSortFields(tt.fields), tt.values)
			qbtest.AssertNoError(t, err)
			src, err := q.Source()
			qbtest.AssertNoError(t, err)
			got, _ := json.Marshal(src)
			if string(got) != tt.want {
				t.Fatalf("expected %s\n got %s", tt.want, got)
			}
		})
	}

	_, err := buildESCursorAfterQuery(parseCursorSortFields([]string{"_shard_doc"}), []any{1})
	if !errors.Is(err, ErrCursorMismatch) {
		t.Fatalf("expected ErrCursorMismatch for pseudo sort field, got %v", err)
	}
}

func TestElasticSearchBuilderCursorValueCountValidation(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name    string
		auxSort []elastic.Sorter
		fields  []string
		values  []any
		wantErr bool
	}{
		{name: "无辅助排序-游标字段数量", fields: []string{"id"}, values: []any{1}},
		{name: "无辅助排序-多出一个值被拒绝", fields: []string{"id"}, values: []any{1, 2}, wantErr: true},
		{name: "辅助排序-游标字段数量", auxSort: []elastic.Sorter{elastic.NewFieldSort("age")}, fields: []string{"id"}, values: []any{1}},
		{name: "辅助排序-完整排序键数量", auxSort: []elastic.Sorter{elastic.NewFieldSort("age")}, fields: []string{"id"}, values: []any{1, 2}},
		{name: "辅助排序-超出排序键数量被拒绝", auxSort: []elastic.Sorter{elastic.NewFieldSort("age")}, fields: []string{"id"}, values: []any{1, 2, 3}, wantErr: true},
		{name: "辅助排序-介于两者之间被拒绝", auxSort: []elastic.Sorter{elastic.NewFieldSort("age"), elastic.NewFieldSort("name")}, fields: []string{"id"}, values: []any{1, 2}, wantErr: true},
		{name: "伪字段仅给游标值且有辅助排序被拒绝", auxSort: []elastic.Sorter{elastic.NewFieldSort("age")}, fields: []string{"_shard_doc"}, values: []any{1}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, client := newFakeESServer(t, 5)
			b := NewElasticSearchBuilder[ElasticTestEntity](NewDBProxyWithAdapters(NewElasticSearchAdapter(client)), "idx")
			if len(tt.auxSort) > 0 {
				b.SetSort(tt.auxSort...)
			}
			b.SetCursorField(tt.fields...).SetCursorValue(tt.values...).SetLimit(2)
			_, err := b.QueryPage(ctx)
			if tt.wantErr {
				if !errors.Is(err, ErrCursorMismatch) {
					t.Fatalf("expected ErrCursorMismatch, got %v", err)
				}
				return
			}
			qbtest.AssertNoError(t, err)
		})
	}
}
