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

// runServerConfig 描述 run 系列测试共享的 mock 服务骨架，消除 setupRunServer /
// askedPollServer / newTodoFailServer 三份同构路由的重复。
type runServerConfig struct {
	sessionID  string
	frames     func(sid string) string // /event 推送一次的帧序列
	eventDelay time.Duration           // prompt 到达后写 frames 前的等待，让 Subscribe 就绪
	onAbort    func()                  // POST .../abort 的副作用
	// extra 处理变体路由（/permission、/question、/todo），返回 true 表示已处理；
	// 在 GET .../message/ 之后、/event 之前判定。
	extra func(w http.ResponseWriter, r *http.Request) bool
}

// runMockServer 构造 run 系列共用的 mock 骨架：建会话、prompt_async、abort、
// 取消息、SSE 推帧、default 404。变体路由与 abort 副作用经 cfg 注入。
func runMockServer(t *testing.T, cfg runServerConfig) *httptest.Server {
	t.Helper()
	promptCh := make(chan struct{}, 8) // 等 prompt 到达后再发 frames，模拟真实服务端时序
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/session" && r.Method == "POST":
			_, _ = w.Write([]byte(`{"id":"` + cfg.sessionID + `","projectID":"global","agent":"build","model":{"id":"m","providerID":"p"},"cost":0,"tokens":{"input":0,"output":0,"reasoning":0,"cache":{"read":0,"write":0}},"time":{"created":1,"updated":1},"title":"t","directory":"/tmp"}`))
		case strings.HasPrefix(r.URL.Path, "/session/") && strings.HasSuffix(r.URL.Path, "/prompt_async"):
			w.WriteHeader(204)
			promptCh <- struct{}{}
		case strings.HasPrefix(r.URL.Path, "/session/") && strings.HasSuffix(r.URL.Path, "/abort"):
			if cfg.onAbort != nil {
				cfg.onAbort()
			}
			w.WriteHeader(200)
		case strings.HasPrefix(r.URL.Path, "/session/") && strings.Contains(r.URL.Path, "/message/") && r.Method == "GET":
			// 落库文本与 frames_textOnly 的 delta 一致（"OK"）
			_, _ = w.Write([]byte(`{"info":{"id":"` + assistantMsgID + `","sessionID":"` + cfg.sessionID + `","role":"assistant"},"parts":[{"type":"text","text":"OK"}]}`))
		case cfg.extra != nil && cfg.extra(w, r):
			// 变体路由已处理
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
			time.Sleep(cfg.eventDelay)
			_, _ = w.Write([]byte(cfg.frames(cfg.sessionID)))
			fl.Flush()
			<-r.Context().Done()
		default:
			w.WriteHeader(404)
		}
	}))
	return srv
}

// setupRunServer 构造一个 mock 服务并在 abort 时记录 interrupted 标志。
// framesFunc(sessionID) 返回要推送的字节流。
func setupRunServer(t *testing.T, sessionID string, framesFunc func(sid string) string) (*httptest.Server, *bool) {
	t.Helper()
	interrupted := false
	srv := runMockServer(t, runServerConfig{
		sessionID:  sessionID,
		frames:     framesFunc,
		eventDelay: 50 * time.Millisecond,
		onAbort:    func() { interrupted = true },
	})
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

// TestRun_ResultPrefersServerText：终止回填优先用服务端落库文本
// （GET message 的 FinalText），SSE delta 缺失时以服务端为准。
func TestRun_ResultPrefersServerText(t *testing.T) {
	promptCh := make(chan struct{}, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/session" && r.Method == "POST":
			_, _ = w.Write([]byte(`{"id":"ses_run7","projectID":"global","cost":0,"tokens":{"input":0,"output":0,"reasoning":0,"cache":{"read":0,"write":0}},"time":{"created":1,"updated":1},"title":"t","directory":"/tmp"}`))
		case strings.HasPrefix(r.URL.Path, "/session/") && strings.HasSuffix(r.URL.Path, "/prompt_async"):
			w.WriteHeader(204)
			promptCh <- struct{}{}
		case strings.Contains(r.URL.Path, "/message/") && r.Method == "GET":
			_, _ = w.Write([]byte(`{"info":{"id":"` + assistantMsgID + `","sessionID":"ses_run7","role":"assistant"},"parts":[{"type":"text","text":"full reply"}]}`))
		case r.URL.Path == "/event":
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(200)
			fl := w.(http.Flusher)
			select {
			case <-promptCh:
			case <-r.Context().Done():
				return
			}
			time.Sleep(50 * time.Millisecond)
			// delta 只到了一部分（模拟丢帧）
			_, _ = w.Write([]byte(sseTextDelta("ses_run7", assistantMsgID, "full re", 0) + sseStepEnded("ses_run7", assistantMsgID, 5)))
			fl.Flush()
			<-r.Context().Done()
		default:
			w.WriteHeader(404)
		}
	}))
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
	for ev := range out {
		if ev.Kind() == HighEventResult {
			if got := ev.Result(); got != "full reply" {
				t.Errorf("Result() = %q, want 服务端落库文本 %q", got, "full reply")
			}
			return
		}
	}
	t.Fatal("no HighEventResult")
}

// sseStepFinish 构造一个指定 reason 的 step-finish 的 message.part.updated 帧。
func sseStepFinish(sid, mid, reason string, seq int64) string {
	return fmt.Sprintf(
		`data: {"id":"evt_%d","type":"message.part.updated","properties":{"sessionID":"%s","part":{"id":"prt_f1","reason":%q,"messageID":"%s","sessionID":"%s","type":"step-finish","tokens":{"input":1,"output":1,"reasoning":0,"cache":{"read":0,"write":0}},"cost":0},"time":1}}`+"\n\n",
		seq, sid, reason, mid, sid,
	)
}

// frames_agentLoop: 多轮 agent-loop——A 轮 tool-calls 收尾，B 轮文本+stop。
// 两轮 messageID 不同，回归 BUG：assistantID 单向锁定首轮导致 B 轮事件被过滤。
func frames_agentLoop(sid string) string {
	var b strings.Builder
	b.WriteString(sseStepStarted(sid, "msg_round_a", 1))
	b.WriteString(sseTextDelta(sid, "msg_round_a", "调用工具", 2))
	b.WriteString(sseStepFinish(sid, "msg_round_a", "tool-calls", 3))
	b.WriteString(sseStepStarted(sid, assistantMsgID, 4))
	b.WriteString(sseTextDelta(sid, assistantMsgID, "OK", 5))
	b.WriteString(sseStepEnded(sid, assistantMsgID, 6))
	return b.String()
}

// TestRun_MultiRoundAgentLoop：多轮 agent-loop 下 assistantID 跟随最新 step，
// 第二轮的 text 与 step-finish(stop) 不被过滤，终态 result 非空且等于第二轮文本。
func TestRun_MultiRoundAgentLoop(t *testing.T) {
	srv, _ := setupRunServer(t, "ses_run8", frames_agentLoop)
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
	var stepFinishes int
	var result HighEvent
	timeout := time.After(3 * time.Second)
loop:
	for {
		select {
		case ev, ok := <-out:
			if !ok {
				break loop
			}
			switch ev.Kind() {
			case HighEventText:
				texts = append(texts, ev.Text())
			case HighEventStepFinish:
				stepFinishes++
			case HighEventResult:
				result = ev
				break loop
			}
		case <-timeout:
			t.Fatal("timeout waiting for result")
		}
	}
	if result.Kind() != HighEventResult {
		t.Fatal("no HighEventResult received")
	}
	if stepFinishes != 1 {
		t.Errorf("step_finish count = %d, want 1 (tool-calls 中间步)", stepFinishes)
	}
	joined := strings.Join(texts, "")
	if !strings.Contains(joined, "OK") {
		t.Errorf("round-B text filtered out, texts = %v", texts)
	}
	if got := result.Result(); got != "OK" {
		t.Errorf("Result() = %q, want %q（多轮终态不得为空）", got, "OK")
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
