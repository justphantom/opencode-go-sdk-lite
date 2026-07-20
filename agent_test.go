package opencode

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestListAgents(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/agent" || r.Method != "GET" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
		if got := r.URL.Query().Get("location[directory]"); got != "/repo" {
			t.Errorf("location[directory] = %q", got)
		}
		_, _ = w.Write([]byte(`{"location":{"directory":"/repo"},"data":[
			{"id":"build","mode":"primary","hidden":false,"request":{"mode":"primary"},"permissions":[]},
			{"id":"explore","mode":"subagent","hidden":false,"request":{"mode":"subagent"},"permissions":["read"]}
		]}`))
	}))
	defer srv.Close()

	c, _ := New(srv.URL)
	agents, err := c.ListAgents(context.Background(), &LocationRef{Directory: "/repo"})
	if err != nil {
		t.Fatalf("ListAgents: %v", err)
	}
	if len(agents) != 2 {
		t.Fatalf("agents = %+v", agents)
	}
	if agents[0].ID != "build" || agents[0].Mode != "primary" || agents[0].Hidden {
		t.Errorf("agents[0] = %+v", agents[0])
	}
	if agents[1].ID != "explore" || agents[1].Mode != "subagent" {
		t.Errorf("agents[1] = %+v", agents[1])
	}
	// RawMessage 字段应原样保留
	if len(agents[0].Request) == 0 {
		t.Errorf("request empty")
	}
}
