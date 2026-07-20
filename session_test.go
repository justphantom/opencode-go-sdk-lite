package opencode

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNew_invalidBaseURL(t *testing.T) {
	if _, err := New("://bad"); err == nil {
		t.Fatal("expected error for invalid baseURL")
	}
}

func TestNew_appliesOptions(t *testing.T) {
	c, err := New("http://x", WithToken("tok"), WithUserAgent("ua/1"), WithHeader("X", "y"))
	if err != nil {
		t.Fatal(err)
	}
	if got := c.headers["Authorization"]; got != "Bearer tok" {
		t.Errorf("Authorization = %q", got)
	}
	if got := c.headers["User-Agent"]; got != "ua/1" {
		t.Errorf("User-Agent = %q", got)
	}
	if got := c.headers["X"]; got != "y" {
		t.Errorf("X = %q", got)
	}
}

const sessionFixture = `{"id":"ses_1","projectID":"prj","directory":"/tmp","title":"t","agent":"build",
	"model":{"id":"m","providerID":"p"},"cost":0,
	"tokens":{"input":0,"output":0,"reasoning":0,"cache":{"read":0,"write":0}},
	"time":{"created":1,"updated":1}}`

func TestCreateSession(t *testing.T) {
	var gotBody CreateSessionReq
	var gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/session" || r.Method != "POST" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("Content-Type = %q", ct)
		}
		gotQuery = r.URL.Query().Get("directory")
		var b CreateSessionReq
		_ = json.NewDecoder(r.Body).Decode(&b)
		gotBody = b
		_, _ = w.Write([]byte(sessionFixture))
	}))
	defer srv.Close()

	c, _ := New(srv.URL, WithToken("tok"))
	got, err := c.CreateSession(context.Background(), &CreateSessionReq{Agent: "build", Directory: "/repo"})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if got.ID != "ses_1" || got.Directory != "/tmp" {
		t.Errorf("got = %+v", got)
	}
	if gotBody.Agent != "build" {
		t.Errorf("body agent = %q", gotBody.Agent)
	}
	if gotQuery != "/repo" {
		t.Errorf("directory query = %q", gotQuery)
	}
}

func TestPrompt_validates(t *testing.T) {
	c, _ := New("http://x")
	if _, err := c.Prompt(context.Background(), "ses_1", &PromptReq{}); err == nil {
		t.Fatal("expected error for empty parts")
	}
	if _, err := c.Prompt(context.Background(), "ses_1", nil); err == nil {
		t.Fatal("expected error for nil request")
	}
	// 显式 id 前缀不合法时拒绝
	if _, err := c.Prompt(context.Background(), "ses_1", &PromptReq{
		MessageID: "bad_1",
		Parts:     []PromptPart{{Type: "text", Text: "hi"}},
	}); err == nil {
		t.Fatal("expected error for bad messageID prefix")
	}
	if _, err := c.Prompt(context.Background(), "ses_1", &PromptReq{
		Parts: []PromptPart{{ID: "bad_1", Type: "text", Text: "hi"}},
	}); err == nil {
		t.Fatal("expected error for bad part id prefix")
	}
}

func TestPrompt_success(t *testing.T) {
	var gotBody struct {
		MessageID string          `json:"messageID"`
		Model     *PromptModelRef `json:"model"`
		Agent     string          `json:"agent"`
		Variant   string          `json:"variant"`
		Parts     []PromptPart    `json:"parts"`
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/session/ses_1/prompt_async" || r.Method != "POST" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(204)
	}))
	defer srv.Close()

	c, _ := New(srv.URL)
	ack, err := c.Prompt(context.Background(), "ses_1", &PromptReq{
		Agent: "build",
		Model: &ModelRef{ID: "m", ProviderID: "p", Variant: "high"},
		Parts: []PromptPart{{Type: "text", Text: "hello"}},
	})
	if err != nil {
		t.Fatalf("Prompt: %v", err)
	}
	// ack 回传 SDK 生成的 id
	if !strings.HasPrefix(ack.MessageID, "msg_") {
		t.Errorf("ack.MessageID = %q", ack.MessageID)
	}
	if len(ack.PartIDs) != 1 || !strings.HasPrefix(ack.PartIDs[0], "prt_") {
		t.Errorf("ack.PartIDs = %v", ack.PartIDs)
	}
	// wire body：id 已填充，model 键为 modelID
	if gotBody.MessageID != ack.MessageID {
		t.Errorf("wire messageID = %q, want %q", gotBody.MessageID, ack.MessageID)
	}
	if gotBody.Model == nil || gotBody.Model.ModelID != "m" || gotBody.Model.ProviderID != "p" {
		t.Errorf("wire model = %+v", gotBody.Model)
	}
	if gotBody.Variant != "high" || gotBody.Agent != "build" {
		t.Errorf("body = %+v", gotBody)
	}
	if len(gotBody.Parts) != 1 || gotBody.Parts[0].ID != ack.PartIDs[0] || gotBody.Parts[0].Text != "hello" {
		t.Errorf("wire parts = %+v", gotBody.Parts)
	}
}

func TestPrompt_respectsGivenIDs(t *testing.T) {
	var gotBody struct {
		MessageID string       `json:"messageID"`
		Parts     []PromptPart `json:"parts"`
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(204)
	}))
	defer srv.Close()

	c, _ := New(srv.URL)
	ack, err := c.Prompt(context.Background(), "ses_1", &PromptReq{
		MessageID: "msg_given",
		Parts:     []PromptPart{{ID: "prt_given", Type: "text", Text: "hi"}},
	})
	if err != nil {
		t.Fatalf("Prompt: %v", err)
	}
	if ack.MessageID != "msg_given" || ack.PartIDs[0] != "prt_given" {
		t.Errorf("ack = %+v", ack)
	}
	if gotBody.MessageID != "msg_given" || gotBody.Parts[0].ID != "prt_given" {
		t.Errorf("wire = %+v", gotBody)
	}
}

func TestInterrupt(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/session/ses_1/abort" || r.Method != "POST" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
		_, _ = w.Write([]byte(`true`))
	}))
	defer srv.Close()

	c, _ := New(srv.URL)
	if err := c.Interrupt(context.Background(), "ses_1"); err != nil {
		t.Fatalf("Interrupt: %v", err)
	}
}

func TestAPIError_non2xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
		_, _ = w.Write([]byte(`{"name":"NotFoundError","data":{"message":"not found"}}`))
	}))
	defer srv.Close()

	c, _ := New(srv.URL)
	_, err := c.GetSession(context.Background(), "ses_x")
	if err == nil {
		t.Fatal("expected error")
	}
	ae, ok := err.(*APIError)
	if !ok {
		t.Fatalf("err type = %T", err)
	}
	if ae.Status != 404 || ae.Type != "NotFoundError" {
		t.Errorf("ae = %+v", ae)
	}
	if !strings.Contains(ae.Error(), "404") {
		t.Errorf("Error() = %q", ae.Error())
	}
}

func TestListMessages(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/session/ses_1/message" {
			t.Errorf("path = %s", r.URL.Path)
		}
		if got := r.URL.Query().Get("limit"); got != "10" {
			t.Errorf("limit = %q", got)
		}
		_, _ = w.Write([]byte(`[
			{"info":{"id":"msg_1","sessionID":"ses_1","role":"user","time":{"created":1}},
			 "parts":[{"id":"prt_1","type":"text","text":"hi"}]},
			{"info":{"id":"msg_2","sessionID":"ses_1","role":"assistant","time":{"created":2}},
			 "parts":[{"id":"prt_2","type":"text","text":"hello"}]}
		]`))
	}))
	defer srv.Close()

	c, _ := New(srv.URL)
	msgs, err := c.ListMessages(context.Background(), "ses_1", &ListMessagesOpt{Limit: 10})
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	if len(msgs) != 2 {
		t.Fatalf("msgs = %+v", msgs)
	}
	if msgs[0].Info.ID != "msg_1" || msgs[0].Info.Role != "user" {
		t.Errorf("msgs[0].Info = %+v", msgs[0].Info)
	}
	if msgs[1].Info.Role != "assistant" || len(msgs[1].Parts) != 1 {
		t.Errorf("msgs[1] = %+v", msgs[1])
	}
}

func TestHealth_healthy(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/global/health" || r.Method != "GET" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"healthy":true,"version":"dev"}`))
	}))
	defer srv.Close()

	c, _ := New(srv.URL)
	if err := c.Health(context.Background()); err != nil {
		t.Fatalf("Health: %v", err)
	}
}

func TestHealth_unhealthy(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"healthy":false}`))
	}))
	defer srv.Close()

	c, _ := New(srv.URL)
	if err := c.Health(context.Background()); err == nil {
		t.Fatal("expected error for unhealthy")
	}
}

func TestHealth_serverError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
	}))
	defer srv.Close()

	c, _ := New(srv.URL)
	err := c.Health(context.Background())
	if err == nil {
		t.Fatal("expected error for 500")
	}
	ae, ok := err.(*APIError)
	if !ok || ae.Status != 500 {
		t.Fatalf("err = %+v (%T)", err, err)
	}
}

func TestDeleteSession(t *testing.T) {
	var gotPath, gotMethod string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotMethod = r.Method
		_, _ = w.Write([]byte(`true`))
	}))
	defer srv.Close()

	c, _ := New(srv.URL)
	if err := c.DeleteSession(context.Background(), "ses_1"); err != nil {
		t.Fatalf("DeleteSession: %v", err)
	}
	if gotMethod != "DELETE" || gotPath != "/session/ses_1" {
		t.Errorf("got %s %s, want DELETE /session/ses_1", gotMethod, gotPath)
	}
}

func TestDeleteSession_falseIsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`false`))
	}))
	defer srv.Close()

	c, _ := New(srv.URL)
	if err := c.DeleteSession(context.Background(), "ses_1"); err == nil {
		t.Fatal("expected error when server returns false")
	}
}

func TestListSessions(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/session" || r.Method != "GET" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
		if got := r.URL.Query().Get("directory"); got != "/repo" {
			t.Errorf("directory = %q", got)
		}
		_, _ = w.Write([]byte(`[` + sessionFixture + `]`))
	}))
	defer srv.Close()

	c, _ := New(srv.URL)
	ss, err := c.ListSessions(context.Background(), &ListSessionsOpt{Directory: "/repo"})
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(ss) != 1 || ss[0].ID != "ses_1" {
		t.Errorf("sessions = %+v", ss)
	}
}
