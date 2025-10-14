package elastic

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// Query 定义查询接口
// 与官方客户端的接口保持一致，便于后续替换为真实依赖
// Source 返回可序列化的查询结构
type Query interface {
	Source() (interface{}, error)
}

// Sorter 定义排序接口
// Source 返回排序配置
type Sorter interface {
	Source() (interface{}, error)
}

// ClientOption 定义客户端初始化选项
type ClientOption func(*Client) error

// Client 提供与 Elasticsearch 交互的最小实现
// 仅包含 QueryBuilder 所需的能力
type Client struct {
	baseURL    string
	httpClient *http.Client
}

// NewClient 创建客户端实例
func NewClient(opts ...ClientOption) (*Client, error) {
	client := &Client{
		httpClient: http.DefaultClient,
	}

	for _, opt := range opts {
		if err := opt(client); err != nil {
			return nil, err
		}
	}

	if client.baseURL == "" {
		client.baseURL = "http://localhost:9200"
	}

	return client, nil
}

// SetURL 设置 Elasticsearch 服务地址
func SetURL(rawURLs ...string) ClientOption {
	return func(c *Client) error {
		if len(rawURLs) == 0 {
			return nil
		}
		c.baseURL = strings.TrimRight(rawURLs[0], "/")
		return nil
	}
}

// SetSniff 用于兼容真实客户端的 API，此处为空实现
func SetSniff(bool) ClientOption { return func(*Client) error { return nil } }

// SetHealthcheck 同上，为空实现
func SetHealthcheck(bool) ClientOption { return func(*Client) error { return nil } }

// SetHttpClient 设置自定义 HTTP 客户端
func SetHttpClient(hc *http.Client) ClientOption {
	return func(c *Client) error {
		if hc != nil {
			c.httpClient = hc
		}
		return nil
	}
}

// Search 创建搜索服务
func (c *Client) Search() *SearchService { return &SearchService{client: c} }

// SearchService 封装搜索参数
type SearchService struct {
	client  *Client
	index   string
	query   Query
	from    *int
	size    *int
	sorters []Sorter
}

// Index 指定索引
func (s *SearchService) Index(index string) *SearchService {
	s.index = index
	return s
}

// Query 指定查询条件
func (s *SearchService) Query(q Query) *SearchService {
	s.query = q
	return s
}

// From 指定起始位置
func (s *SearchService) From(from int) *SearchService {
	s.from = &from
	return s
}

// Size 指定查询条数
func (s *SearchService) Size(size int) *SearchService {
	s.size = &size
	return s
}

// SortBy 指定排序
func (s *SearchService) SortBy(sorters ...Sorter) *SearchService {
	s.sorters = append([]Sorter{}, sorters...)
	return s
}

// Do 执行查询
func (s *SearchService) Do(ctx context.Context) (*SearchResult, error) {
	if s.client == nil {
		return nil, fmt.Errorf("elastic: client is nil")
	}
	if s.index == "" {
		return nil, fmt.Errorf("elastic: index is empty")
	}

	endpoint := fmt.Sprintf("%s/%s/_search", s.client.baseURL, url.PathEscape(s.index))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}

	resp, err := s.client.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= http.StatusBadRequest {
		return nil, fmt.Errorf("elastic: unexpected status %d", resp.StatusCode)
	}

	var result SearchResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	return &result, nil
}

// SearchResult 搜索结果结构
type SearchResult struct {
	Hits *SearchHits `json:"hits"`
}

// SearchHits 命中结果
type SearchHits struct {
	TotalHits *TotalHits   `json:"total"`
	Hits      []*SearchHit `json:"hits"`
}

// TotalHits 命中总数
type TotalHits struct {
	Value int64 `json:"value"`
}

// SearchHit 单条命中记录
type SearchHit struct {
	Source json.RawMessage `json:"_source"`
}

// MatchAllQuery 匹配所有
type MatchAllQuery struct{}

// NewMatchAllQuery 创建匹配所有查询
func NewMatchAllQuery() *MatchAllQuery { return &MatchAllQuery{} }

// Source 返回查询结构
func (MatchAllQuery) Source() (interface{}, error) {
	return map[string]any{"match_all": map[string]any{}}, nil
}

// FieldSort 字段排序
type FieldSort struct {
	field string
	asc   bool
}

// NewFieldSort 创建字段排序
func NewFieldSort(field string) *FieldSort { return &FieldSort{field: field, asc: true} }

// Asc 设置升序
func (f *FieldSort) Asc() *FieldSort {
	f.asc = true
	return f
}

// Desc 设置降序
func (f *FieldSort) Desc() *FieldSort {
	f.asc = false
	return f
}

// Source 返回排序结构
func (f *FieldSort) Source() (interface{}, error) {
	order := "desc"
	if f.asc {
		order = "asc"
	}
	return map[string]any{f.field: map[string]string{"order": order}}, nil
}
