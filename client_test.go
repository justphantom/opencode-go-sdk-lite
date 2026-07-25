package opencode

import (
	"context"
	"net/http"
	"net/url"
	"testing"
)

// TestNewRequest_PreservesBaseURLPathPrefix: H5 回归——baseURL 带路径前缀时
// （反代场景如 http://host/v1），newRequest 必须保留前缀，不再用 ResolveReference
// 把绝对 path 整段覆盖 baseURL.Path。
func TestNewRequest_PreservesBaseURLPathPrefix(t *testing.T) {
	tests := []struct {
		name    string
		baseURL string
		path    string
		want    string
	}{
		{"no prefix", "http://127.0.0.1:4096", "/session/ses_1", "http://127.0.0.1:4096/session/ses_1"},
		{"trailing slash", "http://127.0.0.1:4096/", "/session/ses_1", "http://127.0.0.1:4096/session/ses_1"},
		{"with prefix", "http://127.0.0.1:4096/v1", "/session/ses_1", "http://127.0.0.1:4096/v1/session/ses_1"},
		{"with prefix trailing slash", "http://127.0.0.1:4096/v1/", "/session/ses_1", "http://127.0.0.1:4096/v1/session/ses_1"},
		{"root path", "http://127.0.0.1:4096/v1", "/event", "http://127.0.0.1:4096/v1/event"},
		{"global health", "http://127.0.0.1:4096", "/global/health", "http://127.0.0.1:4096/global/health"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, err := New(tt.baseURL)
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			req, err := c.newRequest(context.Background(), http.MethodGet, tt.path, nil, nil)
			if err != nil {
				t.Fatalf("newRequest: %v", err)
			}
			if got := req.URL.String(); got != tt.want {
				t.Errorf("URL = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestNewRequest_QueryMerged: query 参数正确合并（不破坏 H5 修复）。
func TestNewRequest_QueryMerged(t *testing.T) {
	c, _ := New("http://127.0.0.1:4096/v1")
	values := url.Values{}
	values.Set("directory", "/tmp")
	req, err := c.newRequest(context.Background(), http.MethodGet, "/session/ses_1", values, nil)
	if err != nil {
		t.Fatalf("newRequest: %v", err)
	}
	if got := req.URL.Query().Get("directory"); got != "/tmp" {
		t.Errorf("query directory = %q, want /tmp", got)
	}
	if got := req.URL.Path; got != "/v1/session/ses_1" {
		t.Errorf("path = %q, want /v1/session/ses_1", got)
	}
}
