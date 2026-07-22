package opencode

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// ListQuestions 从全局 /question 列表按 sessionID 过滤。
func TestListQuestions_filtersBySessionID(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/question" || r.Method != "GET" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
		_, _ = w.Write([]byte(`[
			{"id":"q_1","sessionID":"ses_1","questions":[{"question":"?","header":"h","options":[{"label":"a","description":"d"}]}]},
			{"id":"q_2","sessionID":"ses_2","questions":[{"question":"?2","header":"h2","options":[]}]}
		]`))
	}))
	defer srv.Close()

	c, _ := New(srv.URL)
	qs, err := c.ListQuestions(context.Background(), "ses_1")
	if err != nil {
		t.Fatalf("ListQuestions: %v", err)
	}
	if len(qs) != 1 || qs[0].ID != "q_1" {
		t.Fatalf("questions = %+v", qs)
	}
	if qs[0].Questions[0].Options[0].Label != "a" {
		t.Errorf("options = %+v", qs[0].Questions[0].Options)
	}
}

func TestReplyQuestion(t *testing.T) {
	var gotBody QuestionReply
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/question/q_1/reply" || r.Method != "POST" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
		if r.URL.Query().Get("directory") != "/repo" {
			t.Errorf("directory query = %q, want /repo", r.URL.Query().Get("directory"))
		}
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		_, _ = w.Write([]byte(`true`))
	}))
	defer srv.Close()

	c, _ := New(srv.URL)
	if err := c.ReplyQuestion(context.Background(), "q_1", "/repo", &QuestionReply{Answers: [][]string{{"a"}}}); err != nil {
		t.Fatalf("ReplyQuestion: %v", err)
	}
	if len(gotBody.Answers) != 1 || gotBody.Answers[0][0] != "a" {
		t.Errorf("body = %+v", gotBody)
	}
}

func TestRejectQuestion(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/question/q_1/reject" || r.Method != "POST" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
		if r.URL.Query().Get("directory") != "/repo" {
			t.Errorf("directory query = %q, want /repo", r.URL.Query().Get("directory"))
		}
		_, _ = w.Write([]byte(`true`))
	}))
	defer srv.Close()

	c, _ := New(srv.URL)
	if err := c.RejectQuestion(context.Background(), "q_1", "/repo"); err != nil {
		t.Fatalf("RejectQuestion: %v", err)
	}
}
