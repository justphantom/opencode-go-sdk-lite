package opencode

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestLocationQuery(t *testing.T) {
	q := locationQuery(&LocationRef{Directory: "/a", WorkspaceID: "wrk_1"})
	if got := q.Get("directory"); got != "/a" {
		t.Errorf("directory = %q", got)
	}
	if got := q.Get("workspace"); got != "wrk_1" {
		t.Errorf("workspace = %q", got)
	}
	if q2 := locationQuery(nil); len(q2) != 0 {
		t.Errorf("nil query = %v", q2)
	}
}

// providersFixture 是 GET /provider 的标准响应：两个 provider，三个模型。
// capabilities 结构对齐服务端实测（input/output 为模态对象，toolcall 键）。
const providersFixture = `{"all":[
	{"id":"anthropic","name":"Anthropic","source":"api","models":{
		"claude-sonnet":{"id":"claude-sonnet","providerID":"anthropic","name":"Sonnet",
			"api":{"id":"x","url":"u","npm":"n"},
			"capabilities":{"toolcall":true,"reasoning":true,"input":{"text":true,"image":true},"output":{"text":true}},
			"cost":{"input":3,"output":15,"cache":{"read":0.3,"write":3.75}},
			"limit":{"context":200000,"output":8192},"status":"active"},
		"claude-old":{"id":"claude-old","providerID":"anthropic","name":"Old",
			"api":{"id":"y","url":"u","npm":"n"},"capabilities":{"toolcall":false},
			"cost":{"input":1,"output":2,"cache":{"read":0,"write":0}},
			"limit":{"context":100000,"output":4096},"status":"deprecated"}
	}},
	{"id":"opencode","name":"O","source":"api","models":{
		"free":{"id":"free","providerID":"opencode","name":"F",
			"api":{"id":"z","url":"u","npm":"n"},"capabilities":{"toolcall":true},
			"cost":{"input":0,"output":0,"cache":{"read":0,"write":0}},
			"limit":{"context":32000,"output":4096},"status":"active"}
	}}
],"default":{"anthropic":"claude-sonnet"},"connected":["anthropic"]}`

func TestListModels(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/provider" || r.Method != "GET" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
		if got := r.URL.Query().Get("directory"); got != "/repo" {
			t.Errorf("directory query = %q", got)
		}
		_, _ = w.Write([]byte(providersFixture))
	}))
	defer srv.Close()

	c, _ := New(srv.URL)
	ms, err := c.ListModels(context.Background(), &LocationRef{Directory: "/repo"})
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	if len(ms) != 3 {
		t.Fatalf("models = %+v", ms)
	}
	byID := map[string]ModelInfo{}
	for _, m := range ms {
		byID[m.ProviderID+"/"+m.ID] = m
	}
	sonnet, ok := byID["anthropic/claude-sonnet"]
	if !ok {
		t.Fatalf("missing anthropic/claude-sonnet in %v", byID)
	}
	if !sonnet.Enabled || !sonnet.Capabilities.Toolcall || sonnet.Limit.Context != 200000 {
		t.Errorf("sonnet = %+v", sonnet)
	}
	if !sonnet.Capabilities.Input["text"] || !sonnet.Capabilities.Input["image"] {
		t.Errorf("input caps = %v", sonnet.Capabilities.Input)
	}
	if old := byID["anthropic/claude-old"]; old.Enabled || old.Status != "deprecated" {
		t.Errorf("old = %+v", old)
	}
}

func TestListProviders(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/provider" {
			t.Errorf("path = %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(providersFixture))
	}))
	defer srv.Close()

	c, _ := New(srv.URL)
	ps, err := c.ListProviders(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListProviders: %v", err)
	}
	if len(ps) != 2 || ps[0].ID != "anthropic" {
		t.Errorf("providers = %+v", ps)
	}
	if len(ps[0].Models) != 2 {
		t.Errorf("models = %+v", ps[0].Models)
	}
}

func TestListConnectedProviders(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/provider" || r.Method != "GET" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
		// Connected 不受 loc 影响，请求不应带 directory
		if got := r.URL.Query().Get("directory"); got != "" {
			t.Errorf("directory query = %q, want empty", got)
		}
		_, _ = w.Write([]byte(providersFixture))
	}))
	defer srv.Close()

	c, _ := New(srv.URL)
	connected, err := c.ListConnectedProviders(context.Background())
	if err != nil {
		t.Fatalf("ListConnectedProviders: %v", err)
	}
	if len(connected) != 1 || connected[0] != "anthropic" {
		t.Errorf("connected = %v, want [anthropic]", connected)
	}
}

func TestGetProvider(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/provider" {
			t.Errorf("path = %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(providersFixture))
	}))
	defer srv.Close()

	c, _ := New(srv.URL)
	p, err := c.GetProvider(context.Background(), "opencode", nil)
	if err != nil {
		t.Fatalf("GetProvider: %v", err)
	}
	if p.ID != "opencode" || len(p.Models) != 1 {
		t.Errorf("provider = %+v", p)
	}
	if _, err := c.GetProvider(context.Background(), "nope", nil); err == nil {
		t.Fatal("expected error for unknown provider")
	}
}

func TestReplyPermission_validates(t *testing.T) {
	c, _ := New("http://x")
	if err := c.ReplyPermission(context.Background(), "per_1", "/repo", "bogus", ""); err == nil {
		t.Fatal("expected error for invalid reply")
	}
}
