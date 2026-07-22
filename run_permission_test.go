package opencode

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

// ssePermissionAsked 构造一个 permission.asked 帧（实测 properties 格式，含 tool 关联）。
func ssePermissionAsked(sid, mid string, seq int64) string {
	return fmt.Sprintf(
		`data: {"id":"evt_%d","type":"permission.asked","properties":{"id":"per_1","sessionID":"%s","permission":"bash","patterns":["ls *"],"tool":{"messageID":"%s","callID":"call_1"}}}`+"\n\n",
		seq, sid, mid,
	)
}

// TestRun_PermissionAskedPassesThrough：钉死 asked 事件的三条 pump 语义——
// 非终止（asked 后 text/result 照常到达、chan 在 result 后才 close）、
// 不被 assistantID 过滤（assistantID 锁定后 messageID 为空的 asked 仍送达）。
// 不断言 asked 与 tool running 的先后：实测两者顺序不稳定（permission 在后、question 在前）。
func TestRun_PermissionAskedPassesThrough(t *testing.T) {
	frames := func(sid string) string {
		var b strings.Builder
		b.WriteString(sseStepStarted(sid, assistantMsgID, 1))
		b.WriteString(ssePermissionAsked(sid, assistantMsgID, 2))
		b.WriteString(sseTextDelta(sid, assistantMsgID, "done", 3))
		b.WriteString(sseStepEnded(sid, assistantMsgID, 4))
		return b.String()
	}
	srv, _ := setupRunServer(t, "ses_perm", frames)
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

	var kinds []HighEventKind
	var asked *PermissionAskedData
	// range 到 chan 关闭，close 发生在 result 之后由末尾 kinds 断言覆盖
	for ev := range out {
		kinds = append(kinds, ev.Kind())
		if ev.Kind() == HighEventPermissionAsked {
			if ev.MessageID() != "" {
				t.Errorf("asked MessageID() = %q, want empty", ev.MessageID())
			}
			asked = ev.PermissionAsked()
		}
	}
	want := []HighEventKind{HighEventPrompt, HighEventStepStart, HighEventPermissionAsked, HighEventText, HighEventResult}
	if fmt.Sprint(kinds) != fmt.Sprint(want) {
		t.Fatalf("kinds = %v, want %v", kinds, want)
	}
	if asked == nil || asked.ID != "per_1" || asked.Tool == nil || asked.Tool.CallID != "call_1" {
		t.Fatalf("asked payload = %+v", asked)
	}
}
