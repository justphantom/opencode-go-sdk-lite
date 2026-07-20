package opencode

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestLocationQuery(t *testing.T) {
	q := locationQuery(&LocationRef{Directory: "/a", WorkspaceID: "wrk_1"})
	if got := q.Get("location[directory]"); got != "/a" {
		t.Errorf("directory = %q", got)
	}
	if got := q.Get("location[workspace]"); got != "wrk_1" {
		t.Errorf("workspace = %q", got)
	}
	if q2 := locationQuery(nil); len(q2) != 0 {
		t.Errorf("nil query = %v", q2)
	}
}

func TestListModels(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/model" || r.Method != "GET" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
		if got := r.URL.Query().Get("location[directory]"); got != "/repo" {
			t.Errorf("directory query = %q", got)
		}
		_, _ = w.Write([]byte(`{"location":{"directory":"/repo"},"data":[
			{"id":"claude-sonnet","providerID":"anthropic","name":"Sonnet","api":{"model":"x"},
			 "capabilities":{"tools":true,"input":["text"],"output":["text"]},
			 "request":{"headers":{},"body":{}},"variants":[],"time":{"released":1},
			 "cost":[],"status":"active","enabled":true,"limit":{"context":200000,"output":8192}}
		]}`))
	}))
	defer srv.Close()

	c, _ := New(srv.URL)
	ms, err := c.ListModels(context.Background(), &LocationRef{Directory: "/repo"})
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	if len(ms) != 1 || ms[0].ID != "claude-sonnet" {
		t.Fatalf("models = %+v", ms)
	}
	if !ms[0].Capabilities.Tools || ms[0].Limit.Context != 200000 {
		t.Errorf("model = %+v", ms[0])
	}
	if len(ms[0].Capabilities.Output) != 1 || ms[0].Capabilities.Output[0] != "text" {
		t.Errorf("output caps = %v", ms[0].Capabilities.Output)
	}
}

func TestListProviders(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/provider" {
			t.Errorf("path = %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"data":[{"id":"anthropic","name":"Anthropic","api":{},"request":{}}]}`))
	}))
	defer srv.Close()

	c, _ := New(srv.URL)
	ps, err := c.ListProviders(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListProviders: %v", err)
	}
	if len(ps) != 1 || ps[0].ID != "anthropic" {
		t.Errorf("providers = %+v", ps)
	}
}

func TestGetProvider(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/provider/anthropic" {
			t.Errorf("path = %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"data":{"id":"anthropic","name":"Anthropic","api":{},"request":{}}}`))
	}))
	defer srv.Close()

	c, _ := New(srv.URL)
	p, err := c.GetProvider(context.Background(), "anthropic", nil)
	if err != nil {
		t.Fatalf("GetProvider: %v", err)
	}
	if p.ID != "anthropic" {
		t.Errorf("provider = %+v", p)
	}
}

func TestReplyPermission_validates(t *testing.T) {
	c, _ := New("http://x")
	if err := c.ReplyPermission(context.Background(), "ses_1", "per_1", "bogus", ""); err == nil {
		t.Fatal("expected error for invalid reply")
	}
}
