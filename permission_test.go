package opencode

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// ListPermissions 从全局 /permission 列表按 sessionID 过滤。
func TestListPermissions_filtersBySessionID(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/permission" || r.Method != "GET" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
		_, _ = w.Write([]byte(`[
			{"id":"per_1","sessionID":"ses_1","permission":"bash","patterns":["*"]},
			{"id":"per_2","sessionID":"ses_2","permission":"edit","patterns":["*.go"]}
		]`))
	}))
	defer srv.Close()

	c, _ := New(srv.URL)
	ps, err := c.ListPermissions(context.Background(), "ses_1")
	if err != nil {
		t.Fatalf("ListPermissions: %v", err)
	}
	if len(ps) != 1 || ps[0].ID != "per_1" || ps[0].Permission != "bash" {
		t.Errorf("permissions = %+v", ps)
	}
}

func TestReplyPermission(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/permission/per_1/reply" || r.Method != "POST" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		_, _ = w.Write([]byte(`true`))
	}))
	defer srv.Close()

	c, _ := New(srv.URL)
	if err := c.ReplyPermission(context.Background(), "per_1", PermissionReplyOnce, "ok"); err != nil {
		t.Fatalf("ReplyPermission: %v", err)
	}
	if gotBody["reply"] != "once" || gotBody["message"] != "ok" {
		t.Errorf("body = %v", gotBody)
	}
}
