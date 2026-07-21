package opencode

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// setupRunServer 构造一个 mock 服务：
//   - POST /session 返回固定 sessionID
//   - POST /session/{id}/prompt_async 返回 204
//   - POST /session/{id}/abort 返回 200
//   - GET /event 推送 framesFunc 指定的事件帧序列后保持连接
//
// framesFunc(sessionID) 返回要推送的字节流。
func setupRunServer(t *testing.T, sessionID string, framesFunc func(sid string) string) (*httptest.Server, *bool) {
	t.Helper()
	interrupted := false
	promptCh := make(chan struct{}, 8) // 等 prompt 到达后再发 frames，模拟真实服务端时序
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/session" && r.Method == "POST":
			_, _ = w.Write([]byte(`{"id":"` + sessionID + `","projectID":"global","agent":"build","model":{"id":"m","providerID":"p"},"cost":0,"tokens":{"input":0,"output":0,"reasoning":0,"cache":{"read":0,"write":0}},"time":{"created":1,"updated":1},"title":"t","directory":"/tmp"}`))
		case strings.HasPrefix(r.URL.Path, "/session/") && strings.HasSuffix(r.URL.Path, "/prompt_async"):
			w.WriteHeader(204)
			promptCh <- struct{}{}
		case strings.HasPrefix(r.URL.Path, "/session/") && strings.HasSuffix(r.URL.Path, "/abort"):
			interrupted = true
			w.WriteHeader(200)
		case r.URL.Path == "/event":
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(200)
			fl := w.(http.Flusher)
			// 等 prompt 到达（订阅已就绪）再发 frames
			select {
			case <-promptCh:
			case <-r.Context().Done():
				return
			}
			// 给 Run 主循环时间执行 Subscribe
			time.Sleep(50 * time.Millisecond)
			_, _ = w.Write([]byte(framesFunc(sessionID)))
			fl.Flush()
			<-r.Context().Done()
		default:
			w.WriteHeader(404)
		}
	}))
	return srv, &interrupted
}

const assistantMsgID = "msg_assistant_001"

// frames_textOnly: text + step.ended(finish=stop)
func frames_textOnly(sid string) string {
	var b strings.Builder
	b.WriteString(sseTextDelta(sid, assistantMsgID, "OK", 0)) // 无 durable
	b.WriteString(sseStepEnded(sid, assistantMsgID, 5))
	return b.String()
}

func TestRun_TextOnlyTurn(t *testing.T) {
	srv, _ := setupRunServer(t, "ses_run1", frames_textOnly)
	defer srv.Close()

	c, _ := New(srv.URL)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stream, _ := c.NewGlobalEventStream(ctx, nil)
	defer func() { _ = stream.Close() }()

	out, err := c.Run(ctx, stream, RunOptions{Prompt: "hi"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	var got []HighEventKind
	timeout := time.After(3 * time.Second)
	for ev := range out {
		got = append(got, ev.Kind())
		if ev.Kind() == HighEventResult || ev.Kind() == HighEventError {
			break
		}
		select {
		case <-timeout:
			t.Fatalf("timeout")
		default:
		}
	}
	// 首事件必须是 Prompt
	if len(got) == 0 || got[0] != HighEventPrompt {
		t.Fatalf("first event = %v, want prompt", got)
	}
	// 必含终止 result
	found := false
	for _, k := range got {
		if k == HighEventResult {
			found = true
		}
	}
	if !found {
		t.Errorf("no HighEventResult in %v", got)
	}
}

func TestRun_PromptFirstEvent(t *testing.T) {
	srv, _ := setupRunServer(t, "ses_run2", frames_textOnly)
	defer srv.Close()

	c, _ := New(srv.URL)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stream, _ := c.NewGlobalEventStream(ctx, nil)
	defer func() { _ = stream.Close() }()

	out, err := c.Run(ctx, stream, RunOptions{Prompt: "hi"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	select {
	case ev := <-out:
		if ev.Kind() != HighEventPrompt {
			t.Fatalf("first kind = %v", ev.Kind())
		}
		// prompt_async 返 204，user messageID 由客户端预生成
		if !strings.HasPrefix(ev.MessageID(), "msg_") {
			t.Errorf("user messageID = %q", ev.MessageID())
		}
		if ev.SessionID() != "ses_run2" {
			t.Errorf("sessionID = %q", ev.SessionID())
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no first event")
	}
}

// frames_otherAssistant: 先 step.started 锁定 assistantID，
// 再混入另一个 assistantID 的 text.delta（应被过滤），最后正确帧
func frames_otherAssistant(sid string) string {
	var b strings.Builder
	b.WriteString(sseStepStarted(sid, assistantMsgID, 1))
	// 另一个 assistantID 的帧（应被过滤）
	b.WriteString(sseTextDelta(sid, "msg_OTHER", "noise", 0))
	// 正确 assistantID 的帧
	b.WriteString(sseTextDelta(sid, assistantMsgID, "real", 0))
	b.WriteString(sseStepEnded(sid, assistantMsgID, 5))
	return b.String()
}

func sseStepStarted(sid, mid string, seq int64) string {
	return fmt.Sprintf(
		`data: {"id":"evt_%d","type":"message.part.updated","properties":{"sessionID":"%s","part":{"id":"prt_s1","messageID":"%s","sessionID":"%s","type":"step-start"},"time":1}}`+"\n\n",
		seq, sid, mid, sid,
	)
}

func TestRun_MessageIDFiltering(t *testing.T) {
	srv, _ := setupRunServer(t, "ses_run3", frames_otherAssistant)
	defer srv.Close()

	c, _ := New(srv.URL)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stream, _ := c.NewGlobalEventStream(ctx, nil)
	defer func() { _ = stream.Close() }()

	out, err := c.Run(ctx, stream, RunOptions{Prompt: "hi"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	var texts []string
	timeout := time.After(3 * time.Second)
	for ev := range out {
		if ev.Kind() == HighEventText {
			texts = append(texts, ev.Text())
		}
		if ev.Kind() == HighEventResult {
			break
		}
		select {
		case <-timeout:
			t.Fatalf("timeout")
		default:
		}
	}
	// 应只看到 "real"，不含 "noise"
	for _, tx := range texts {
		if tx == "noise" {
			t.Errorf("filtered event leaked through: %v", texts)
		}
	}
	found := false
	for _, tx := range texts {
		if tx == "real" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected 'real' in %v", texts)
	}
}

func TestRun_AbortCancelsStream(t *testing.T) {
	// stallFrames：只发 text.delta，不发终止，让 pump 必须靠 ctx 取消退出
	stallFrames := func(sid string) string {
		return sseTextDelta(sid, assistantMsgID, "partial...", 0)
	}
	srv, interrupted := setupRunServer(t, "ses_run4", stallFrames)
	defer srv.Close()

	c, _ := New(srv.URL)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stream, _ := c.NewGlobalEventStream(ctx, nil)
	defer func() { _ = stream.Close() }()

	out, err := c.Run(ctx, stream, RunOptions{Prompt: "hi"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	// 收到首事件后立即取消
	<-out
	cancel()

	// 等待 chan 关闭
	timeout := time.After(2 * time.Second)
	for range out {
		select {
		case <-timeout:
			t.Fatal("chan not closed after cancel")
		default:
		}
	}

	// 应触发 interrupt
	deadline := time.After(500 * time.Millisecond)
	for !*interrupted {
		select {
		case <-deadline:
			t.Fatal("interrupt not called")
		default:
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestRun_ResultCarriesAccumulatedText：HighEventResult.Result() 必须是
// 累积的 assistant text delta（助手回复本体），而不是 step.ended 事件
// 里的 finish 字段（"stop" 等终止原因）。回归：旧版把 finish 塞进
// result，消费方拿到 "stop" 当作最终回复。
func TestRun_ResultCarriesAccumulatedText(t *testing.T) {
	srv, _ := setupRunServer(t, "ses_run5", frames_textOnly)
	defer srv.Close()

	c, _ := New(srv.URL)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stream, _ := c.NewGlobalEventStream(ctx, nil)
	defer func() { _ = stream.Close() }()

	out, err := c.Run(ctx, stream, RunOptions{Prompt: "hi"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	var result HighEvent
	timeout := time.After(3 * time.Second)
	for ev := range out {
		if ev.Kind() == HighEventResult {
			result = ev
			break
		}
		select {
		case <-timeout:
			t.Fatal("timeout waiting for result")
		default:
		}
	}
	if result.Kind() != HighEventResult {
		t.Fatal("no HighEventResult received")
	}
	if got := result.Result(); got != "OK" {
		t.Errorf("Result() = %q, want accumulated text %q (not finish reason)", got, "OK")
	}
}

// TestRun_StreamClosedCarriesText：订阅 chan 被关闭（stream.Close 触发）且未收到
// 终止事件时，pump 走兜底 Error。错误文本必须在 text 字段（BUG-1 修复一致性），
// 调用方 ev.Text() 应拿到 "stream closed" 而非空串。
func TestRun_StreamClosedCarriesText(t *testing.T) {
	emptyFrames := func(sid string) string { return "" }
	srv, _ := setupRunServer(t, "ses_run6", emptyFrames)
	defer srv.Close()

	c, _ := New(srv.URL)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stream, _ := c.NewGlobalEventStream(ctx, nil)

	out, err := c.Run(ctx, stream, RunOptions{Prompt: "hi"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	// 等 Prompt 事件到达（确保 pump 已进入 select），再 Close 触发订阅 chan 关闭。
	go func() {
		time.Sleep(150 * time.Millisecond)
		_ = stream.Close()
	}()

	var last HighEvent
	timeout := time.After(3 * time.Second)
	for ev := range out {
		last = ev
		if ev.Kind() == HighEventError {
			break
		}
		select {
		case <-timeout:
			t.Fatal("timeout waiting for error")
		default:
		}
	}
	if last.Kind() != HighEventError {
		t.Fatalf("last kind = %v, want HighEventError", last.Kind())
	}
	if got := last.Text(); got != "stream closed" {
		t.Errorf("Text() = %q, want %q（兜底错误文本必须落到 text）", got, "stream closed")
	}
}
