package builder

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/fantasticbin/QueryBuilder/v2/core"
	qbtest "github.com/fantasticbin/QueryBuilder/v2/test"
	"github.com/redis/go-redis/v9"
)

// ---------- 测试基建 ----------

// RedisTestEntity Redis JSON 文档测试实体
type RedisTestEntity struct {
	ID        uint32           `json:"id"`
	Name      string           `json:"name"`
	Age       int              `json:"age"`
	Price     float64          `json:"price"`
	Active    bool             `json:"active"`
	Tags      []string         `json:"tags"`
	Profile   RedisTestProfile `json:"profile"`
	Nullable  *string          `json:"nullable"`
	CreatedAt int64            `json:"created_at"`
}

// RedisTestProfile 嵌套属性实体
type RedisTestProfile struct {
	Age int `json:"age"`
}

// RedisJSONAliasEntity 别名恰好为 json 的局部投影实体
// 用于验证局部投影不会被误判为完整文档投影
type RedisJSONAliasEntity struct {
	JSON string `json:"json"`
}

// redisSearchCall 记录一次 FTSearchWithArgs 调用的参数快照
type redisSearchCall struct {
	Index   string
	Query   string
	Options *redis.FTSearchOptions
}

// fakeRedisSearchClient 伪造 go-redis 客户端，仅实现构建器依赖的 FTSearchWithArgs
// 嵌入 nil 的 UniversalClient 接口以满足类型约束，其余方法不应被触达
type fakeRedisSearchClient struct {
	redis.UniversalClient
	calls   []redisSearchCall
	results []redis.FTSearchResult
	err     error
}

func (f *fakeRedisSearchClient) FTSearchWithArgs(
	ctx context.Context,
	index string,
	query string,
	options *redis.FTSearchOptions,
) *redis.FTSearchCmd {
	// buildSearchOptions 每次调用都构造全新 options（内部切片均为克隆），指针快照安全
	f.calls = append(f.calls, redisSearchCall{Index: index, Query: query, Options: options})
	cmd := &redis.FTSearchCmd{}
	if f.err != nil {
		cmd.SetErr(f.err)
		return cmd
	}
	if i := len(f.calls) - 1; i < len(f.results) {
		cmd.SetVal(f.results[i])
	}
	return cmd
}

// newRedisTestProxy 构造仅注册 RedisSearch 适配器的 DBProxy
func newRedisTestProxy(client *fakeRedisSearchClient) *core.DBProxy {
	return NewDBProxyWithAdapters(NewRedisAdapter(client))
}

// redisJSONDoc 构造默认完整投影（$ AS json）下的 JSON 文档
func redisJSONDoc(payload string) redis.Document {
	return redis.Document{Fields: map[string]string{redisJSONReturnAlias: payload}}
}

func TestRedisSearchBuilderQueryListBuildsExpectedSearch(t *testing.T) {
	client := &fakeRedisSearchClient{
		results: []redis.FTSearchResult{{
			Total: 9,
			Docs:  []redis.Document{redisJSONDoc(`{"id":1,"name":"Alice","age":30,"price":12.5,"active":true,"created_at":100}`)},
		}},
	}
	b := NewRedisSearchBuilder[RedisTestEntity](newRedisTestProxy(client), "idx:user")
	b.SetFilter(RedisFilter{
		Query:          "@status:{active} @tenant:{$tenant}",
		NumericFilters: []redis.FTSearchFilter{{FieldName: "price", Min: 10, Max: 100}},
		InKeys:         []any{"user:1"},
		InFields:       []any{"name"},
		Params:         map[string]any{"tenant": "acme"},
	})
	b.SetSort(RedisSort{FieldName: "created_at", Desc: true})
	b.SetStart(5)
	b.SetLimit(2)
	b.SetNeedTotal(true)
	b.SetNeedPagination(true)

	result, err := b.QueryList(context.Background())
	qbtest.AssertNoError(t, err)
	qbtest.AssertListResult(t, result, []*RedisTestEntity{{ID: 1, Name: "Alice", Age: 30, Price: 12.5, Active: true, CreatedAt: 100}}, 9)

	if len(client.calls) != 1 {
		t.Fatalf("expected 1 redis call, got %d", len(client.calls))
	}
	call := client.calls[0]
	if call.Index != "idx:user" || call.Query != "@status:{active} @tenant:{$tenant}" {
		t.Fatalf("unexpected redis call: %+v", call)
	}
	opt := call.Options
	if opt.DialectVersion != redisDialectV2 {
		t.Fatalf("expected dialect %d, got %d", redisDialectV2, opt.DialectVersion)
	}
	if !reflect.DeepEqual(opt.Filters, []redis.FTSearchFilter{{FieldName: "price", Min: 10, Max: 100}}) {
		t.Fatalf("unexpected numeric filters: %+v", opt.Filters)
	}
	if !reflect.DeepEqual(opt.InKeys, []any{"user:1"}) || !reflect.DeepEqual(opt.InFields, []any{"name"}) {
		t.Fatalf("unexpected in keys/fields: %+v %+v", opt.InKeys, opt.InFields)
	}
	if !reflect.DeepEqual(opt.Params, map[string]any{"tenant": "acme"}) {
		t.Fatalf("unexpected params: %+v", opt.Params)
	}
	if !reflect.DeepEqual(opt.Return, []redis.FTSearchReturn{{FieldName: "$", As: redisJSONReturnAlias}}) {
		t.Fatalf("unexpected default return: %+v", opt.Return)
	}
	if !reflect.DeepEqual(opt.SortBy, []redis.FTSearchSortBy{{FieldName: "created_at", Desc: true}}) {
		t.Fatalf("unexpected sort: %+v", opt.SortBy)
	}
	if !opt.SortByWithCount {
		t.Fatal("expected SortByWithCount for accurate total")
	}
	if opt.LimitOffset != 5 || opt.Limit != 2 {
		t.Fatalf("unexpected limit window: [%d,%d]", opt.LimitOffset, opt.Limit)
	}
}

func TestRedisSearchBuilderDefaults(t *testing.T) {
	client := &fakeRedisSearchClient{results: []redis.FTSearchResult{{
		Total: 1,
		Docs:  []redis.Document{redisJSONDoc(`{"id":1}`)},
	}}}
	b := NewRedisSearchBuilder[RedisTestEntity](newRedisTestProxy(client), "idx:user")

	if b.builder.dataSource != RedisSearch {
		t.Fatalf("expected RedisSearch data source, got %v", b.builder.dataSource)
	}
	if b.documentKind != RedisJSON {
		t.Fatalf("expected default JSON document kind, got %v", b.documentKind)
	}
	if b.builder.limit != defaultLimit {
		t.Fatalf("expected default limit %d, got %d", defaultLimit, b.builder.limit)
	}
	if !b.builder.needPagination || !b.builder.needTotal {
		t.Fatal("expected default needPagination=true and needTotal=true")
	}
	meta := b.GetQueryMeta()
	if meta.DataSource != RedisSearch {
		t.Fatalf("expected RedisSearch meta, got %+v", meta)
	}

	b.SetSort(RedisSort{FieldName: "id", Asc: true})
	result, err := b.QueryList(context.Background())
	qbtest.AssertNoError(t, err)
	qbtest.AssertListResult(t, result, []*RedisTestEntity{{ID: 1}}, 1)

	call := client.calls[0]
	if call.Query != "*" {
		t.Fatalf("expected wildcard query, got %q", call.Query)
	}
	opt := call.Options
	if opt.LimitOffset != 0 || opt.Limit != int(defaultLimit) {
		t.Fatalf("unexpected default limit window: [%d,%d]", opt.LimitOffset, opt.Limit)
	}
	if !opt.SortByWithCount {
		t.Fatal("expected SortByWithCount with default needTotal")
	}
	if !reflect.DeepEqual(opt.Return, []redis.FTSearchReturn{{FieldName: "$", As: redisJSONReturnAlias}}) {
		t.Fatalf("unexpected default return: %+v", opt.Return)
	}
}

func TestRedisSearchBuilderQueryWildcardFallback(t *testing.T) {
	tests := []struct {
		name  string
		query string
		want  string
	}{
		{"empty query falls back to wildcard", "", "*"},
		{"whitespace query falls back to wildcard", "   ", "*"},
		{"explicit wildcard kept", "*", "*"},
		{"expression kept verbatim", "@city:{shanghai} @age:[20 30]", "@city:{shanghai} @age:[20 30]"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := &fakeRedisSearchClient{results: []redis.FTSearchResult{{}}}
			b := NewRedisSearchBuilder[RedisTestEntity](newRedisTestProxy(client), "idx:user")
			b.SetFilter(RedisFilter{Query: tt.query})
			b.SetSort(RedisSort{FieldName: "id", Asc: true})

			_, err := b.QueryList(context.Background())
			qbtest.AssertNoError(t, err)
			if client.calls[0].Query != tt.want {
				t.Fatalf("expected query %q, got %q", tt.want, client.calls[0].Query)
			}
		})
	}
}

func TestRedisSearchBuilderValidationErrors(t *testing.T) {
	tests := []struct {
		name  string
		build func(client *fakeRedisSearchClient) *RedisSearchBuilder[RedisTestEntity]
		want  error
	}{
		{
			name: "index not configured",
			build: func(c *fakeRedisSearchClient) *RedisSearchBuilder[RedisTestEntity] {
				return NewRedisSearchBuilder[RedisTestEntity](newRedisTestProxy(c), "")
			},
			want: ErrRedisIndexNotConfigured,
		},
		{
			name: "sort required",
			build: func(c *fakeRedisSearchClient) *RedisSearchBuilder[RedisTestEntity] {
				return NewRedisSearchBuilder[RedisTestEntity](newRedisTestProxy(c), "idx:user")
			},
			want: ErrRedisSortRequired,
		},
		{
			name: "sort field name required",
			build: func(c *fakeRedisSearchClient) *RedisSearchBuilder[RedisTestEntity] {
				b := NewRedisSearchBuilder[RedisTestEntity](newRedisTestProxy(c), "idx:user")
				b.SetSort(RedisSort{})
				return b
			},
			want: ErrRedisSortRequired,
		},
		{
			name: "sort direction conflict",
			build: func(c *fakeRedisSearchClient) *RedisSearchBuilder[RedisTestEntity] {
				b := NewRedisSearchBuilder[RedisTestEntity](newRedisTestProxy(c), "idx:user")
				b.SetSort(RedisSort{FieldName: "id", Asc: true, Desc: true})
				return b
			},
			want: ErrRedisSortDirectionInvalid,
		},
		{
			name: "multiple sort fields unsupported",
			build: func(c *fakeRedisSearchClient) *RedisSearchBuilder[RedisTestEntity] {
				b := NewRedisSearchBuilder[RedisTestEntity](newRedisTestProxy(c), "idx:user")
				b.SetSort(RedisSort{FieldName: "age"}, RedisSort{FieldName: "id"})
				return b
			},
			want: ErrRedisSortFieldUnsupported,
		},
		{
			name: "nil proxy",
			build: func(c *fakeRedisSearchClient) *RedisSearchBuilder[RedisTestEntity] {
				return NewRedisSearchBuilder[RedisTestEntity](nil, "idx:user")
			},
			want: ErrDataNotConfigured,
		},
		{
			name: "adapter not registered",
			build: func(c *fakeRedisSearchClient) *RedisSearchBuilder[RedisTestEntity] {
				return NewRedisSearchBuilder[RedisTestEntity](NewDBProxyWithAdapters(), "idx:user")
			},
			want: ErrDataNotConfigured,
		},
		{
			name: "nil redis client",
			build: func(c *fakeRedisSearchClient) *RedisSearchBuilder[RedisTestEntity] {
				return NewRedisSearchBuilder[RedisTestEntity](NewDBProxyWithAdapters(NewRedisAdapter(nil)), "idx:user")
			},
			want: ErrDataNotConfigured,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := &fakeRedisSearchClient{}
			_, err := tt.build(client).QueryList(context.Background())
			qbtest.AssertErrorIs(t, err, tt.want)
			if len(client.calls) != 0 {
				t.Fatalf("expected no redis call on validation failure, got %d", len(client.calls))
			}
		})
	}
}

func TestRedisSearchBuilderPaginationWindows(t *testing.T) {
	tests := []struct {
		name       string
		configure  func(b *RedisSearchBuilder[RedisTestEntity])
		wantOffset int
		wantLimit  int
		wantCount  bool
		wantTotal  int64
	}{
		{
			name: "offset and limit window",
			configure: func(b *RedisSearchBuilder[RedisTestEntity]) {
				b.SetStart(5)
				b.SetLimit(2)
			},
			wantOffset: 5, wantLimit: 2, wantCount: true, wantTotal: 9,
		},
		{
			name: "pagination disabled requests full window",
			configure: func(b *RedisSearchBuilder[RedisTestEntity]) {
				b.SetNeedPagination(false)
			},
			wantOffset: 0, wantLimit: int(maxLimit), wantCount: true, wantTotal: 9,
		},
		{
			name: "zero limit falls back to default",
			configure: func(b *RedisSearchBuilder[RedisTestEntity]) {
				b.SetLimit(0)
			},
			wantOffset: 0, wantLimit: int(defaultLimit), wantCount: true, wantTotal: 9,
		},
		{
			name: "total disabled skips withcount and total",
			configure: func(b *RedisSearchBuilder[RedisTestEntity]) {
				b.SetNeedTotal(false)
			},
			wantOffset: 0, wantLimit: int(defaultLimit), wantCount: false, wantTotal: 0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := &fakeRedisSearchClient{results: []redis.FTSearchResult{{
				Total: 9,
				Docs:  []redis.Document{redisJSONDoc(`{"id":1,"name":"Alice"}`)},
			}}}
			b := NewRedisSearchBuilder[RedisTestEntity](newRedisTestProxy(client), "idx:user")
			b.SetSort(RedisSort{FieldName: "id", Asc: true})
			tt.configure(b)

			result, err := b.QueryList(context.Background())
			qbtest.AssertNoError(t, err)
			if result.Total != tt.wantTotal {
				t.Fatalf("expected total %d, got %d", tt.wantTotal, result.Total)
			}
			opt := client.calls[0].Options
			if opt.LimitOffset != tt.wantOffset || opt.Limit != tt.wantLimit {
				t.Fatalf("expected limit window [%d,%d], got [%d,%d]",
					tt.wantOffset, tt.wantLimit, opt.LimitOffset, opt.Limit)
			}
			if opt.SortByWithCount != tt.wantCount {
				t.Fatalf("expected SortByWithCount %v, got %v", tt.wantCount, opt.SortByWithCount)
			}
		})
	}
}

func TestRedisSearchBuilderProjectionAndDecoding(t *testing.T) {
	fullDoc := `{"id":1,"name":"Alice"}`
	tests := []struct {
		name        string
		configure   func(b *RedisSearchBuilder[RedisTestEntity])
		fields      map[string]string
		want        *RedisTestEntity
		wantReturns []redis.FTSearchReturn
	}{
		{
			name:        "default full document projection",
			configure:   func(b *RedisSearchBuilder[RedisTestEntity]) {},
			fields:      map[string]string{"json": fullDoc},
			want:        &RedisTestEntity{ID: 1, Name: "Alice"},
			wantReturns: []redis.FTSearchReturn{{FieldName: "$", As: redisJSONReturnAlias}},
		},
		{
			name: "custom full document alias",
			configure: func(b *RedisSearchBuilder[RedisTestEntity]) {
				b.SetReturn(RedisReturn{Path: "$", As: "doc"})
			},
			fields:      map[string]string{"doc": fullDoc},
			want:        &RedisTestEntity{ID: 1, Name: "Alice"},
			wantReturns: []redis.FTSearchReturn{{FieldName: "$", As: "doc"}},
		},
		{
			name: "no projection falls back to raw dollar key",
			configure: func(b *RedisSearchBuilder[RedisTestEntity]) {
				b.SetReturn(RedisReturn{Path: ""})
			},
			fields:      map[string]string{"$": fullDoc},
			want:        &RedisTestEntity{ID: 1, Name: "Alice"},
			wantReturns: nil,
		},
		{
			name: "SetFields plain field gains json path prefix",
			configure: func(b *RedisSearchBuilder[RedisTestEntity]) {
				b.SetFields("name")
			},
			fields:      map[string]string{"name": "Alice"},
			want:        &RedisTestEntity{Name: "Alice"},
			wantReturns: []redis.FTSearchReturn{{FieldName: "$.name", As: "name"}},
		},
		{
			name: "SetFields nested path derives alias from last segment",
			configure: func(b *RedisSearchBuilder[RedisTestEntity]) {
				b.SetFields("profile.age")
			},
			fields:      map[string]string{"age": "40"},
			want:        &RedisTestEntity{Age: 40},
			wantReturns: []redis.FTSearchReturn{{FieldName: "$.profile.age", As: "age"}},
		},
		{
			name: "SetReturn overrides SetFields",
			configure: func(b *RedisSearchBuilder[RedisTestEntity]) {
				b.SetFields("name")
				b.SetReturn(RedisReturn{Path: "$.age", As: "age"})
			},
			fields:      map[string]string{"age": "30"},
			want:        &RedisTestEntity{Age: 30},
			wantReturns: []redis.FTSearchReturn{{FieldName: "$.age", As: "age"}},
		},
		{
			name: "SetReturn plain path gains json path prefix",
			configure: func(b *RedisSearchBuilder[RedisTestEntity]) {
				b.SetReturn(RedisReturn{Path: "name"})
			},
			fields:      map[string]string{"name": "Alice"},
			want:        &RedisTestEntity{Name: "Alice"},
			wantReturns: []redis.FTSearchReturn{{FieldName: "$.name", As: "name"}},
		},
		{
			name: "SetReturn custom alias keeps complex value",
			configure: func(b *RedisSearchBuilder[RedisTestEntity]) {
				b.SetReturn(RedisReturn{Path: "$.profile", As: "profile"})
			},
			fields:      map[string]string{"profile": `{"age":40}`},
			want:        &RedisTestEntity{Profile: RedisTestProfile{Age: 40}},
			wantReturns: []redis.FTSearchReturn{{FieldName: "$.profile", As: "profile"}},
		},
		{
			name: "best effort scalar and complex values",
			configure: func(b *RedisSearchBuilder[RedisTestEntity]) {
				b.SetReturn(
					RedisReturn{Path: "$.name", As: "name"},
					RedisReturn{Path: "$.age", As: "age"},
					RedisReturn{Path: "$.price", As: "price"},
					RedisReturn{Path: "$.active", As: "active"},
					RedisReturn{Path: "$.tags", As: "tags"},
					RedisReturn{Path: "$.nullable", As: "nullable"},
				)
			},
			fields: map[string]string{
				"name":     "Alice",
				"age":      "30",
				"price":    "12.5",
				"active":   "true",
				"tags":     `["go","redis"]`,
				"nullable": "null",
			},
			want: &RedisTestEntity{
				Name: "Alice", Age: 30, Price: 12.5, Active: true, Tags: []string{"go", "redis"},
			},
			wantReturns: []redis.FTSearchReturn{
				{FieldName: "$.name", As: "name"},
				{FieldName: "$.age", As: "age"},
				{FieldName: "$.price", As: "price"},
				{FieldName: "$.active", As: "active"},
				{FieldName: "$.tags", As: "tags"},
				{FieldName: "$.nullable", As: "nullable"},
			},
		},
		{
			name: "missing projected field keeps zero value",
			configure: func(b *RedisSearchBuilder[RedisTestEntity]) {
				b.SetFields("name", "age")
			},
			fields:      map[string]string{"name": "Alice"},
			want:        &RedisTestEntity{Name: "Alice"},
			wantReturns: []redis.FTSearchReturn{{FieldName: "$.name", As: "name"}, {FieldName: "$.age", As: "age"}},
		},
		{
			name: "empty path entry dropped",
			configure: func(b *RedisSearchBuilder[RedisTestEntity]) {
				b.SetReturn(RedisReturn{As: "ignored"}, RedisReturn{Path: "$.name"})
			},
			fields:      map[string]string{"name": "Alice"},
			want:        &RedisTestEntity{Name: "Alice"},
			wantReturns: []redis.FTSearchReturn{{FieldName: "$.name", As: "name"}},
		},
		{
			name: "SetFields reset falls back to full document",
			configure: func(b *RedisSearchBuilder[RedisTestEntity]) {
				b.SetFields("name")
				b.SetFields()
			},
			fields:      map[string]string{"json": fullDoc},
			want:        &RedisTestEntity{ID: 1, Name: "Alice"},
			wantReturns: []redis.FTSearchReturn{{FieldName: "$", As: redisJSONReturnAlias}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := &fakeRedisSearchClient{results: []redis.FTSearchResult{{
				Total: 1,
				Docs:  []redis.Document{{Fields: tt.fields}},
			}}}
			b := NewRedisSearchBuilder[RedisTestEntity](newRedisTestProxy(client), "idx:user")
			tt.configure(b)
			b.SetSort(RedisSort{FieldName: "id", Asc: true})

			result, err := b.QueryList(context.Background())
			qbtest.AssertNoError(t, err)
			qbtest.AssertListResult(t, result, []*RedisTestEntity{tt.want}, 1)
			if !reflect.DeepEqual(client.calls[0].Options.Return, tt.wantReturns) {
				t.Fatalf("expected return %+v, got %+v", tt.wantReturns, client.calls[0].Options.Return)
			}
		})
	}
}

// ---------- 投影别名边界 ----------

func TestRedisSearchBuilderPartialReturnAliasNamedJSON(t *testing.T) {
	client := &fakeRedisSearchClient{results: []redis.FTSearchResult{{
		Docs: []redis.Document{{Fields: map[string]string{"json": "Alice"}}},
	}}}
	b := NewRedisSearchBuilder[RedisJSONAliasEntity](newRedisTestProxy(client), "idx:user")
	b.SetReturn(RedisReturn{Path: "$.name", As: "json"})
	b.SetSort(RedisSort{FieldName: "id", Asc: true})

	result, err := b.QueryList(context.Background())
	qbtest.AssertNoError(t, err)
	qbtest.AssertListResult(t, result, []*RedisJSONAliasEntity{{JSON: "Alice"}}, 0)
}

// ---------- HASH 文档 ----------

func TestRedisSearchBuilderHashDocuments(t *testing.T) {
	t.Run("field projection keeps hash field names", func(t *testing.T) {
		client := &fakeRedisSearchClient{results: []redis.FTSearchResult{{
			Total: 1,
			Docs:  []redis.Document{{Fields: map[string]string{"name": "Alice", "age": "30"}}},
		}}}
		b := NewRedisSearchBuilder[RedisTestEntity](newRedisTestProxy(client), "idx:hash")
		b.SetDocumentKind(RedisHash)
		b.SetFields("name", "age")
		b.SetSort(RedisSort{FieldName: "id", Asc: true})

		result, err := b.QueryList(context.Background())
		qbtest.AssertNoError(t, err)
		qbtest.AssertListResult(t, result, []*RedisTestEntity{{Name: "Alice", Age: 30}}, 1)
		if !reflect.DeepEqual(client.calls[0].Options.Return, []redis.FTSearchReturn{
			{FieldName: "name", As: "name"},
			{FieldName: "age", As: "age"},
		}) {
			t.Fatalf("unexpected hash return: %+v", client.calls[0].Options.Return)
		}
	})

	t.Run("no projection returns all hash fields best effort", func(t *testing.T) {
		client := &fakeRedisSearchClient{results: []redis.FTSearchResult{{
			Docs: []redis.Document{{Fields: map[string]string{"id": "1", "name": "Alice", "active": "true"}}},
		}}}
		b := NewRedisSearchBuilder[RedisTestEntity](newRedisTestProxy(client), "idx:hash")
		b.SetDocumentKind(RedisHash)
		b.SetSort(RedisSort{FieldName: "id", Asc: true})

		result, err := b.QueryList(context.Background())
		qbtest.AssertNoError(t, err)
		qbtest.AssertListResult(t, result, []*RedisTestEntity{{ID: 1, Name: "Alice", Active: true}}, 0)
		if client.calls[0].Options.Return != nil {
			t.Fatalf("expected no RETURN clause for hash without projection, got %+v", client.calls[0].Options.Return)
		}
	})
}

// ---------- 错误传播 ----------

func TestRedisSearchBuilderDecodeAndClientErrors(t *testing.T) {
	ctx := context.Background()

	t.Run("document level error propagates with document id", func(t *testing.T) {
		docErr := errors.New("document expired")
		client := &fakeRedisSearchClient{results: []redis.FTSearchResult{{
			Docs: []redis.Document{{ID: "user:1", Error: docErr}},
		}}}
		b := NewRedisSearchBuilder[RedisTestEntity](newRedisTestProxy(client), "idx:user")
		b.SetSort(RedisSort{FieldName: "id", Asc: true})

		_, err := b.QueryList(ctx)
		if !errors.Is(err, docErr) {
			t.Fatalf("expected document error, got %v", err)
		}
		if !strings.Contains(err.Error(), "user:1") {
			t.Fatalf("expected document id in error, got %v", err)
		}
	})

	t.Run("invalid full document json fails unmarshal", func(t *testing.T) {
		client := &fakeRedisSearchClient{results: []redis.FTSearchResult{{
			Docs: []redis.Document{redisJSONDoc(`{"id":`)},
		}}}
		b := NewRedisSearchBuilder[RedisTestEntity](newRedisTestProxy(client), "idx:user")
		b.SetSort(RedisSort{FieldName: "id", Asc: true})

		_, err := b.QueryList(ctx)
		if err == nil || !strings.Contains(err.Error(), "unmarshal document") {
			t.Fatalf("expected unmarshal document error, got %v", err)
		}
	})

	t.Run("hash partial type mismatch fails unmarshal", func(t *testing.T) {
		client := &fakeRedisSearchClient{results: []redis.FTSearchResult{{
			Docs: []redis.Document{{Fields: map[string]string{"age": "not-a-number"}}},
		}}}
		b := NewRedisSearchBuilder[RedisTestEntity](newRedisTestProxy(client), "idx:hash")
		b.SetDocumentKind(RedisHash)
		b.SetFields("age")
		b.SetSort(RedisSort{FieldName: "id", Asc: true})

		_, err := b.QueryList(ctx)
		if err == nil || !strings.Contains(err.Error(), "unmarshal partial document") {
			t.Fatalf("expected unmarshal partial document error, got %v", err)
		}
	})

	t.Run("client error propagates", func(t *testing.T) {
		clientErr := errors.New("connection refused")
		client := &fakeRedisSearchClient{err: clientErr}
		b := NewRedisSearchBuilder[RedisTestEntity](newRedisTestProxy(client), "idx:user")
		b.SetSort(RedisSort{FieldName: "id", Asc: true})

		_, err := b.QueryList(ctx)
		qbtest.AssertErrorIs(t, err, clientErr)
	})
}

// ---------- Clone 与防御性拷贝 ----------

func TestRedisSearchBuilderCloneIsolation(t *testing.T) {
	client := &fakeRedisSearchClient{}
	original := NewRedisSearchBuilder[RedisTestEntity](newRedisTestProxy(client), "idx:original")
	original.SetDocumentKind(RedisHash)
	original.SetLimit(7)
	sorts := []RedisSort{{FieldName: "created_at", Desc: true}}
	returns := []RedisReturn{{Path: "$.name", As: "name"}}
	original.SetFilter(RedisFilter{Query: "@status:{active}"})
	original.SetSort(sorts...)
	original.SetReturn(returns...)

	// Set* 均为拷贝语义：调用方事后修改入参切片不应影响已设置的配置
	sorts[0].FieldName = "mutated"
	returns[0].As = "mutated"

	cloned := original.Clone()
	cloned.SetIndex("idx:clone")
	cloned.SetFilter(RedisFilter{Query: "@status:{inactive}"})
	cloned.SetSort(RedisSort{FieldName: "age", Asc: true})
	cloned.SetReturn(RedisReturn{Path: "$.age", As: "age"})
	cloned.SetDocumentKind(RedisJSON)
	cloned.SetLimit(9)

	if original.index != "idx:original" || original.documentKind != RedisHash || original.builder.limit != 7 {
		t.Fatalf("original state mutated by clone: index=%q kind=%v limit=%d",
			original.index, original.documentKind, original.builder.limit)
	}
	if original.filter.Query != "@status:{active}" {
		t.Fatalf("expected original filter preserved, got %q", original.filter.Query)
	}
	if len(original.sort) != 1 || original.sort[0].FieldName != "created_at" {
		t.Fatalf("expected original sort preserved, got %+v", original.sort)
	}
	if len(original.returns) != 1 || original.returns[0].As != "name" {
		t.Fatalf("expected original returns preserved, got %+v", original.returns)
	}
	if cloned.index != "idx:clone" || cloned.documentKind != RedisJSON || cloned.builder.limit != 9 {
		t.Fatalf("unexpected cloned state: index=%q kind=%v limit=%d",
			cloned.index, cloned.documentKind, cloned.builder.limit)
	}
	if cloned.filter.Query != "@status:{inactive}" || len(cloned.sort) != 1 || cloned.sort[0].FieldName != "age" {
		t.Fatalf("unexpected cloned config: filter=%q sort=%+v", cloned.filter.Query, cloned.sort)
	}

	// 克隆分叉后原实例仍可独立执行查询
	_, err := original.QueryList(context.Background())
	qbtest.AssertNoError(t, err)
	if len(client.calls) != 1 || client.calls[0].Query != "@status:{active}" {
		t.Fatalf("unexpected original query after clone: %+v", client.calls)
	}
	if !reflect.DeepEqual(client.calls[0].Options.SortBy, []redis.FTSearchSortBy{{FieldName: "created_at", Desc: true}}) {
		t.Fatalf("unexpected original sort after clone: %+v", client.calls[0].Options.SortBy)
	}
}

func TestRedisSearchBuilderFilterDefensiveCopy(t *testing.T) {
	client := &fakeRedisSearchClient{results: []redis.FTSearchResult{{}}}
	filter := RedisFilter{
		Query:          "@status:{active}",
		NumericFilters: []redis.FTSearchFilter{{FieldName: "age", Min: 20, Max: 40}},
		InKeys:         []any{"user:1"},
		InFields:       []any{"name"},
		Params:         map[string]any{"tenant": "acme"},
	}
	b := NewRedisSearchBuilder[RedisTestEntity](newRedisTestProxy(client), "idx:user")
	b.SetFilter(filter)
	b.SetSort(RedisSort{FieldName: "id", Asc: true})

	filter.Query = "@mutated:{all}"
	filter.NumericFilters[0].Min = 999
	filter.InKeys[0] = "mutated"
	filter.InFields[0] = "mutated"
	filter.Params["tenant"] = "mutated"

	_, err := b.QueryList(context.Background())
	qbtest.AssertNoError(t, err)
	call := client.calls[0]
	if call.Query != "@status:{active}" {
		t.Fatalf("expected original query, got %q", call.Query)
	}
	opt := call.Options
	if opt.Filters[0].Min != 20 {
		t.Fatalf("expected numeric filter isolated, got %+v", opt.Filters)
	}
	if opt.InKeys[0] != "user:1" || opt.InFields[0] != "name" {
		t.Fatalf("expected key/field filters isolated, got %+v %+v", opt.InKeys, opt.InFields)
	}
	if opt.Params["tenant"] != "acme" {
		t.Fatalf("expected params isolated, got %+v", opt.Params)
	}
}

// ---------- 占位接口行为 ----------

func TestRedisSearchBuilderNotImplementedPlaceholders(t *testing.T) {
	ctx := context.Background()
	client := &fakeRedisSearchClient{}
	b := NewRedisSearchBuilder[RedisTestEntity](newRedisTestProxy(client), "idx:user")
	b.SetSort(RedisSort{FieldName: "id", Asc: true})

	explain, err := b.Explain(ctx)
	qbtest.AssertNoError(t, err)
	if explain != "" {
		t.Fatalf("expected empty explain placeholder, got %q", explain)
	}

	page, err := b.QueryPage(ctx)
	qbtest.AssertNoError(t, err)
	qbtest.AssertCursorPageResult(t, page, nil, 0, false, nil)

	iterated := 0
	for item, err := range b.QueryCursor(ctx) {
		if err != nil {
			t.Fatalf("unexpected cursor error: %v", err)
		}
		_ = item
		iterated++
	}
	if iterated != 0 {
		t.Fatalf("expected no items from cursor placeholder, got %d", iterated)
	}

	// SetTotalLimit 在该后端为 no-op：total 不被截断
	countClient := &fakeRedisSearchClient{results: []redis.FTSearchResult{{Total: 9}}}
	capped := NewRedisSearchBuilder[RedisTestEntity](newRedisTestProxy(countClient), "idx:user")
	capped.SetSort(RedisSort{FieldName: "id", Asc: true})
	capped.SetTotalLimit(5)
	result, err := capped.QueryList(ctx)
	qbtest.AssertNoError(t, err)
	if result.Total != 9 {
		t.Fatalf("expected totalLimit no-op, got total %d", result.Total)
	}

	if len(client.calls) != 0 {
		t.Fatalf("expected placeholders to skip the client, got %d calls", len(client.calls))
	}
}

// ---------- 门面集成 ----------

func TestRedisSearchBuilderFacadeListQuery(t *testing.T) {
	ctx := context.Background()

	t.Run("scope and options wired into builder", func(t *testing.T) {
		client := &fakeRedisSearchClient{results: []redis.FTSearchResult{{
			Total: 9,
			Docs:  []redis.Document{redisJSONDoc(`{"id":1,"name":"Alice"}`)},
		}}}
		list := NewListWithData[RedisTestEntity](RedisSearch, newRedisTestProxy(client))
		list.SetScope(NewRedisSearchScope[RedisTestEntity](
			RedisFilter{Query: "@status:{active}"},
			RedisSort{FieldName: "id", Asc: true},
		))

		var middlewareCalled, afterCalled bool
		list.Use(func(
			ctx context.Context,
			querier Querier[RedisTestEntity],
			next func(context.Context) (core.Result[RedisTestEntity], error),
		) (core.Result[RedisTestEntity], error) {
			middlewareCalled = true
			meta := querier.GetQueryMeta()
			if meta.DataSource != RedisSearch || meta.Limit != 3 {
				t.Fatalf("unexpected middleware meta: %+v", meta)
			}
			return next(ctx)
		})
		list.SetAfterQueryHook(func(ctx context.Context, result core.Result[RedisTestEntity], err error) {
			if err != nil {
				t.Fatalf("unexpected after hook error: %v", err)
			}
			afterCalled = result.GetTotal() == 9
		})

		result, err := list.Query(ctx, WithRedisIndex("idx:users"), WithStart(0), WithLimit(3))
		qbtest.AssertNoError(t, err)
		qbtest.AssertListResult(t, result, []*RedisTestEntity{{ID: 1, Name: "Alice"}}, 9)
		if !middlewareCalled || !afterCalled {
			t.Fatalf("expected middleware=%v after hook=%v", middlewareCalled, afterCalled)
		}

		if len(client.calls) != 1 {
			t.Fatalf("expected 1 redis call, got %d", len(client.calls))
		}
		call := client.calls[0]
		if call.Index != "idx:users" || call.Query != "@status:{active}" {
			t.Fatalf("unexpected redis call: %+v", call)
		}
		if call.Options.LimitOffset != 0 || call.Options.Limit != 3 {
			t.Fatalf("unexpected limit window: %+v", call.Options)
		}
		if !reflect.DeepEqual(call.Options.SortBy, []redis.FTSearchSortBy{{FieldName: "id", Asc: true}}) {
			t.Fatalf("unexpected sort: %+v", call.Options.SortBy)
		}
	})

	t.Run("missing index option rejected before query", func(t *testing.T) {
		client := &fakeRedisSearchClient{}
		list := NewListWithData[RedisTestEntity](RedisSearch, newRedisTestProxy(client))
		list.SetScope(NewRedisSearchScope[RedisTestEntity](
			RedisFilter{},
			RedisSort{FieldName: "id", Asc: true},
		))
		_, err := list.Query(ctx)
		qbtest.AssertErrorIs(t, err, ErrRedisIndexNotConfigured)
		if len(client.calls) != 0 {
			t.Fatalf("expected no redis call, got %d", len(client.calls))
		}
	})

	t.Run("zero value scope filter falls back to wildcard", func(t *testing.T) {
		client := &fakeRedisSearchClient{results: []redis.FTSearchResult{{}}}
		list := NewListWithData[RedisTestEntity](RedisSearch, newRedisTestProxy(client))
		list.SetScope(NewRedisSearchScope[RedisTestEntity](
			RedisFilter{},
			RedisSort{FieldName: "id", Asc: true},
		))
		_, err := list.Query(ctx, WithRedisIndex("idx:users"))
		qbtest.AssertNoError(t, err)
		if client.calls[0].Query != "*" {
			t.Fatalf("expected wildcard query, got %q", client.calls[0].Query)
		}
	})

	t.Run("NewBuilder factory returns redis search builder", func(t *testing.T) {
		querier := NewBuilder[RedisTestEntity](RedisSearch, newRedisTestProxy(&fakeRedisSearchClient{}))
		if _, ok := querier.(*RedisSearchBuilder[RedisTestEntity]); !ok {
			t.Fatalf("expected *RedisSearchBuilder, got %T", querier)
		}
	})
}
