package opencode

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestListSkills(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/skill" || r.Method != "GET" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
		if got := r.URL.Query().Get("directory"); got != "/repo" {
			t.Errorf("directory = %q", got)
		}
		_, _ = w.Write([]byte(`[
			{"name":"init","description":"guided setup","location":"<built-in>","content":"<!-- body -->"}
		]`))
	}))
	defer srv.Close()

	c, _ := New(srv.URL)
	skills, err := c.ListSkills(context.Background(), &LocationRef{Directory: "/repo"})
	if err != nil {
		t.Fatalf("ListSkills: %v", err)
	}
	if len(skills) != 1 {
		t.Fatalf("skills = %+v", skills)
	}
	s := skills[0]
	if s.Name != "init" || s.Description != "guided setup" || s.Location != "<built-in>" || s.Content == "" {
		t.Errorf("skill = %+v", s)
	}
}
