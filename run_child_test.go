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

// shrinkChild 缩短子 session 发现节奏以加速测试，返回恢复函数。
func shrinkChild(interval, taskDelay time.Duration) func() {
	oi, od := pollChildrenInterval, taskEventDiscoverDelay
	pollChildrenInterval = interval
	taskEventDiscoverDelay = taskDelay
	return func() {
		pollChildrenInterval = oi
		taskEventDiscoverDelay = od
	}
}

// sseToolUse 构造 tool part(status=running) 的 message.part.updated 帧，
// 可指定 tool 名（sseToolRunning 的参数化版本，便于 task/subagent 测试）。
func sseToolUse(sid, mid, toolName string, seq int64) string {
	return fmt.Sprintf(
		`data: {"id":"evt_%d","type":"message.part.updated","properties":{"sessionID":"%s","part":{"id":"prt_tool1","messageID":"%s","sessionID":"%s","type":"tool","tool":%q,"callID":"call_t1","state":{"status":"running","input":{}}},"time":1}}`+"\n\n",
		seq, sid, mid, sid, toolName,
	)
}

// childSessionJSON 构造 ListChildren 返回的单个 SessionInfo JSON 片段。
func childSessionJSON(id, parentID string) string {
	return fmt.Sprintf(
		`{"id":%q,"parentID":%q,"projectID":"global","directory":"/tmp","title":"sub","agent":"explore","cost":0,"tokens":{"input":0,"output":0,"reasoning":0,"cache":{"read":0,"write":0}},"time":{"created":1,"updated":1}}`,
		id, parentID,
	)
}

// childrenServer 构造支持子 session 发现 + asked 补偿轮询的 mock 服务。
// children 返回 sid 的直接子 session 列表（按被查询的 sessionID 区分，支持嵌套）。
// permPending/qPending 动态控制 GET /permission、/question 的返回。
func childrenServer(t *testing.T, parentSID, frames string,
	children func(querySID string) []SessionInfo,
	permPending func() []PermissionRequest,
	qPending func() []QuestionRequest,
) *httptest.Server {
	t.Helper()
	return runMockServer(t, runServerConfig{
		sessionID:  parentSID,
		frames:     func(string) string { return frames },
		eventDelay: 30 * time.Millisecond,
		extra: func(w http.ResponseWriter, r *http.Request) bool {
			switch {
			case strings.HasPrefix(r.URL.Path, "/session/") && strings.HasSuffix(r.URL.Path, "/children") && r.Method == "GET":
				// 从路径切出被查询的 sessionID（/session/{id}/children）
				querySID := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/session/"), "/children")
				writeJSON(w, children(querySID))
				return true
			case r.URL.Path == "/permission" && r.Method == "GET":
				writeJSON(w, permPending())
				return true
			case r.URL.Path == "/question" && r.Method == "GET":
				writeJSON(w, qPending())
				return true
			}
			return false
		},
	})
}

// TestRun_ChildSessionAskedForwarded：钉死 subagent 子 session 的 asked 投递链路。
// task 工具事件 → childTracker.discover 通过 ListChildren 发现子 sid 并订阅 →
// pollAsked 拉全局 pending 按父+子 sid 集合过滤 → 子 sid 的 permission 被投递为
// HighEventPermissionAsked。这是 lark-bridge 收到 subagent 权限请求的唯一兜底路径。
func TestRun_ChildSessionAskedForwarded(t *testing.T) {
	restore := shrinkChild(50*time.Millisecond, 20*time.Millisecond)
	defer restore()
	restorePoll := shrinkPoll(50*time.Millisecond, 20*time.Millisecond)
	defer restorePoll()

	sid := "ses_parent"
	childSID := "ses_child_1"
	var childrenCalls int32
	var permCalls int32

	// SSE 推 task tool_use（触发 taskDiscover）+ step-start，不发 result 让 pump 存活
	frames := sseStepStarted(sid, assistantMsgID, 1) + sseToolUse(sid, assistantMsgID, "task", 2)
	srv := childrenServer(t, sid, frames,
		func(querySID string) []SessionInfo {
			atomic.AddInt32(&childrenCalls, 1)
			if querySID == sid {
				return []SessionInfo{{ID: childSID, ParentID: sid, Directory: "/tmp", Title: "sub", Agent: "explore"}}
			}
			return nil
		},
		func() []PermissionRequest {
			atomic.AddInt32(&permCalls, 1)
			return []PermissionRequest{{ID: "per_child_1", SessionID: childSID, Permission: "bash", Patterns: []string{"ls"}}}
		},
		func() []QuestionRequest { return nil },
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

	var asked *PermissionAskedData
	timeout := time.After(2 * time.Second)
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

	if asked == nil || asked.ID != "per_child_1" || asked.SessionID != childSID {
		t.Fatalf("子 session permission 未转发: asked=%+v childrenCalls=%d permCalls=%d",
			asked, childrenCalls, permCalls)
	}
	if childrenCalls == 0 {
		t.Errorf("ListChildren 从未被调用")
	}
}

// TestRun_NestedGrandchildAsked：嵌套 subagent 的 asked 同样能投递。
// 父 → 子 → 孙 三层，ListChildren 递归发现全部，pollAsked 覆盖孙 sid。
func TestRun_NestedGrandchildAsked(t *testing.T) {
	restore := shrinkChild(50*time.Millisecond, 20*time.Millisecond)
	defer restore()
	restorePoll := shrinkPoll(50*time.Millisecond, 20*time.Millisecond)
	defer restorePoll()

	parent := "ses_nest_p"
	child := "ses_nest_c"
	grand := "ses_nest_g"

	frames := sseStepStarted(parent, assistantMsgID, 1) + sseToolUse(parent, assistantMsgID, "task", 2)
	srv := childrenServer(t, parent, frames,
		func(querySID string) []SessionInfo {
			switch querySID {
			case parent:
				return []SessionInfo{{ID: child, ParentID: parent, Directory: "/tmp"}}
			case child:
				return []SessionInfo{{ID: grand, ParentID: child, Directory: "/tmp"}}
			}
			return nil
		},
		func() []PermissionRequest {
			return []PermissionRequest{{ID: "per_grand", SessionID: grand, Permission: "write"}}
		},
		func() []QuestionRequest { return nil },
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

	var asked *PermissionAskedData
	timeout := time.After(2 * time.Second)
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

	if asked == nil || asked.ID != "per_grand" || asked.SessionID != grand {
		t.Fatalf("孙 session permission 未转发: %+v", asked)
	}
}

// TestRun_ChildUnsubscribeOnExit：pump 退出时必须 Unsubscribe 全部子 sid，
// 否则子 session 的订阅 chan 泄漏并持续占 GlobalEventStream.subs 槽位。
// 间接验证：turn 结束（cancel）后向子 sid 推一帧，stream.subs 不应再持有它
// （通过 stream Subscribe 同 sid 不会触发旧 chan 关闭来验证比较间接，
// 这里用「close 后 subs 映射不含子 sid」直接断言）。
func TestRun_ChildUnsubscribeOnExit(t *testing.T) {
	restore := shrinkChild(50*time.Millisecond, 20*time.Millisecond)
	defer restore()
	restorePoll := shrinkPoll(50*time.Millisecond, 20*time.Millisecond)
	defer restorePoll()

	sid := "ses_close_p"
	childSID := "ses_close_c"

	frames := sseStepStarted(sid, assistantMsgID, 1) + sseToolUse(sid, assistantMsgID, "task", 2)
	srv := childrenServer(t, sid, frames,
		func(querySID string) []SessionInfo {
			if querySID == sid {
				return []SessionInfo{{ID: childSID, ParentID: sid, Directory: "/tmp"}}
			}
			return nil
		},
		func() []PermissionRequest { return nil },
		func() []QuestionRequest { return nil },
	)
	defer srv.Close()

	c, _ := New(srv.URL)
	ctx, cancel := context.WithCancel(context.Background())
	stream, _ := c.NewGlobalEventStream(ctx, nil)
	defer func() { _ = stream.Close() }()

	out, err := c.Run(ctx, stream, RunOptions{Prompt: "hi"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	// 等 discover 发生（子 sid 进 subs）
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		stream.mu.Lock()
		_, subscribed := stream.subs[childSID]
		stream.mu.Unlock()
		if subscribed {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	// 取消让 pump 退出
	cancel()
	for range out {
	}

	// pump 退出后子 sid 必须已 Unsubscribe
	stream.mu.Lock()
	_, leaked := stream.subs[childSID]
	stream.mu.Unlock()
	if leaked {
		t.Fatalf("pump 退出后子 sid %q 仍在 stream.subs（订阅泄漏）", childSID)
	}
}

// TestForwardChildAsked_DropsNonAsked：直接驱动 forwardAsked，验证它只转发 asked，
// 子 session 的 text/idle/tool 等事件一律丢——否则会让父 pump 误判 turn 结束
// 或污染父文本流。单元化测试避免 SSE 时序干扰。
func TestForwardChildAsked_DropsNonAsked(t *testing.T) {
	out := make(chan HighEvent, 16)
	asked := &askedTracker{seen: make(map[string]bool)}
	parentCtx, cancelParent := context.WithCancel(context.Background())
	defer cancelParent()

	tr := newChildTracker(nil, out, asked, "/tmp", parentCtx)

	// 手工构造子 chan，注入混合事件
	childSrc := make(chan Event, 8)
	// 非 asked 事件：应被丢弃
	childSrc <- Event{Type: EventMessagePartUpdated, Properties: []byte(`{"sessionID":"ses_c","part":{"id":"p1","type":"text","text":"hi"}}`)}
	childSrc <- Event{Type: EventSessionIdle, Properties: []byte(`{"sessionID":"ses_c"}`)}
	childSrc <- Event{Type: EventMessageUpdated, Properties: []byte(`{"sessionID":"ses_c","info":{"role":"assistant"}}`)}
	// asked 事件：应被转发
	childSrc <- Event{Type: EventPermissionAsked, Properties: []byte(`{"id":"per_x","sessionID":"ses_c","permission":"bash","patterns":["ls"]}`)}
	close(childSrc)

	tr.wg.Add(1)
	tr.forwardAsked(childSrc)

	select {
	case ev := <-out:
		if ev.Kind() != HighEventPermissionAsked {
			t.Fatalf("转发了非 asked 事件: %v", ev.Kind())
		}
		if ev.PermissionAsked().ID != "per_x" {
			t.Fatalf("asked id = %q", ev.PermissionAsked().ID)
		}
	case <-time.After(time.Second):
		t.Fatal("asked 未被转发")
	}

	// out 应再无事件（非 asked 全部丢弃）
	select {
	case ev := <-out:
		t.Fatalf("意外收到额外事件: %v", ev.Kind())
	case <-time.After(100 * time.Millisecond):
	}
}

// TestForwardChildAsked_DedupAcrossParentAndChild：父 SSE 与子 SSE/poll 投同一
// requestID 的 asked，先到登记后到丢，全程只投一次。
func TestForwardChildAsked_DedupAcrossParentAndChild(t *testing.T) {
	out := make(chan HighEvent, 16)
	asked := &askedTracker{seen: make(map[string]bool)}
	parentCtx, cancelParent := context.WithCancel(context.Background())
	defer cancelParent()

	tr := newChildTracker(nil, out, asked, "/tmp", parentCtx)

	// 父路径先登记
	parentAsked := HighEvent{kind: HighEventPermissionAsked, permission: &PermissionAskedData{ID: "per_dup", SessionID: "ses_p"}}
	if !asked.register(&parentAsked) {
		t.Fatal("父首次登记应成功")
	}

	// 子路径推同 ID：应被去重丢弃
	childSrc := make(chan Event, 2)
	childSrc <- Event{Type: EventPermissionAsked, Properties: []byte(`{"id":"per_dup","sessionID":"ses_c","permission":"bash"}`)}
	close(childSrc)

	tr.wg.Add(1)
	tr.forwardAsked(childSrc)

	select {
	case ev := <-out:
		t.Fatalf("重复 asked 不应被转发: %+v", ev)
	case <-time.After(100 * time.Millisecond):
	}
}
