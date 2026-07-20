package opencode

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// Client 是 opencode v1 HTTP API 的薄客户端。
type Client struct {
	baseURL    *url.URL
	httpClient *http.Client
	headers    map[string]string
}

// Option 配置 Client。
type Option func(*Client)

// WithToken 设置 Authorization: Bearer <token>。
func WithToken(token string) Option {
	return func(c *Client) { c.headers["Authorization"] = "Bearer " + token }
}

// WithHTTPClient 注入自定义 *http.Client。
func WithHTTPClient(h *http.Client) Option {
	return func(c *Client) { c.httpClient = h }
}

// WithHeader 追加/覆盖单个请求头。
func WithHeader(key, value string) Option {
	return func(c *Client) { c.headers[key] = value }
}

// WithUserAgent 便捷设置 User-Agent。
func WithUserAgent(ua string) Option {
	return func(c *Client) { c.headers["User-Agent"] = ua }
}

// New 创建 Client。baseURL 形如 "http://127.0.0.1:4096"。
func New(baseURL string, opts ...Option) (*Client, error) {
	u, err := url.Parse(strings.TrimRight(baseURL, "/"))
	if err != nil {
		return nil, fmt.Errorf("opencode: invalid baseURL: %w", err)
	}
	c := &Client{
		baseURL:    u,
		httpClient: http.DefaultClient,
		headers:    map[string]string{"Accept": "application/json"},
	}
	for _, opt := range opts {
		opt(c)
	}
	return c, nil
}

// doJSON 发送 JSON 请求并把响应解析到 out。status 为期望状态码，0 表示仅要求 2xx。
func (c *Client) doJSON(ctx context.Context, method, path string, query url.Values, body any, out any, status int) error {
	var reader io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("opencode: marshal body: %w", err)
		}
		reader = bytes.NewReader(buf)
	}
	req, err := c.newRequest(ctx, method, path, query, reader)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("opencode: http: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	return decodeJSON(resp, out, status)
}

// doEmpty 发送请求但无响应体，仅校验状态码。
func (c *Client) doEmpty(ctx context.Context, method, path string, query url.Values, body any, status int) error {
	var reader io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("opencode: marshal body: %w", err)
		}
		reader = bytes.NewReader(buf)
	}
	req, err := c.newRequest(ctx, method, path, query, reader)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("opencode: http: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if !statusOK(resp.StatusCode, status) {
		raw, _ := io.ReadAll(resp.Body)
		return parseAPIError(resp.StatusCode, raw)
	}
	return nil
}

// Health 检查服务端是否可用。GET /global/health，解析 {healthy:true}，
// 响应非 2xx 或 healthy != true 都视为不健康。
func (c *Client) Health(ctx context.Context) error {
	var body struct {
		Healthy bool `json:"healthy"`
	}
	if err := c.doJSON(ctx, http_GET, "/global/health", nil, nil, &body, 0); err != nil {
		return err
	}
	if !body.Healthy {
		return fmt.Errorf("opencode: health check reports unhealthy")
	}
	return nil
}

func (c *Client) newRequest(ctx context.Context, method, path string, query url.Values, body io.Reader) (*http.Request, error) {
	rel, err := url.Parse(path)
	if err != nil {
		return nil, fmt.Errorf("opencode: invalid path: %w", err)
	}
	if len(query) > 0 {
		rel.RawQuery = query.Encode()
	}
	u := c.baseURL.ResolveReference(rel)
	req, err := http.NewRequestWithContext(ctx, method, u.String(), body)
	if err != nil {
		return nil, err
	}
	for k, v := range c.headers {
		req.Header.Set(k, v)
	}
	return req, nil
}

func decodeJSON(resp *http.Response, out any, status int) error {
	if !statusOK(resp.StatusCode, status) {
		raw, _ := io.ReadAll(resp.Body)
		return parseAPIError(resp.StatusCode, raw)
	}
	if out == nil {
		return nil
	}
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("opencode: read body: %w", err)
	}
	if len(raw) == 0 {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("opencode: decode body: %w", err)
	}
	return nil
}

func statusOK(code, expect int) bool {
	if expect != 0 {
		return code == expect
	}
	return code >= 200 && code < 300
}
