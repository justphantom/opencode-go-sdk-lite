package opencode

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// sseTodoUpdated 构造一个 todo.updated 帧（properties.todos 为全量列表）。
func sseTodoUpdated(sid string, seq int64, todos []Todo) string {
	return fmt.Sprintf(
		`data: {"id":"evt_%d","type":"todo.updated","properties":{"sessionID":"%s","todos":%s}}`+"\n\n",
		seq, sid, mustJSON(todos),
	)
}

func todoList(items ...Todo) []Todo { return items }

// TestRun_TodoRecoveredByImmediatePoll：SSE 不发 todo.updated（模拟丢帧），但服务端
// /session/{id}/todo 返回非空列表。pump 启动时的首次立即轮询必须捞回并投递一次
// HighEventTodoUpdated，且 payload 为全量列表。
func TestRun_TodoRecoveredByImmediatePoll(t *testing.T) {
	sid := "ses_t_imm"
	todos := todoList(
		Todo{Content: "写测试", Status: "in_progress", Priority: "high"},
		Todo{Content: "提交", Status: "pending", Priority: "low"},
	)
	// SSE 只发终止（step-finish stop），不发 todo.updated
	frames := sseStepEnded(sid, assistantMsgID, 1)
	srv := askedPollServer(t, sid, frames,
		func() []PermissionRequest { return nil },
		func() []QuestionRequest { return nil },
		func() []Todo { return todos },
	)
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
	var todoCount int
	var todo *TodoUpdatedData
	for ev := range out {
		if ev.Kind() == HighEventTodoUpdated {
			todoCount++
			todo = ev.TodoUpdated()
		}
	}
	if todoCount != 1 {
		t.Fatalf("todo count = %d, want 1（首次立即轮询捞回一次）", todoCount)
	}
	if todo == nil || len(todo.Todos) != 2 {
		t.Fatalf("todo payload = %+v", todo)
	}
	if todo.Todos[0].Content != "写测试" || todo.Todos[1].Status != "pending" {
		t.Errorf("todos = %+v", todo.Todos)
	}
}

// TestRun_TodoDedupNoDuplicate：服务端 /todo 持续返回同一非空快照，ticker 多次触发轮询，
// 必须靠 lastTodo 签名去重，全程只投递一次 HighEventTodoUpdated。
func TestRun_TodoDedupNoDuplicate(t *testing.T) {
	restore := shrinkPoll(30*time.Millisecond, 30*time.Millisecond)
	defer restore()

	sid := "ses_t_dedup"
	todos := todoList(Todo{Content: "x", Status: "pending", Priority: "low"})
	var todoCalls int32
	todoPending := func() []Todo {
		atomic.AddInt32(&todoCalls, 1)
		return todos
	}
	// step-start 不终止，让 pump 存活承接多次 ticker 轮询
	frames := sseStepStarted(sid, assistantMsgID, 1)
	srv := askedPollServer(t, sid, frames,
		func() []PermissionRequest { return nil },
		func() []QuestionRequest { return nil },
		todoPending,
	)
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
	var todoCount int
	timeout := time.After(350 * time.Millisecond)
loop:
	for {
		select {
		case ev, ok := <-out:
			if !ok {
				break loop
			}
			if ev.Kind() == HighEventTodoUpdated {
				todoCount++
			}
		case <-timeout:
			break loop
		}
	}
	cancel()
	if todoCount != 1 {
		t.Errorf("todo count = %d, want 1（多次轮询同快照不得重复投递）", todoCount)
	}
	if todoCalls < 3 {
		t.Errorf("todoCalls = %d, want >=3（需证明多次轮询发生且被去重）", todoCalls)
	}
}

// TestRun_TodoSSEDedupWithPoll：SSE 先投递 todo.updated，轮询返回同快照，
// 必须靠 lastTodo 签名命中而不二次投递。
func TestRun_TodoSSEDedupWithPoll(t *testing.T) {
	restore := shrinkPoll(30*time.Millisecond, 30*time.Millisecond)
	defer restore()

	sid := "ses_t_ssededup"
	todos := todoList(Todo{Content: "from-sse", Status: "in_progress", Priority: "high"})
	// SSE 发 step-start + todo.updated（同快照），不发 result
	frames := sseStepStarted(sid, assistantMsgID, 1) + sseTodoUpdated(sid, 2, todos)
	srv := askedPollServer(t, sid, frames,
		func() []PermissionRequest { return nil },
		func() []QuestionRequest { return nil },
		func() []Todo { return todos }, // 轮询返回同快照
	)
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
	var todoCount int
	var first Todo
	timeout := time.After(350 * time.Millisecond)
loop:
	for {
		select {
		case ev, ok := <-out:
			if !ok {
				break loop
			}
			if ev.Kind() == HighEventTodoUpdated {
				todoCount++
				if d := ev.TodoUpdated(); d != nil && len(d.Todos) > 0 {
					first = d.Todos[0]
				}
			}
		case <-timeout:
			break loop
		}
	}
	cancel()
	if todoCount != 1 {
		t.Errorf("todo count = %d, want 1（SSE + 同快照轮询不得重复投递）", todoCount)
	}
	if first.Content != "from-sse" {
		t.Errorf("todo content = %q, want from-sse（应取 SSE 那次）", first.Content)
	}
}

// TestRun_TodoLeadingEmptySuppressed：turn 开始服务端无 todo（返回空列表），
// 首次立即轮询不得投递空清单（避免噪声），但登记签名使后续可比对。
func TestRun_TodoLeadingEmptySuppressed(t *testing.T) {
	sid := "ses_t_leadempty"
	frames := sseStepEnded(sid, assistantMsgID, 1)
	srv := askedPollServer(t, sid, frames,
		func() []PermissionRequest { return nil },
		func() []QuestionRequest { return nil },
		func() []Todo { return []Todo{} }, // 空列表
	)
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
	var todoCount int
	var sawResult bool
	for ev := range out {
		if ev.Kind() == HighEventTodoUpdated {
			todoCount++
		}
		if ev.Kind() == HighEventResult {
			sawResult = true
		}
	}
	if todoCount != 0 {
		t.Errorf("todo count = %d, want 0（leading-empty 不得投递空清单）", todoCount)
	}
	if !sawResult {
		t.Error("expected HighEventResult（pump 正常终止）")
	}
}

// TestRun_TodoClearSemantics：先投递非空 todo，随后服务端清空（[]），必须投递一次空
// HighEventTodoUpdated（保留「清空」语义）。
func TestRun_TodoClearSemantics(t *testing.T) {
	restore := shrinkPoll(30*time.Millisecond, 30*time.Millisecond)
	defer restore()

	sid := "ses_t_clear"
	nonEmpty := todoList(Todo{Content: "做完", Status: "completed", Priority: "low"})
	var todoCalls int32
	todoPending := func() []Todo {
		if atomic.AddInt32(&todoCalls, 1) == 1 {
			return nonEmpty // 首次立即轮询：非空
		}
		return []Todo{} // 之后清空
	}
	frames := sseStepStarted(sid, assistantMsgID, 1) // 不终止
	srv := askedPollServer(t, sid, frames,
		func() []PermissionRequest { return nil },
		func() []QuestionRequest { return nil },
		todoPending,
	)
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
	var events []*TodoUpdatedData
	timeout := time.After(250 * time.Millisecond)
loop:
	for {
		select {
		case ev, ok := <-out:
			if !ok {
				break loop
			}
			if ev.Kind() == HighEventTodoUpdated {
				events = append(events, ev.TodoUpdated())
			}
		case <-timeout:
			break loop
		}
	}
	cancel()
	if len(events) != 2 {
		t.Fatalf("todo events = %d, want 2（非空 + 清空）", len(events))
	}
	if len(events[0].Todos) != 1 {
		t.Errorf("first todo len = %d, want 1", len(events[0].Todos))
	}
	if len(events[1].Todos) != 0 {
		t.Errorf("second todo len = %d, want 0（清空语义）", len(events[1].Todos))
	}
}

// TestRun_TodoPollErrorSwallowed：GET /todo 返 500，pollTodo 静默吞错，
// pump 不终止，turn 仍正常收到 result。
func TestRun_TodoPollErrorSwallowed(t *testing.T) {
	sid := "ses_t_err"
	frames := sseStepEnded(sid, assistantMsgID, 1)
	srv := newTodoFailServer(t, sid, frames, 500)
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
	var todoCount int
	var sawResult bool
	for ev := range out {
		if ev.Kind() == HighEventTodoUpdated {
			todoCount++
		}
		if ev.Kind() == HighEventResult {
			sawResult = true
		}
	}
	if todoCount != 0 {
		t.Errorf("todo count = %d, want 0（500 应被吞）", todoCount)
	}
	if !sawResult {
		t.Error("expected HighEventResult（补偿路径失败不得终止 pump）")
	}
}

// newTodoFailServer 构造 /session/{id}/todo 永远返回指定错误码的 mock 服务，
// 用于验证 pollTodo 吞错。共享骨架见 runMockServer。
func newTodoFailServer(t *testing.T, sessionID, frames string, todoStatus int) *httptest.Server {
	t.Helper()
	return runMockServer(t, runServerConfig{
		sessionID:  sessionID,
		frames:     func(string) string { return frames },
		eventDelay: 30 * time.Millisecond,
		extra: func(w http.ResponseWriter, r *http.Request) bool {
			if strings.HasPrefix(r.URL.Path, "/session/") && strings.HasSuffix(r.URL.Path, "/todo") {
				w.WriteHeader(todoStatus)
				return true
			}
			return false
		},
	})
}
