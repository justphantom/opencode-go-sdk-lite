package opencode

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// makeStepFinishEvent 构造一个 step-finish(reason=stop) 终止事件（sessionID 为 ses_test）。
func makeStepFinishEvent() Event {
	return Event{
		Type:       EventMessagePartUpdated,
		Properties: []byte(`{"sessionID":"ses_test","part":{"id":"p1","type":"step-finish","reason":"stop","messageID":"msg_a","sessionID":"ses_test"}}`),
	}
}

// makeTextDeltaEvent 构造一个非终止文本事件。
func makeTextDeltaEvent() Event {
	return Event{
		Type:       EventMessagePartDelta,
		Properties: []byte(`{"sessionID":"ses_test","messageID":"msg_a","partID":"p_t","field":"text","delta":"hi"}`),
	}
}

// TestPump_DrainSrcOnExitRecoversTerminal: src 已缓冲终止事件，drainSrcOnExit 应
// 投出 HighEventResult（而非 pump 后续合成的 HighEventError）。
func TestPump_DrainSrcOnExitRecoversTerminal(t *testing.T) {
	c, _ := New("http://127.0.0.1:1")
	src := make(chan Event, 4)
	out := make(chan HighEvent, 4)
	parts := partTracker{}
	asked := &askedTracker{seen: make(map[string]bool)}
	var lastTodo string
	var accText strings.Builder

	// 缓冲一个非终止 + 一个终止事件
	src <- makeTextDeltaEvent()
	src <- makeStepFinishEvent()

	got := c.drainSrcOnExit(src, out, "ses_test", "msg_a", parts, asked, &lastTodo, &accText)
	if !got {
		t.Fatal("drainSrcOnExit 应返回 true（取到终止事件）")
	}
	// out 应有 2 个事件（text + result），最后一个是 HighEventResult
	close(out)
	var kinds []HighEventKind
	for ev := range out {
		kinds = append(kinds, ev.Kind())
	}
	if len(kinds) != 2 {
		t.Fatalf("out 事件数 = %d, want 2: %v", len(kinds), kinds)
	}
	if kinds[0] != HighEventText {
		t.Errorf("首事件 kind = %v, want HighEventText", kinds[0])
	}
	if kinds[1] != HighEventResult {
		t.Errorf("末事件 kind = %v, want HighEventResult", kinds[1])
	}
}

// TestPump_DrainSrcOnExitEmptySrc: src 空，drainSrcOnExit 返回 false（无终止事件）。
// pump 主循环据此走原合成 HighEventError 路径。
func TestPump_DrainSrcOnExitEmptySrc(t *testing.T) {
	c, _ := New("http://127.0.0.1:1")
	src := make(chan Event, 4)
	out := make(chan HighEvent, 4)
	parts := partTracker{}
	asked := &askedTracker{seen: make(map[string]bool)}
	var lastTodo string
	var accText strings.Builder

	got := c.drainSrcOnExit(src, out, "ses_test", "msg_a", parts, asked, &lastTodo, &accText)
	if got {
		t.Fatal("空 src 时 drainSrcOnExit 应返回 false")
	}
	if len(out) != 0 {
		t.Fatalf("空 src 时不应投递事件，实际 out 有 %d 个", len(out))
	}
}

// TestPump_DrainSrcOnExitOutFull: out 已满，drainSrcOnExit 提前返回（不阻塞）。
func TestPump_DrainSrcOnExitOutFull(t *testing.T) {
	c, _ := New("http://127.0.0.1:1")
	src := make(chan Event, 8)
	out := make(chan HighEvent, 1) // 容量 1
	parts := partTracker{}
	asked := &askedTracker{seen: make(map[string]bool)}
	var lastTodo string
	var accText strings.Builder

	// 先投一个事件占满 out
	src <- makeTextDeltaEvent()
	src <- makeTextDeltaEvent()  // 第二个会因 out 满而触发提前返回
	src <- makeStepFinishEvent() // 第三个终止事件也投不出去

	start := time.Now()
	got := c.drainSrcOnExit(src, out, "ses_test", "msg_a", parts, asked, &lastTodo, &accText)
	elapsed := time.Since(start)
	if got {
		t.Fatal("out 满时应返回 false")
	}
	if elapsed > 200*time.Millisecond {
		t.Errorf("drainSrcOnExit 应立即返回，实际耗时 %v", elapsed)
	}
}

// TestRunHandle_WaitTerminalReceives: 用 RunWithHandle，pump 投出终止事件后
// WaitTerminal 应收到（验证 RunHandle 句柄可被订阅者用于显式等终止事件）。
func TestRunHandle_WaitTerminalReceives(t *testing.T) {
	ch := make(chan HighEvent, 1)
	ch <- HighEvent{kind: HighEventResult, result: "done"}
	h := &RunHandle{events: ch}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	ev, ok := h.WaitTerminal(ctx)
	if !ok {
		t.Fatal("WaitTerminal 应收到终止事件")
	}
	if ev.Kind() != HighEventResult {
		t.Errorf("kind = %v, want HighEventResult", ev.Kind())
	}
	if ev.Result() != "done" {
		t.Errorf("result = %q, want done", ev.Result())
	}
}

// TestRunHandle_WaitTerminalGraceExpires: 无终止事件 + ctx 超时 → WaitTerminal 返回 false。
func TestRunHandle_WaitTerminalGraceExpires(t *testing.T) {
	ch := make(chan HighEvent, 1)
	// 投一个非终止事件，确保 WaitTerminal 不会因 chan 空立即返回
	ch <- HighEvent{kind: HighEventText, text: "thinking"}
	h := &RunHandle{events: ch}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	ev, ok := h.WaitTerminal(ctx)
	elapsed := time.Since(start)
	if ok {
		t.Fatal("WaitTerminal 应超时返回 false")
	}
	if ev.Kind() != "" {
		t.Errorf("超时返回的 ev 应零值，实际 kind=%v", ev.Kind())
	}
	if elapsed < 40*time.Millisecond {
		t.Errorf("应等到 ctx 超时，实际 %v 立即返回", elapsed)
	}
}

// TestRunHandle_WaitTerminalChanClose: chan close 后 WaitTerminal 返回 false。
func TestRunHandle_WaitTerminalChanClose(t *testing.T) {
	events := make(chan HighEvent, 1)
	close(events)
	h := &RunHandle{events: events}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, ok := h.WaitTerminal(ctx)
	if ok {
		t.Fatal("chan close 后 WaitTerminal 应返回 false")
	}
}

// TestRecoverPanic_LogsError: 注入 panic → ERROR 日志含 where/r/stack。
func TestRecoverPanic_LogsError(t *testing.T) {
	var buf bytes.Buffer
	logger := newTestLogger(&buf)

	func() {
		defer recoverPanic(logger, "test.where")
		panic(errors.New("boom"))
	}()

	log := buf.String()
	for _, want := range []string{`level=ERROR`, `panic recovered`, `where=test.where`, `r=boom`, `stack=`} {
		if !strings.Contains(log, want) {
			t.Errorf("日志缺失 %q\n完整日志:\n%s", want, log)
		}
	}
}
