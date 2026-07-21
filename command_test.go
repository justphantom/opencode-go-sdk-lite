package opencode

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestListCommands(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/command" || r.Method != "GET" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
		if got := r.URL.Query().Get("directory"); got != "/repo" {
			t.Errorf("directory = %q", got)
		}
		_, _ = w.Write([]byte(`[
			{"name":"init","description":"setup","source":"command","template":"Create AGENTS.md $ARGUMENTS","hints":["path"]},
			{"name":"customize-opencode","source":"skill","template":"...","subtask":true,"hints":[]}
		]`))
	}))
	defer srv.Close()

	c, _ := New(srv.URL)
	cmds, err := c.ListCommands(context.Background(), &LocationRef{Directory: "/repo"})
	if err != nil {
		t.Fatalf("ListCommands: %v", err)
	}
	if len(cmds) != 2 {
		t.Fatalf("cmds = %+v", cmds)
	}
	if cmds[0].Name != "init" || cmds[0].Source != "command" || cmds[0].Template == "" || len(cmds[0].Hints) != 1 {
		t.Errorf("cmds[0] = %+v", cmds[0])
	}
	if cmds[1].Source != "skill" || !cmds[1].Subtask {
		t.Errorf("cmds[1] = %+v", cmds[1])
	}
}
