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
