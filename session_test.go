package opencode

import (
	"context"
	"encoding/json"
	"io"
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

func TestCreateSession(t *testing.T) {
	var gotBody CreateSessionReq
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/session" || r.Method != "POST" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("Content-Type = %q", ct)
		}
		var b CreateSessionReq
		_ = json.NewDecoder(r.Body).Decode(&b)
		gotBody = b
		_, _ = w.Write([]byte(`{"data":{"id":"ses_1","projectID":"prj","cost":0,"tokens":{"input":0,"output":0,"reasoning":0,"cache":{"read":0,"write":0}},"time":{"created":1,"updated":1},"title":"t","location":{"directory":"/tmp"}}}`))
	}))
	defer srv.Close()

	c, _ := New(srv.URL, WithToken("tok"))
	got, err := c.CreateSession(context.Background(), &CreateSessionReq{Agent: "build"})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if got.ID != "ses_1" {
		t.Errorf("id = %q", got.ID)
	}
	if gotBody.Agent != "build" {
		t.Errorf("body agent = %q", gotBody.Agent)
	}
}

func TestPrompt_validatesText(t *testing.T) {
	c, _ := New("http://x")
	if _, err := c.Prompt(context.Background(), "ses_1", &PromptReq{}); err == nil {
		t.Fatal("expected error for empty prompt.text")
	}
	if _, err := c.Prompt(context.Background(), "ses_1", nil); err == nil {
		t.Fatal("expected error for nil request")
	}
}

func TestPrompt_success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/session/ses_1/prompt" || r.Method != "POST" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
		var b PromptReq
		_ = json.NewDecoder(r.Body).Decode(&b)
		if b.Prompt.Text != "hello" {
			t.Errorf("text = %q", b.Prompt.Text)
		}
		if b.Delivery != "queue" {
			t.Errorf("delivery = %q", b.Delivery)
		}
		_, _ = w.Write([]byte(`{"data":{"admittedSeq":5,"id":"msg_1","sessionID":"ses_1","prompt":{"text":"hello"},"delivery":"queue","timeCreated":1.5}}`))
	}))
	defer srv.Close()

	c, _ := New(srv.URL)
	got, err := c.Prompt(context.Background(), "ses_1", &PromptReq{
		Prompt:   PromptInput{Text: "hello"},
		Delivery: "queue",
	})
	if err != nil {
		t.Fatalf("Prompt: %v", err)
	}
	if got.ID != "msg_1" || got.AdmittedSeq != 5 {
		t.Errorf("got = %+v", got)
	}
}

func TestInterrupt(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/session/ses_1/interrupt" || r.Method != "POST" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
		w.WriteHeader(204)
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
		_, _ = w.Write([]byte(`{"type":"SessionNotFoundError","message":"not found"}`))
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
	if ae.Status != 404 || ae.Type != "SessionNotFoundError" {
		t.Errorf("ae = %+v", ae)
	}
	if !strings.Contains(ae.Error(), "404") {
		t.Errorf("Error() = %q", ae.Error())
	}
}

func TestListMessages_unmarshal(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/session/ses_1/message" {
			t.Errorf("path = %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"data":[{"type":"user","id":"msg_1","time":{"created":1},"text":"hi"}],"cursor":{"next":"c1"}}`))
	}))
	defer srv.Close()

	c, _ := New(srv.URL)
	out, err := c.ListMessages(context.Background(), "ses_1", &ListMessagesOpt{Limit: 10, Order: "asc"})
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	if len(out.Data) != 1 || out.Data[0].Type != "user" {
		t.Errorf("data = %+v", out.Data)
	}
	if out.Cursor == nil || out.Cursor.Next != "c1" {
		t.Errorf("cursor = %+v", out.Cursor)
	}
	_ = io.Discard
}

func TestHealth_healthy(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/health" || r.Method != "GET" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"healthy":true}`))
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
