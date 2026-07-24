package opencode

import (
	"testing"
)

// TestFormatErrorMap：error map 优先取 message 字段；
// 无 message 时退到 JSON 序列化；空 map 返空。
func TestFormatErrorMap(t *testing.T) {
	tests := []struct {
		name string
		in   map[string]any
		want string
	}{
		{"nil", nil, ""},
		{"empty", map[string]any{}, ""},
		{"message preferred", map[string]any{"message": "boom", "code": 1}, "boom"},
		{"fallback to json", map[string]any{"code": 1}, `{"code":1}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := formatErrorMap(tt.in); got != tt.want {
				t.Errorf("formatErrorMap = %q, want %q", got, tt.want)
			}
		})
	}
}

func partUpdatedEvent(t PartUpdatedData) Event {
	return Event{Type: EventMessagePartUpdated, Properties: jsonRaw(mustJSON(t))}
}

// TestMapToHighEvent_TextTurn：text part 登记 → delta 映射为 text →
// step-finish(reason=stop) 为终止 result。
func TestMapToHighEvent_TextTurn(t *testing.T) {
	var assistantID string
	parts := partTracker{}

	// text part.updated 只登记类型，不产生事件、不锁 assistantID
	//（用户输入回显也是 text part，抢锁会过滤掉 assistant delta）
	he, emit, term := mapToHighEvent(partUpdatedEvent(PartUpdatedData{
		SessionID: "ses_1",
		Part:      Part{ID: "prt_1", MessageID: "msg_user", Type: "text"},
	}), &assistantID, parts)
	if emit || term {
		t.Errorf("text part.updated should not emit: %+v", he)
	}
	if assistantID != "" {
		t.Errorf("text part.updated must not lock assistantID, got %q", assistantID)
	}

	// step-start 锁定 assistantID
	_, emit, _ = mapToHighEvent(partUpdatedEvent(PartUpdatedData{
		SessionID: "ses_1",
		Part:      Part{ID: "prt_s", MessageID: "msg_a", Type: "step-start"},
	}), &assistantID, parts)
	if !emit || assistantID != "msg_a" {
		t.Errorf("step-start should emit and lock, assistantID = %q", assistantID)
	}

	// delta → text
	ev := Event{Type: EventMessagePartDelta, Properties: jsonRaw(mustJSON(
		PartDeltaData{SessionID: "ses_1", MessageID: "msg_a", PartID: "prt_1", Field: "text", Delta: "你好"}))}
	he, emit, term = mapToHighEvent(ev, &assistantID, parts)
	if !emit || term || he.Kind() != HighEventText || he.Text() != "你好" {
		t.Errorf("delta: %+v emit=%v term=%v", he, emit, term)
	}

	// step-finish stop → 终止 result
	he, emit, term = mapToHighEvent(partUpdatedEvent(PartUpdatedData{
		SessionID: "ses_1",
		Part: Part{ID: "prt_2", MessageID: "msg_a", Type: "step-finish", Reason: "stop",
			Tokens: StepTokens{Input: 10, Output: 5, Cache: StepCache{Read: 3, Write: 1}}, Cost: 0.01},
	}), &assistantID, parts)
	if !emit || !term || he.Kind() != HighEventResult {
		t.Fatalf("step-finish: %+v emit=%v term=%v", he, emit, term)
	}
	if he.InputTokens() != 10 || he.OutputTokens() != 5 || he.CacheRead() != 3 || he.CacheWrite() != 1 {
		t.Errorf("tokens = %+v", he)
	}
}

// TestMapToHighEvent_ReasoningDelta：reasoning part 的 delta 映射为 thinking。
func TestMapToHighEvent_ReasoningDelta(t *testing.T) {
	var assistantID string
	parts := partTracker{}

	mapToHighEvent(partUpdatedEvent(PartUpdatedData{
		SessionID: "ses_1",
		Part:      Part{ID: "prt_r", MessageID: "msg_a", Type: "reasoning"},
	}), &assistantID, parts)

	ev := Event{Type: EventMessagePartDelta, Properties: jsonRaw(mustJSON(
		PartDeltaData{SessionID: "ses_1", MessageID: "msg_a", PartID: "prt_r", Delta: "思考中"}))}
	he, emit, _ := mapToHighEvent(ev, &assistantID, parts)
	if !emit || he.Kind() != HighEventThinking {
		t.Errorf("reasoning delta: %+v", he)
	}
}

// TestMapToHighEvent_Tool：tool part 三态映射。
func TestMapToHighEvent_Tool(t *testing.T) {
	var assistantID string
	parts := partTracker{}

	running := partUpdatedEvent(PartUpdatedData{SessionID: "ses_1", Part: Part{
		ID: "prt_t", MessageID: "msg_a", Type: "tool", Tool: "bash", CallID: "c1",
		State: &ToolState{Status: "running", Input: map[string]any{"cmd": "ls"}},
	}})
	he, emit, _ := mapToHighEvent(running, &assistantID, parts)
	if !emit || he.Kind() != HighEventToolUse || he.ToolName() != "bash" {
		t.Errorf("running: %+v", he)
	}
	if he.ToolKind() != ToolKindShell {
		t.Errorf("running toolKind = %s, want shell", he.ToolKind())
	}

	completed := partUpdatedEvent(PartUpdatedData{SessionID: "ses_1", Part: Part{
		ID: "prt_t", MessageID: "msg_a", Type: "tool", Tool: "bash",
		State: &ToolState{Status: "completed", Output: "file.txt"},
	}})
	he, emit, _ = mapToHighEvent(completed, &assistantID, parts)
	if !emit || he.Kind() != HighEventToolResult || he.IsToolError() || he.Text() != "file.txt" {
		t.Errorf("completed: %+v", he)
	}

	errored := partUpdatedEvent(PartUpdatedData{SessionID: "ses_1", Part: Part{
		ID: "prt_t", MessageID: "msg_a", Type: "tool", Tool: "bash",
		State: &ToolState{Status: "error", Error: "exit 1"},
	}})
	he, emit, _ = mapToHighEvent(errored, &assistantID, parts)
	if !emit || he.Kind() != HighEventToolResult || !he.IsToolError() || he.Text() != "exit 1" {
		t.Errorf("error: %+v", he)
	}
	if he.ToolKind() != ToolKindShell {
		t.Errorf("error toolKind = %s, want shell", he.ToolKind())
	}
}

// TestMapToHighEvent_IdleTerminal：session.idle 兜底终止。
func TestMapToHighEvent_IdleTerminal(t *testing.T) {
	var assistantID string
	parts := partTracker{}
	ev := Event{Type: EventSessionIdle, Properties: jsonRaw(`{"sessionID":"ses_1"}`)}
	he, emit, term := mapToHighEvent(ev, &assistantID, parts)
	if !emit || !term || he.Kind() != HighEventResult || he.SessionID() != "ses_1" {
		t.Errorf("idle: %+v emit=%v term=%v", he, emit, term)
	}
}

// TestMapToHighEvent_StepFinishNonStop：多步 turn 的中间 step-finish 不终止。
func TestMapToHighEvent_StepFinishNonStop(t *testing.T) {
	var assistantID string
	parts := partTracker{}
	he, emit, term := mapToHighEvent(partUpdatedEvent(PartUpdatedData{
		SessionID: "ses_1",
		Part:      Part{ID: "prt_2", MessageID: "msg_a", Type: "step-finish", Reason: "tool-calls"},
	}), &assistantID, parts)
	if !emit || term || he.Kind() != HighEventStepFinish || he.Result() != "tool-calls" {
		t.Errorf("step-finish non-stop: %+v emit=%v term=%v", he, emit, term)
	}
}

// TestMapToHighEvent_PermissionAsked：permission.asked 映射为非终止事件，
// payload 含 tool 关联（golden 取自 docs/sse-capture-permission-question.log 实测流）。
func TestMapToHighEvent_PermissionAsked(t *testing.T) {
	var assistantID string
	parts := partTracker{}
	props := `{"id":"per_1","sessionID":"ses_1","permission":"bash","patterns":["ls -la /tmp/oc-ptest"],"metadata":{"command":"ls -la /tmp/oc-ptest"},"always":["ls *"],"tool":{"messageID":"msg_a","callID":"call_1"}}`
	he, emit, term := mapToHighEvent(Event{Type: EventPermissionAsked, Properties: jsonRaw(props)}, &assistantID, parts)
	if !emit || term || he.Kind() != HighEventPermissionAsked || he.SessionID() != "ses_1" {
		t.Fatalf("permission.asked: %+v emit=%v term=%v", he, emit, term)
	}
	// messageID 必须为空：session 级事件，空值才能绕过 pump 的 assistantID 过滤
	if he.MessageID() != "" {
		t.Errorf("MessageID() = %q, want empty", he.MessageID())
	}
	d := he.PermissionAsked()
	if d == nil {
		t.Fatal("PermissionAsked() is nil")
	}
	if d.ID != "per_1" || d.Permission != "bash" {
		t.Errorf("payload = %+v", d)
	}
	if len(d.Patterns) != 1 || d.Patterns[0] != "ls -la /tmp/oc-ptest" {
		t.Errorf("patterns = %v", d.Patterns)
	}
	if len(d.Always) != 1 || d.Always[0] != "ls *" {
		t.Errorf("always = %v", d.Always)
	}
	if d.Metadata["command"] != "ls -la /tmp/oc-ptest" {
		t.Errorf("metadata = %v", d.Metadata)
	}
	if d.Tool == nil || d.Tool.MessageID != "msg_a" || d.Tool.CallID != "call_1" {
		t.Errorf("tool = %+v", d.Tool)
	}
	if he.QuestionAsked() != nil {
		t.Error("QuestionAsked() must be nil for permission kind")
	}
	// 解码失败丢事件（对齐现有容错）
	if _, emit, _ := mapToHighEvent(Event{Type: EventPermissionAsked, Properties: jsonRaw(`{bad`)}, &assistantID, parts); emit {
		t.Error("bad json should drop event")
	}
}

// TestMapToHighEvent_QuestionAsked：question.asked 映射为非终止事件，
// 覆盖多问题 + options + custom/multiple 字段（字段名按实测流）。
func TestMapToHighEvent_QuestionAsked(t *testing.T) {
	var assistantID string
	parts := partTracker{}
	props := `{"id":"que_1","sessionID":"ses_1","questions":[` +
		`{"question":"你想喝什么？","header":"饮品选择","options":[{"label":"咖啡","description":"含咖啡因饮品"},{"label":"茶","description":"茶类饮品"}]},` +
		`{"question":"选哪些功能？","header":"功能","options":[{"label":"A","description":"a"}],"multiple":true,"custom":true}` +
		`],"tool":{"messageID":"msg_a","callID":"call_2"}}`
	he, emit, term := mapToHighEvent(Event{Type: EventQuestionAsked, Properties: jsonRaw(props)}, &assistantID, parts)
	if !emit || term || he.Kind() != HighEventQuestionAsked || he.SessionID() != "ses_1" {
		t.Fatalf("question.asked: %+v emit=%v term=%v", he, emit, term)
	}
	if he.MessageID() != "" {
		t.Errorf("MessageID() = %q, want empty", he.MessageID())
	}
	d := he.QuestionAsked()
	if d == nil {
		t.Fatal("QuestionAsked() is nil")
	}
	if d.ID != "que_1" || len(d.Questions) != 2 {
		t.Fatalf("payload = %+v", d)
	}
	q0, q1 := d.Questions[0], d.Questions[1]
	if q0.Question != "你想喝什么？" || q0.Header != "饮品选择" || len(q0.Options) != 2 {
		t.Errorf("q0 = %+v", q0)
	}
	if q0.Options[0].Label != "咖啡" || q0.Options[0].Description != "含咖啡因饮品" {
		t.Errorf("q0.options[0] = %+v", q0.Options[0])
	}
	if !q1.Multiple || !q1.Custom {
		t.Errorf("q1 multiple/custom = %v/%v, want true/true", q1.Multiple, q1.Custom)
	}
	if d.Tool == nil || d.Tool.CallID != "call_2" {
		t.Errorf("tool = %+v", d.Tool)
	}
	if he.PermissionAsked() != nil {
		t.Error("PermissionAsked() must be nil for question kind")
	}
	if _, emit, _ := mapToHighEvent(Event{Type: EventQuestionAsked, Properties: jsonRaw(`{bad`)}, &assistantID, parts); emit {
		t.Error("bad json should drop event")
	}
}

// TestMapToHighEvent_TodoUpdated：todo.updated 映射为非终止事件，
// payload 为全量列表；空 todos / 缺 todos 字段均正常上抛（不得丢事件）。
func TestMapToHighEvent_TodoUpdated(t *testing.T) {
	var assistantID string
	parts := partTracker{}

	// 正常全量列表（2 项，多种 status/priority）
	props := `{"sessionID":"ses_1","todos":[` +
		`{"content":"写测试","status":"in_progress","priority":"high"},` +
		`{"content":"提交","status":"pending","priority":"low"}]}`
	he, emit, term := mapToHighEvent(Event{Type: EventTodoUpdated, Properties: jsonRaw(props)}, &assistantID, parts)
	if !emit || term || he.Kind() != HighEventTodoUpdated || he.SessionID() != "ses_1" {
		t.Fatalf("todo.updated: %+v emit=%v term=%v", he, emit, term)
	}
	// messageID 必须为空：session 级事件，空值才能绕过 pump 的 assistantID 过滤
	if he.MessageID() != "" {
		t.Errorf("MessageID() = %q, want empty", he.MessageID())
	}
	d := he.TodoUpdated()
	if d == nil {
		t.Fatal("TodoUpdated() is nil")
	}
	if len(d.Todos) != 2 {
		t.Fatalf("payload todos len = %d, want 2", len(d.Todos))
	}
	if d.Todos[0].Content != "写测试" || d.Todos[0].Status != "in_progress" || d.Todos[0].Priority != "high" {
		t.Errorf("todos[0] = %+v", d.Todos[0])
	}
	if d.Todos[1].Status != "pending" || d.Todos[1].Priority != "low" {
		t.Errorf("todos[1] = %+v", d.Todos[1])
	}
	// getter 隔离：todo kind 下 asked getter 必须为 nil
	if he.PermissionAsked() != nil || he.QuestionAsked() != nil {
		t.Error("asked getters must be nil for todo kind")
	}

	// 空 todos：不得 return false（语义=清空）
	he, emit, term = mapToHighEvent(Event{Type: EventTodoUpdated, Properties: jsonRaw(`{"sessionID":"ses_1","todos":[]}`)}, &assistantID, parts)
	if !emit || term || he.Kind() != HighEventTodoUpdated {
		t.Errorf("empty todos: %+v emit=%v term=%v", he, emit, term)
	}
	if d := he.TodoUpdated(); d == nil || len(d.Todos) != 0 {
		t.Errorf("empty todos payload = %+v", d)
	}

	// 缺 todos 字段：映射成功，Todos 为 nil/0
	he, emit, _ = mapToHighEvent(Event{Type: EventTodoUpdated, Properties: jsonRaw(`{"sessionID":"ses_1"}`)}, &assistantID, parts)
	if !emit || he.TodoUpdated() == nil || len(he.TodoUpdated().Todos) != 0 {
		t.Errorf("missing todos field: %+v emit=%v", he, emit)
	}

	// 坏 JSON：丢事件
	if _, emit, _ := mapToHighEvent(Event{Type: EventTodoUpdated, Properties: jsonRaw(`{bad`)}, &assistantID, parts); emit {
		t.Error("bad json should drop event")
	}
}

// TestFollowAssistantID：无条件跟随新 messageID；空 id 与 nil 指针不动。
func TestFollowAssistantID(t *testing.T) {
	id := "msg_a"
	followAssistantID(&id, "msg_b")
	if id != "msg_b" {
		t.Errorf("follow = %q, want msg_b", id)
	}
	followAssistantID(&id, "")
	if id != "msg_b" {
		t.Errorf("empty id must not overwrite, got %q", id)
	}
	followAssistantID(nil, "msg_c") // 不应 panic
}

// TestMapToHighEvent_MultiRoundFollowsNewAssistantID：多轮 agent-loop 回归。
// step-start/step-finish 必须把 assistantID 换到新一轮 messageID，
// 否则第二轮的 text delta 与 step-finish(stop) 会被 pump 过滤，终态 result 为空。
func TestMapToHighEvent_MultiRoundFollowsNewAssistantID(t *testing.T) {
	var assistantID string
	parts := partTracker{}

	stepStart := func(mid string) {
		t.Helper()
		he, emit, term := mapToHighEvent(partUpdatedEvent(PartUpdatedData{
			SessionID: "ses_1",
			Part:      Part{ID: "prt_s_" + mid, MessageID: mid, Type: "step-start"},
		}), &assistantID, parts)
		if !emit || term || he.Kind() != HighEventStepStart {
			t.Fatalf("step-start %s: %+v emit=%v term=%v", mid, he, emit, term)
		}
	}
	stepFinish := func(mid, reason string) (HighEvent, bool) {
		t.Helper()
		he, emit, term := mapToHighEvent(partUpdatedEvent(PartUpdatedData{
			SessionID: "ses_1",
			Part:      Part{ID: "prt_f_" + mid, MessageID: mid, Type: "step-finish", Reason: reason},
		}), &assistantID, parts)
		if !emit {
			t.Fatalf("step-finish %s not emitted", mid)
		}
		return he, term
	}

	// 第一轮 A
	stepStart("msg_a")
	if _, term := stepFinish("msg_a", "tool-calls"); term {
		t.Fatal("tool-calls step-finish must not terminate")
	}
	// 第二轮 B：step-start 跟随新 messageID
	stepStart("msg_b")
	if assistantID != "msg_b" {
		t.Fatalf("assistantID = %q, want follow to msg_b", assistantID)
	}
	// 第二轮 text delta 必须映射为 text（pump 过滤以 assistantID 为准）
	he, emit, _ := mapToHighEvent(Event{Type: EventMessagePartDelta, Properties: jsonRaw(mustJSON(
		PartDeltaData{SessionID: "ses_1", MessageID: "msg_b", PartID: "prt_t_b", Field: "text", Delta: "第二轮"}))}, &assistantID, parts)
	if !emit || he.Kind() != HighEventText || he.Text() != "第二轮" {
		t.Errorf("round-B delta: %+v emit=%v", he, emit)
	}
	// 第二轮 step-finish(stop) 必须是终止 result
	he, term := stepFinish("msg_b", "stop")
	if !term || he.Kind() != HighEventResult || he.MessageID() != "msg_b" {
		t.Errorf("round-B finish: %+v term=%v", he, term)
	}
}

// TestMapToHighEvent_StepFollowIdempotent：同一 messageID 多 step（工具+文本同轮）
// 重复跟随是幂等的，assistantID 不变。
func TestMapToHighEvent_StepFollowIdempotent(t *testing.T) {
	var assistantID string
	parts := partTracker{}
	for i := 0; i < 3; i++ {
		mapToHighEvent(partUpdatedEvent(PartUpdatedData{
			SessionID: "ses_1",
			Part:      Part{ID: "prt_s", MessageID: "msg_a", Type: "step-start"},
		}), &assistantID, parts)
	}
	if assistantID != "msg_a" {
		t.Errorf("assistantID = %q, want msg_a", assistantID)
	}
}

// TestMapToHighEvent_ToolDeltaKeepOneWayLock：tool/delta 分支保持单向锁定，
// 不跟随新 messageID（delta 可能先于 part.updated 到达，跟随会丢首轮文本）。
func TestMapToHighEvent_ToolDeltaKeepOneWayLock(t *testing.T) {
	var assistantID string
	parts := partTracker{}
	mapToHighEvent(partUpdatedEvent(PartUpdatedData{
		SessionID: "ses_1",
		Part:      Part{ID: "prt_s", MessageID: "msg_a", Type: "step-start"},
	}), &assistantID, parts)

	// tool part 携带其他 messageID：不跟随
	mapToHighEvent(partUpdatedEvent(PartUpdatedData{SessionID: "ses_1", Part: Part{
		ID: "prt_t", MessageID: "msg_x", Type: "tool", Tool: "bash",
		State: &ToolState{Status: "running"},
	}}), &assistantID, parts)
	// delta 携带其他 messageID：不跟随
	mapToHighEvent(Event{Type: EventMessagePartDelta, Properties: jsonRaw(mustJSON(
		PartDeltaData{SessionID: "ses_1", MessageID: "msg_y", PartID: "prt_t1", Delta: "d"}))}, &assistantID, parts)

	if assistantID != "msg_a" {
		t.Errorf("assistantID = %q, want one-way lock on msg_a", assistantID)
	}
}

// TestMapToHighEvent_SessionError：session.error 的服务端错误文本必须落在 text 字段，
// 对齐 lark-bridge 旧 CLI 版 {kind:EventError, text:msg} 约定；调用方 ev.Text()
// 直接拿到错误（quota/auth/工具详情），不走通用 fallback。
// 回归 BUG-1：旧版把 formatErrorMap 塞进 result，text 恒空。
func TestMapToHighEvent_SessionError(t *testing.T) {
	var assistantID string
	parts := partTracker{}
	ev := Event{Type: EventSessionError, Properties: jsonRaw(`{"sessionID":"ses_1","error":{"message":"quota exceeded","code":429}}`)}
	he, emit, term := mapToHighEvent(ev, &assistantID, parts)
	if !emit || !term || he.Kind() != HighEventError || !he.IsError() {
		t.Fatalf("session.error: %+v emit=%v term=%v", he, emit, term)
	}
	if got := he.Text(); got != "quota exceeded" {
		t.Errorf("Text() = %q, want %q (服务端错误文本必须落到 text)", got, "quota exceeded")
	}
	if got := he.Result(); got != "" {
		t.Errorf("Result() = %q, want empty（Error 事件不应在 result 携带文本）", got)
	}
}
