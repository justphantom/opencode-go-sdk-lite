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

func TestWithBasicAuth(t *testing.T) {
	c, err := New("http://x", WithBasicAuth("opencode", "secret"))
	if err != nil {
		t.Fatal(err)
	}
	// base64("opencode:secret")
	if got := c.headers["Authorization"]; got != "Basic b3BlbmNvZGU6c2VjcmV0" {
		t.Errorf("Authorization = %q", got)
	}

	c2, _ := New("http://x", WithPassword("secret"))
	if got := c2.headers["Authorization"]; got != "Basic b3BlbmNvZGU6c2VjcmV0" {
		t.Errorf("WithPassword Authorization = %q", got)
	}

	// 与 WithToken 互斥：后应用者覆盖
	c3, _ := New("http://x", WithToken("tok"), WithPassword("secret"))
	if got := c3.headers["Authorization"]; got != "Basic b3BlbmNvZGU6c2VjcmV0" {
		t.Errorf("override Authorization = %q", got)
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

func TestPrompt_toolsAndFilePart(t *testing.T) {
	var gotBody struct {
		Tools map[string]bool `json:"tools"`
		Parts []PromptPart    `json:"parts"`
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(204)
	}))
	defer srv.Close()

	c, _ := New(srv.URL)
	_, err := c.Prompt(context.Background(), "ses_1", &PromptReq{
		Tools: map[string]bool{"bash": false, "read": true},
		Parts: []PromptPart{
			{Type: "text", Text: "看附件"},
			{Type: "file", Mime: "text/plain", Filename: "a.txt", URL: "data:text/plain;base64,aGk="},
		},
	})
	if err != nil {
		t.Fatalf("Prompt: %v", err)
	}
	if gotBody.Tools["bash"] != false || gotBody.Tools["read"] != true || len(gotBody.Tools) != 2 {
		t.Errorf("wire tools = %+v", gotBody.Tools)
	}
	if len(gotBody.Parts) != 2 {
		t.Fatalf("wire parts = %+v", gotBody.Parts)
	}
	fp := gotBody.Parts[1]
	if fp.Type != "file" || fp.Mime != "text/plain" || fp.Filename != "a.txt" || fp.URL == "" {
		t.Errorf("wire file part = %+v", fp)
	}
}

func TestSessionMessage_FinalText(t *testing.T) {
	m := SessionMessage{Parts: []json.RawMessage{
		json.RawMessage(`{"type":"step-start"}`),
		json.RawMessage(`{"type":"text","text":"你好"}`),
		json.RawMessage(`{"type":"text","text":"（合成）","synthetic":true}`),
		json.RawMessage(`{"type":"text","text":"（忽略）","ignored":true}`),
		json.RawMessage(`{"type":"reasoning","text":"思考"}`),
		json.RawMessage(`{"type":"text","text":"，世界"}`),
		json.RawMessage(`{bad json`),
	}}
	if got := m.FinalText(); got != "你好，世界" {
		t.Errorf("FinalText = %q", got)
	}
}

func TestSessionMessage_ReasoningText(t *testing.T) {
	cases := []struct {
		name  string
		parts []json.RawMessage
		want  string
	}{
		{
			name:  "单段 reasoning 原文返回",
			parts: []json.RawMessage{json.RawMessage(`{"type":"reasoning","text":"想一下"}`)},
			want:  "想一下",
		},
		{
			name: "多段 reasoning 以换行连接",
			parts: []json.RawMessage{
				json.RawMessage(`{"type":"reasoning","text":"先想"}`),
				json.RawMessage(`{"type":"reasoning","text":"再想"}`),
			},
			want: "先想\n再想",
		},
		{
			name:  "空 text 的 reasoning part 跳过",
			parts: []json.RawMessage{json.RawMessage(`{"type":"reasoning","text":""}`)},
			want:  "",
		},
		{
			name:  "无 reasoning part 返回空",
			parts: []json.RawMessage{json.RawMessage(`{"type":"text","text":"正文"}`)},
			want:  "",
		},
		{
			name: "与 text part 混合只取 reasoning",
			parts: []json.RawMessage{
				json.RawMessage(`{"type":"text","text":"正文"}`),
				json.RawMessage(`{"type":"reasoning","text":"思考"}`),
				json.RawMessage(`{"type":"text","text":"再正文"}`),
			},
			want: "思考",
		},
		{
			name:  "坏 JSON part 跳过",
			parts: []json.RawMessage{json.RawMessage(`{bad json`), json.RawMessage(`{"type":"reasoning","text":"好"}`)},
			want:  "好",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := SessionMessage{Parts: tc.parts}
			if got := m.ReasoningText(); got != tc.want {
				t.Errorf("ReasoningText = %q, want %q", got, tc.want)
			}
		})
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

func TestUpdateSession(t *testing.T) {
	var gotMethod, gotPath, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		buf := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(buf)
		gotBody = string(buf)
		_, _ = w.Write([]byte(`{"id":"ses_1","projectID":"p","directory":"/repo","title":"新标题","cost":0,"tokens":{"input":0,"output":0,"reasoning":0,"cache":{"read":0,"write":0}},"time":{"created":1,"updated":2}}`))
	}))
	defer srv.Close()

	c, _ := New(srv.URL)
	ses, err := c.UpdateSession(context.Background(), "ses_1", &UpdateSessionReq{Title: "新标题"})
	if err != nil {
		t.Fatalf("UpdateSession: %v", err)
	}
	if gotMethod != "PATCH" || gotPath != "/session/ses_1" {
		t.Errorf("got %s %s, want PATCH /session/ses_1", gotMethod, gotPath)
	}
	if gotBody != `{"title":"新标题"}` {
		t.Errorf("body = %s", gotBody)
	}
	if ses.Title != "新标题" {
		t.Errorf("title = %q", ses.Title)
	}
}

func TestUpdateSession_archived(t *testing.T) {
	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(buf)
		gotBody = string(buf)
		_, _ = w.Write([]byte(`{"id":"ses_1","projectID":"p","directory":"/repo","title":"","cost":0,"tokens":{"input":0,"output":0,"reasoning":0,"cache":{"read":0,"write":0}},"time":{"created":1,"updated":2,"archived":1735689600000}}`))
	}))
	defer srv.Close()

	c, _ := New(srv.URL)
	ses, err := c.UpdateSession(context.Background(), "ses_1", &UpdateSessionReq{Archived: 1735689600000})
	if err != nil {
		t.Fatalf("UpdateSession: %v", err)
	}
	if gotBody != `{"time":{"archived":1735689600000}}` {
		t.Errorf("body = %s", gotBody)
	}
	if ses.Time.Archived != 1735689600000 {
		t.Errorf("archived = %d", ses.Time.Archived)
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

func TestGetMessage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/session/ses_1/message/msg_1" || r.Method != "GET" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"info":{"id":"msg_1","sessionID":"ses_1","role":"assistant"},"parts":[{"type":"text","text":"hello"},{"type":"text","text":" world","synthetic":true}]}`))
	}))
	defer srv.Close()

	c, _ := New(srv.URL)
	m, err := c.GetMessage(context.Background(), "ses_1", "msg_1")
	if err != nil {
		t.Fatalf("GetMessage: %v", err)
	}
	if got := m.FinalText(); got != "hello" {
		t.Errorf("FinalText() = %q, want %q（synthetic 应被过滤）", got, "hello")
	}
}

func TestSessionStatuses(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/session/status" || r.Method != "GET" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"ses_1":{"type":"busy"},"ses_2":{"type":"retry"}}`))
	}))
	defer srv.Close()

	c, _ := New(srv.URL)
	st, err := c.SessionStatuses(context.Background())
	if err != nil {
		t.Fatalf("SessionStatuses: %v", err)
	}
	if st["ses_1"].Type != "busy" || st["ses_2"].Type != "retry" {
		t.Errorf("statuses = %+v", st)
	}
	if _, ok := st["ses_idle"]; ok {
		t.Error("idle 会话不应出现在 map 中")
	}
}

func TestDeleteSessionIfIdle_idle(t *testing.T) {
	var deleted bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/session/status":
			_, _ = w.Write([]byte(`{"ses_1":{"type":"idle"}}`))
		case "/session/ses_1":
			deleted = true
			_, _ = w.Write([]byte(`true`))
		}
	}))
	defer srv.Close()

	c, _ := New(srv.URL)
	if err := c.DeleteSessionIfIdle(context.Background(), "ses_1"); err != nil {
		t.Fatalf("DeleteSessionIfIdle: %v", err)
	}
	if !deleted {
		t.Error("idle 会话应发出 DELETE")
	}
}

func TestDeleteSessionIfIdle_busy(t *testing.T) {
	var deleted bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/session/status":
			_, _ = w.Write([]byte(`{"ses_1":{"type":"busy"}}`))
		case "/session/ses_1":
			deleted = true
			_, _ = w.Write([]byte(`true`))
		}
	}))
	defer srv.Close()

	c, _ := New(srv.URL)
	if err := c.DeleteSessionIfIdle(context.Background(), "ses_1"); err == nil {
		t.Fatal("busy 会话应返回错误")
	}
	if deleted {
		t.Error("busy 会话不应发出 DELETE")
	}
}

func TestDeleteSessionIfIdle_statusError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"type":"InternalError","message":"boom"}`))
	}))
	defer srv.Close()

	c, _ := New(srv.URL)
	if err := c.DeleteSessionIfIdle(context.Background(), "ses_1"); err == nil {
		t.Fatal("状态查询失败应透传错误")
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
		if got := r.URL.Query().Get("limit"); got != "200" {
			t.Errorf("limit = %q, want 200", got)
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

func TestListSessions_defaultLimit(t *testing.T) {
	for name, opt := range map[string]*ListSessionsOpt{
		"nil":      nil,
		"zero":     {Limit: 0},
		"negative": {Limit: -3},
	} {
		t.Run(name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if got := r.URL.Query().Get("limit"); got != "200" {
					t.Errorf("limit = %q, want 200", got)
				}
				_, _ = w.Write([]byte(`[]`))
			}))
			defer srv.Close()

			c, _ := New(srv.URL)
			if _, err := c.ListSessions(context.Background(), opt); err != nil {
				t.Fatalf("ListSessions: %v", err)
			}
		})
	}
}

func TestListSessions_explicitLimit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("limit"); got != "50" {
			t.Errorf("limit = %q, want 50", got)
		}
		_, _ = w.Write([]byte(`[]`))
	}))
	defer srv.Close()

	c, _ := New(srv.URL)
	if _, err := c.ListSessions(context.Background(), &ListSessionsOpt{Limit: 50}); err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
}
