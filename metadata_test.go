package opencode

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// makeMessageUpdatedEvent 构造 message.updated 帧（携带 assistant info.modelID）。
// pump / processOneEvent peek 此帧抓 model 信息作为 HighEventResult.ModelID 主源。
func makeMessageUpdatedEvent(modelID, providerID string) Event {
	return Event{
		Type:       EventMessageUpdated,
		Properties: []byte(`{"sessionID":"ses_test","info":{"id":"msg_a","sessionID":"ses_test","role":"assistant","modelID":"` + modelID + `","providerID":"` + providerID + `","finish":"stop","cost":0.001,"tokens":{"input":10,"output":5,"reasoning":3,"cache":{"read":2,"write":0}}}}`),
	}
}

// makeStepFinishWithReasoning 构造带 reasoning tokens 的 step-finish 终止帧。
func makeStepFinishWithReasoning(reasoningTokens int) Event {
	return Event{
		Type:       EventMessagePartUpdated,
		Properties: []byte(`{"sessionID":"ses_test","part":{"id":"prt_f1","reason":"stop","messageID":"msg_a","sessionID":"ses_test","type":"step-finish","tokens":{"input":10,"output":5,"reasoning":` + itoa(reasoningTokens) + `,"cache":{"read":2,"write":0}},"cost":0.001}}`),
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	neg := n < 0
	if neg {
		n = -n
	}
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	if neg {
		b = append([]byte{'-'}, b...)
	}
	return string(b)
}

// TestMessageInfo_ModelFields: MessageInfo 反序列化服务端返回的 modelID/providerID。
func TestMessageInfo_ModelFields(t *testing.T) {
	raw := `{"id":"msg_x","sessionID":"ses_1","role":"assistant","modelID":"glm-5.2","providerID":"zhipuai","finish":"stop","cost":0.001,"tokens":{"input":1,"output":2,"reasoning":3,"cache":{"read":4,"write":0}}}`
	var info MessageInfo
	if err := json.Unmarshal([]byte(raw), &info); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if info.ModelID != "glm-5.2" {
		t.Errorf("ModelID = %q, want glm-5.2", info.ModelID)
	}
	if info.ProviderID != "zhipuai" {
		t.Errorf("ProviderID = %q, want zhipuai", info.ProviderID)
	}
	if info.Tokens.Reasoning != 3 {
		t.Errorf("Tokens.Reasoning = %v, want 3", info.Tokens.Reasoning)
	}
}

// TestPump_ResultIncludesReasoningTokens: step-finish 携带的 reasoning tokens
// 应通过 HighEventResult.ReasoningTokens() 暴露。
func TestPump_ResultIncludesReasoningTokens(t *testing.T) {
	c, _ := New("http://127.0.0.1:1")
	out := make(chan HighEvent, 8)
	parts := partTracker{}
	asked := &askedTracker{seen: make(map[string]bool)}
	var lastTodo string
	var accText, accThinking strings.Builder
	var resultModel, resultProvider string

	c.processOneEvent(makeStepFinishWithReasoning(42), out, "ses_test", "msg_a", parts, asked, &lastTodo, &accText, &accThinking, &resultModel, &resultProvider)

	close(out)
	for ev := range out {
		if ev.Kind() == HighEventResult {
			if got := ev.ReasoningTokens(); got != 42 {
				t.Errorf("ReasoningTokens() = %d, want 42", got)
			}
			return
		}
	}
	t.Fatal("未投出 HighEventResult")
}

// TestPump_ResultIncludesModel_FromSSE: 投 message.updated 带 modelID →
// HighEventResult.ModelID 来自 SSE 抓取（不走 GetMessage 兜底）。
func TestPump_ResultIncludesModel_FromSSE(t *testing.T) {
	c, _ := New("http://127.0.0.1:1")
	out := make(chan HighEvent, 8)
	parts := partTracker{}
	asked := &askedTracker{seen: make(map[string]bool)}
	var lastTodo string
	var accText, accThinking strings.Builder
	var resultModel, resultProvider string

	// 1. message.updated 抓 model
	c.processOneEvent(makeMessageUpdatedEvent("glm-5.2", "zhipuai"), out, "ses_test", "msg_a", parts, asked, &lastTodo, &accText, &accThinking, &resultModel, &resultProvider)
	// 2. 终止
	c.processOneEvent(makeStepFinishEvent(), out, "ses_test", "msg_a", parts, asked, &lastTodo, &accText, &accThinking, &resultModel, &resultProvider)

	close(out)
	for ev := range out {
		if ev.Kind() == HighEventResult {
			if got := ev.ModelID(); got != "glm-5.2" {
				t.Errorf("ModelID() = %q, want glm-5.2（SSE 主源）", got)
			}
			if got := ev.ProviderID(); got != "zhipuai" {
				t.Errorf("ProviderID() = %q, want zhipuai", got)
			}
			return
		}
	}
	t.Fatal("未投出 HighEventResult")
}

// TestPump_ResultIncludesModel_FromGetMessage: 不投 message.updated（SSE 未抓到 model）
// → HighEventResult.ModelID 走 GetMessage 兜底（mock server 返回 info.modelID）。
func TestPump_ResultIncludesModel_FromGetMessage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "/message/") && r.Method == "GET":
			_, _ = w.Write([]byte(`{"info":{"id":"msg_a","sessionID":"ses_test","role":"assistant","modelID":"glm-5.2","providerID":"zhipuai","finish":"stop","cost":0,"tokens":{"input":0,"output":0,"reasoning":0,"cache":{"read":0,"write":0}}},"parts":[]}`))
		case r.URL.Path == "/session/ses_test" && r.Method == "GET":
			_, _ = w.Write([]byte(`{"id":"ses_test","projectID":"global","agent":"build","cost":0.5,"tokens":{"input":100,"output":20,"reasoning":10,"cache":{"read":50,"write":0}},"time":{"created":1,"updated":1},"title":"t","directory":"/tmp"}`))
		default:
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()

	c, _ := New(srv.URL)
	out := make(chan HighEvent, 8)
	parts := partTracker{}
	asked := &askedTracker{seen: make(map[string]bool)}
	var lastTodo string
	var accText, accThinking strings.Builder
	var resultModel, resultProvider string // resultModel 留空，触发 GetMessage 兜底

	c.processOneEvent(makeStepFinishEvent(), out, "ses_test", "msg_a", parts, asked, &lastTodo, &accText, &accThinking, &resultModel, &resultProvider)

	close(out)
	for ev := range out {
		if ev.Kind() == HighEventResult {
			if got := ev.ModelID(); got != "glm-5.2" {
				t.Errorf("ModelID() = %q, want glm-5.2（GetMessage 兜底）", got)
			}
			if got := ev.ProviderID(); got != "zhipuai" {
				t.Errorf("ProviderID() = %q, want zhipuai", got)
			}
			return
		}
	}
	t.Fatal("未投出 HighEventResult")
}

// TestPump_ResultIncludesSessionUsage: HighEventResult.SessionTokens/SessionCost
// 来自 GetSession（每次 turn 结束 1 RPC）。
func TestPump_ResultIncludesSessionUsage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "/message/") && r.Method == "GET":
			_, _ = w.Write([]byte(`{"info":{"id":"msg_a","sessionID":"ses_test","role":"assistant"},"parts":[]}`))
		case r.URL.Path == "/session/ses_test" && r.Method == "GET":
			_, _ = w.Write([]byte(`{"id":"ses_test","projectID":"global","agent":"build","cost":0.42,"tokens":{"input":99735,"output":5537,"reasoning":5139,"cache":{"read":1204928,"write":0}},"time":{"created":1,"updated":1},"title":"t","directory":"/tmp"}`))
		default:
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()

	c, _ := New(srv.URL)
	out := make(chan HighEvent, 8)
	parts := partTracker{}
	asked := &askedTracker{seen: make(map[string]bool)}
	var lastTodo string
	var accText, accThinking strings.Builder
	var resultModel, resultProvider string

	c.processOneEvent(makeStepFinishEvent(), out, "ses_test", "msg_a", parts, asked, &lastTodo, &accText, &accThinking, &resultModel, &resultProvider)

	close(out)
	for ev := range out {
		if ev.Kind() == HighEventResult {
			st := ev.SessionTokens()
			if st.Input != 99735 {
				t.Errorf("SessionTokens.Input = %v, want 99735", st.Input)
			}
			if st.Reasoning != 5139 {
				t.Errorf("SessionTokens.Reasoning = %v, want 5139", st.Reasoning)
			}
			if st.Cache.Read != 1204928 {
				t.Errorf("SessionTokens.Cache.Read = %v, want 1204928", st.Cache.Read)
			}
			if got := ev.SessionCost(); got != 0.42 {
				t.Errorf("SessionCost() = %v, want 0.42", got)
			}
			return
		}
	}
	t.Fatal("未投出 HighEventResult")
}
