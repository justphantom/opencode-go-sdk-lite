package opencode

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// shrinkPoll 缩短 asked 补偿轮询周期以加速测试，返回恢复函数。
func shrinkPoll(interval, toolDelay time.Duration) func() {
	oi, od := pollAskedInterval, pollAskedToolDelay
	pollAskedInterval = interval
	pollAskedToolDelay = toolDelay
	return func() {
		pollAskedInterval = oi
		pollAskedToolDelay = od
	}
}

// askedPollServer 构造支持 asked 补偿轮询的 mock 服务。
// permPending/qPending 动态控制每次 GET /permission、/question 的返回；
// frames 为 /event 推送一次后保持连接的事件流。
func askedPollServer(t *testing.T, sessionID, frames string,
	permPending func() []PermissionRequest, qPending func() []QuestionRequest,
) *httptest.Server {
	t.Helper()
	promptCh := make(chan struct{}, 4)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/session" && r.Method == "POST":
			_, _ = w.Write([]byte(`{"id":"` + sessionID + `","projectID":"global","agent":"build","cost":0,"tokens":{"input":0,"output":0,"reasoning":0,"cache":{"read":0,"write":0}},"time":{"created":1,"updated":1},"title":"t","directory":"/tmp"}`))
		case strings.HasPrefix(r.URL.Path, "/session/") && strings.HasSuffix(r.URL.Path, "/prompt_async"):
			w.WriteHeader(204)
			promptCh <- struct{}{}
		case strings.HasPrefix(r.URL.Path, "/session/") && strings.HasSuffix(r.URL.Path, "/abort"):
			w.WriteHeader(200)
		case strings.Contains(r.URL.Path, "/message/") && r.Method == "GET":
			_, _ = w.Write([]byte(`{"info":{"id":"` + assistantMsgID + `","sessionID":"` + sessionID + `","role":"assistant"},"parts":[{"type":"text","text":"OK"}]}`))
		case r.URL.Path == "/permission" && r.Method == "GET":
			writeJSON(w, permPending())
		case r.URL.Path == "/question" && r.Method == "GET":
			writeJSON(w, qPending())
		case r.URL.Path == "/event":
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(200)
			fl := w.(http.Flusher)
			select {
			case <-promptCh:
			case <-r.Context().Done():
				return
			}
			time.Sleep(30 * time.Millisecond)
			_, _ = w.Write([]byte(frames))
			fl.Flush()
			<-r.Context().Done()
		default:
			w.WriteHeader(404)
		}
	}))
	return srv
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// sseToolRunning 构造一个 tool part(status=running) 的 message.part.updated 帧，
// 映射为 HighEventToolUse。
func sseToolRunning(sid, mid string, seq int64) string {
	return fmt.Sprintf(
		`data: {"id":"evt_%d","type":"message.part.updated","properties":{"sessionID":"%s","part":{"id":"prt_tool1","messageID":"%s","sessionID":"%s","type":"tool","tool":"write","callID":"call_t1","state":{"status":"running","input":{"path":"a.txt"}}},"time":1}}`+"\n\n",
		seq, sid, mid, sid,
	)
}

// TestRun_AskedRecoveredByImmediatePoll：SSE 不发 asked（模拟丢帧），但服务端
// pending 列表有该会话的 permission+question。pump 启动时的首次立即轮询必须把它们
// 捞回并投递为 HighEventPermissionAsked/HighEventQuestionAsked。
func TestRun_AskedRecoveredByImmediatePoll(t *testing.T) {
	sid := "ses_imm"
	permPending := func() []PermissionRequest {
		return []PermissionRequest{{
			ID: "per_imm", SessionID: sid, Permission: "bash", Patterns: []string{"ls *"},
			Tool: &PermissionTool{MessageID: assistantMsgID, CallID: "c1"},
		}}
	}
	qPending := func() []QuestionRequest {
		return []QuestionRequest{{
			ID: "q_imm", SessionID: sid,
			Questions: []QuestionInfo{{Question: "继续?", Header: "h", Options: []QuestionOption{{Label: "y"}}}},
		}}
	}
	// SSE 只发终止（step-finish stop），不发 asked
	frames := sseStepEnded(sid, assistantMsgID, 1)
	srv := askedPollServer(t, sid, frames, permPending, qPending)
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
	var perm *PermissionAskedData
	var q *QuestionAskedData
	var askedCount int
	for ev := range out {
		if ev.Kind() == HighEventPermissionAsked {
			askedCount++
			perm = ev.PermissionAsked()
		}
		if ev.Kind() == HighEventQuestionAsked {
			askedCount++
			q = ev.QuestionAsked()
		}
	}
	if perm == nil || perm.ID != "per_imm" || perm.Tool == nil || perm.Tool.CallID != "c1" {
		t.Errorf("permission from poll = %+v", perm)
	}
	if q == nil || q.ID != "q_imm" || len(q.Questions) != 1 {
		t.Errorf("question from poll = %+v", q)
	}
	if askedCount != 2 {
		t.Errorf("asked count = %d, want 2 (1 perm + 1 question)", askedCount)
	}
}

// TestRun_AskedRecoveredByCompensateAfterTool：首次轮询时 asked 尚未发出（pending 空），
// SSE 发 tool_use（仍不发 asked）后，compensate 定时器必须在 ~toolDelay 内触发轮询
// 捞回随后进入 pending 的 permission。隔离 ticker（拉长到 5s）以只验证 compensate 路径。
func TestRun_AskedRecoveredByCompensateAfterTool(t *testing.T) {
	restore := shrinkPoll(5*time.Second, 30*time.Millisecond)
	defer restore()

	sid := "ses_comp"
	var permCalls int32
	permPending := func() []PermissionRequest {
		if atomic.AddInt32(&permCalls, 1) == 1 {
			return nil // 首次（immediate poll）空，模拟 asked 还没发出
		}
		return []PermissionRequest{{ID: "per_comp", SessionID: sid, Permission: "write"}}
	}
	// step-start + tool_use；不发 asked、不发 result，turn 保持悬挂等待补偿
	frames := sseStepStarted(sid, assistantMsgID, 1) + sseToolRunning(sid, assistantMsgID, 2)
	srv := askedPollServer(t, sid, frames, permPending, func() []QuestionRequest { return nil })
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
	var asked *PermissionAskedData
	timeout := time.After(500 * time.Millisecond)
loop:
	for {
		select {
		case ev, ok := <-out:
			if !ok {
				break loop
			}
			if ev.Kind() == HighEventPermissionAsked {
				asked = ev.PermissionAsked()
				break loop
			}
		case <-timeout:
			break loop
		}
	}
	cancel()
	if asked == nil || asked.ID != "per_comp" {
		t.Fatalf("compensate poll 未捞回 asked: %+v (permCalls=%d)", asked, permCalls)
	}
	if permCalls < 2 {
		t.Errorf("permCalls = %d, want >=2 (immediate + compensate)", permCalls)
	}
}

// TestRun_AskedDedupNoDuplicate：SSE 正常送达 asked，同时 pending 列表持续返回该
// 请求（模拟丢帧后的稳定 pending）。ticker 多次触发轮询，必须靠 seen 去重，
// 全程只投递一次 HighEventPermissionAsked。
func TestRun_AskedDedupNoDuplicate(t *testing.T) {
	restore := shrinkPoll(30*time.Millisecond, 30*time.Millisecond)
	defer restore()

	sid := "ses_dedup"
	var permCalls int32
	permPending := func() []PermissionRequest {
		if atomic.AddInt32(&permCalls, 1) == 1 {
			return nil // 首次 immediate poll 空，让 SSE 先登记
		}
		// 之后持续返回同一 pending，考验去重
		return []PermissionRequest{{ID: "per_1", SessionID: sid, Permission: "bash"}}
	}
	// step-start + permission.asked；不发 result，让 pump 存活承接多次 ticker 轮询
	frames := sseStepStarted(sid, assistantMsgID, 1) + ssePermissionAsked(sid, assistantMsgID, 2)
	srv := askedPollServer(t, sid, frames, permPending, func() []QuestionRequest { return nil })
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
	var asked int
	timeout := time.After(350 * time.Millisecond)
loop:
	for {
		select {
		case ev, ok := <-out:
			if !ok {
				break loop
			}
			if ev.Kind() == HighEventPermissionAsked {
				asked++
			}
		case <-timeout:
			break loop
		}
	}
	cancel()
	if asked != 1 {
		t.Errorf("asked count = %d, want 1（SSE + 多次轮询不得重复投递）", asked)
	}
	if permCalls < 3 {
		t.Errorf("permCalls = %d, want >=3（需证明多次轮询发生且被去重）", permCalls)
	}
}
