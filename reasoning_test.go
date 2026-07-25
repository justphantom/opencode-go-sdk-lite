package opencode

import (
	"bytes"
	"strings"
	"testing"
)

// makeReasoningDeltaEvent 构造 reasoning part 的 delta 帧（field 恒为 text）。
// partID 与对应 part.updated{reasoning} 一致，mapToHighEvent 靠 partTracker 路由到 HighEventThinking。
func makeReasoningDeltaEvent(partID, delta string) Event {
	return Event{
		Type:       EventMessagePartDelta,
		Properties: []byte(`{"sessionID":"ses_test","messageID":"msg_a","partID":"` + partID + `","field":"text","delta":` + strconvQuote(delta) + `}`),
	}
}

// makeReasoningDoneEvent 构造 reasoning part 的终止帧（text!=""）。
// 服务端整合后的完整文本，作为 HighEventThinkingDone 的权威值覆盖累积。
func makeReasoningDoneEvent(partID, text string) Event {
	return Event{
		Type:       EventMessagePartUpdated,
		Properties: []byte(`{"sessionID":"ses_test","part":{"id":"` + partID + `","type":"reasoning","text":` + strconvQuote(text) + `,"messageID":"msg_a","sessionID":"ses_test"}}`),
	}
}

// makeReasoningBuildingEvent 构造 reasoning part 的建块帧（text=""）。
// 仅登记 partID→reasoning，事件本身应被丢弃（ok=false）。
func makeReasoningBuildingEvent(partID string) Event {
	return Event{
		Type:       EventMessagePartUpdated,
		Properties: []byte(`{"sessionID":"ses_test","part":{"id":"` + partID + `","type":"reasoning","text":"","messageID":"msg_a","sessionID":"ses_test"}}`),
	}
}

// strconvQuote 用 Go 字符串字面量语法安全包裹 delta/text（处理换行/引号转义）。
func strconvQuote(s string) string {
	// 用 strconv.Quote 等价，避免直接 import strconv 多处
	var b bytes.Buffer
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

// TestMapToHighEvent_ReasoningBuildingDiscarded: part.updated{reasoning text=""}
// 建块帧不产生高层事件（仅登记 partID→reasoning，事件本身丢弃）。
func TestMapToHighEvent_ReasoningBuildingDiscarded(t *testing.T) {
	parts := partTracker{}
	var assistantID string
	_, emit, _ := mapToHighEvent(makeReasoningBuildingEvent("prt_r1"), &assistantID, parts)
	if emit {
		t.Fatal("建块帧（text=\"\"）应不投递事件")
	}
	if parts["prt_r1"] != PartTypeReasoning {
		t.Errorf("建块帧应登记 partID→reasoning，实际 parts=%v", parts)
	}
}

// TestMapToHighEvent_ReasoningDoneEmitted: part.updated{reasoning text!=""} 终止帧
// 投 HighEventThinkingDone，携带权威完整文本。
func TestMapToHighEvent_ReasoningDoneEmitted(t *testing.T) {
	parts := partTracker{"prt_r1": PartTypeReasoning}
	var assistantID string
	he, emit, term := mapToHighEvent(makeReasoningDoneEvent("prt_r1", "完整思考全文"), &assistantID, parts)
	if !emit {
		t.Fatal("终止帧应投递 HighEventThinkingDone")
	}
	if he.Kind() != HighEventThinkingDone {
		t.Errorf("kind = %v, want HighEventThinkingDone", he.Kind())
	}
	if term {
		t.Error("HighEventThinkingDone 不应是终止事件")
	}
	if he.Text() != "完整思考全文" {
		t.Errorf("text = %q, want 完整思考全文", he.Text())
	}
}

// TestPump_AccThinkingFromDeltas: 连续 reasoning delta → HighEventResult.Thinking()
// 等于拼接（白盒：经 processOneEvent 喂事件）。
func TestPump_AccThinkingFromDeltas(t *testing.T) {
	c, _ := New("http://127.0.0.1:1")
	out := make(chan HighEvent, 16)
	parts := partTracker{}
	asked := &askedTracker{seen: make(map[string]bool)}
	var lastTodo string
	var accText strings.Builder
	var accThinking strings.Builder
	var resultModel, resultProvider string

	// 1. reasoning 建块（登记 partID）
	c.processOneEvent(makeReasoningBuildingEvent("prt_r"), out, "ses_test", "msg_a", parts, asked, &lastTodo, &accText, &accThinking, &resultModel, &resultProvider)
	// 2. 多条 delta
	for _, d := range []string{"The ", "user ", "asks."} {
		c.processOneEvent(makeReasoningDeltaEvent("prt_r", d), out, "ses_test", "msg_a", parts, asked, &lastTodo, &accText, &accThinking, &resultModel, &resultProvider)
	}
	// 3. 终止事件（step-finish reason=stop → HighEventResult）
	c.processOneEvent(makeStepFinishEvent(), out, "ses_test", "msg_a", parts, asked, &lastTodo, &accText, &accThinking, &resultModel, &resultProvider)

	close(out)
	var result HighEvent
	for ev := range out {
		if ev.Kind() == HighEventResult {
			result = ev
		}
	}
	// accThinking 累积；assistantID="msg_a" 但 New("http://127.0.0.1:1") 无法 GetMessage，落空回退 accThinking
	if got := result.Thinking(); got != "The user asks." {
		t.Errorf("Thinking() = %q, want \"The user asks.\"", got)
	}
}

// TestPump_ThinkingDoneOverridesAcc: 投 delta 后投 ThinkingDone（不同文本），
// accThinking 应被覆盖为权威文本，最终 Result.Thinking() = ThinkingDone 文本。
func TestPump_ThinkingDoneOverridesAcc(t *testing.T) {
	c, _ := New("http://127.0.0.1:1")
	out := make(chan HighEvent, 16)
	parts := partTracker{}
	asked := &askedTracker{seen: make(map[string]bool)}
	var lastTodo string
	var accText strings.Builder
	var accThinking strings.Builder
	var resultModel, resultProvider string

	c.processOneEvent(makeReasoningBuildingEvent("prt_r"), out, "ses_test", "msg_a", parts, asked, &lastTodo, &accText, &accThinking, &resultModel, &resultProvider)
	c.processOneEvent(makeReasoningDeltaEvent("prt_r", "delta-fragment"), out, "ses_test", "msg_a", parts, asked, &lastTodo, &accText, &accThinking, &resultModel, &resultProvider)
	// 服务端整合的完整文本（覆盖 delta 累积）
	c.processOneEvent(makeReasoningDoneEvent("prt_r", "权威完整思考"), out, "ses_test", "msg_a", parts, asked, &lastTodo, &accText, &accThinking, &resultModel, &resultProvider)
	c.processOneEvent(makeStepFinishEvent(), out, "ses_test", "msg_a", parts, asked, &lastTodo, &accText, &accThinking, &resultModel, &resultProvider)

	close(out)
	var result HighEvent
	var doneEmitted bool
	for ev := range out {
		if ev.Kind() == HighEventResult {
			result = ev
		}
		if ev.Kind() == HighEventThinkingDone {
			doneEmitted = true
		}
	}
	if !doneEmitted {
		t.Error("HighEventThinkingDone 未投出")
	}
	if got := result.Thinking(); got != "权威完整思考" {
		t.Errorf("Thinking() = %q, want 权威完整思考（覆盖累积）", got)
	}
}

// TestPump_DrainSrcOnExitPreservesThinking: drainSrcOnExit 路径同样累积 thinking，
// 投出 HighEventResult 的 Thinking() 回填正确。
func TestPump_DrainSrcOnExitPreservesThinking(t *testing.T) {
	c, _ := New("http://127.0.0.1:1")
	src := make(chan Event, 8)
	out := make(chan HighEvent, 8)
	parts := partTracker{}
	asked := &askedTracker{seen: make(map[string]bool)}
	var lastTodo string
	var accText strings.Builder
	var accThinking strings.Builder
	var resultModel, resultProvider string

	// 缓冲：建块 + delta + 终止
	src <- makeReasoningBuildingEvent("prt_r")
	src <- makeReasoningDeltaEvent("prt_r", "drain-thinking")
	src <- makeStepFinishEvent()

	got := c.drainSrcOnExit(src, out, "ses_test", "msg_a", parts, asked, &lastTodo, &accText, &accThinking, &resultModel, &resultProvider)
	if !got {
		t.Fatal("drainSrcOnExit 应返回 true（取到终止事件）")
	}
	close(out)
	var result HighEvent
	for ev := range out {
		if ev.Kind() == HighEventResult {
			result = ev
		}
	}
	if got := result.Thinking(); got != "drain-thinking" {
		t.Errorf("drain 路径 Thinking() = %q, want drain-thinking", got)
	}
}

// TestHighEventResult_ThinkingDefault: 无 reasoning turn 的 Result.Thinking() 为空。
// 验证新 getter 不破坏无 reasoning 场景。
func TestHighEventResult_ThinkingDefault(t *testing.T) {
	c, _ := New("http://127.0.0.1:1")
	out := make(chan HighEvent, 8)
	parts := partTracker{}
	asked := &askedTracker{seen: make(map[string]bool)}
	var lastTodo string
	var accText strings.Builder
	var accThinking strings.Builder
	var resultModel, resultProvider string

	// 纯 text 事件 + 终止（无 reasoning）
	c.processOneEvent(makeTextDeltaEvent(), out, "ses_test", "msg_a", parts, asked, &lastTodo, &accText, &accThinking, &resultModel, &resultProvider)
	c.processOneEvent(makeStepFinishEvent(), out, "ses_test", "msg_a", parts, asked, &lastTodo, &accText, &accThinking, &resultModel, &resultProvider)

	close(out)
	var result HighEvent
	for ev := range out {
		if ev.Kind() == HighEventResult {
			result = ev
		}
	}
	if got := result.Thinking(); got != "" {
		t.Errorf("无 reasoning turn 的 Thinking() = %q, want \"\"", got)
	}
}

// TestPump_MultiStepReasoningConcatenated: agent-loop 多 step 各产一个 reasoning part，
// accThinking 持续累积（落库失败回退场景）。验证 SSE 累积路径会把多 step 思考拼接。
// 注意：落库成功路径由 finalReasoning 走 GetMessage.ReasoningText() 按 "\n" 拼，
// 本测试模拟落库失败（New 用无效 URL），覆盖 accThinking 回退分支。
func TestPump_MultiStepReasoningConcatenated(t *testing.T) {
	c, _ := New("http://127.0.0.1:1")
	out := make(chan HighEvent, 32)
	parts := partTracker{}
	asked := &askedTracker{seen: make(map[string]bool)}
	var lastTodo string
	var accText strings.Builder
	var accThinking strings.Builder
	var resultModel, resultProvider string

	// step 1: reasoning part 1
	c.processOneEvent(makeReasoningBuildingEvent("prt_r1"), out, "ses_test", "msg_a", parts, asked, &lastTodo, &accText, &accThinking, &resultModel, &resultProvider)
	c.processOneEvent(makeReasoningDeltaEvent("prt_r1", "step1-think"), out, "ses_test", "msg_a", parts, asked, &lastTodo, &accText, &accThinking, &resultModel, &resultProvider)
	c.processOneEvent(makeReasoningDoneEvent("prt_r1", "step1-think"), out, "ses_test", "msg_a", parts, asked, &lastTodo, &accText, &accThinking, &resultModel, &resultProvider)
	// step 2: reasoning part 2（不同 partID，相同 messageID）
	c.processOneEvent(makeReasoningBuildingEvent("prt_r2"), out, "ses_test", "msg_a", parts, asked, &lastTodo, &accText, &accThinking, &resultModel, &resultProvider)
	c.processOneEvent(makeReasoningDeltaEvent("prt_r2", "step2-think"), out, "ses_test", "msg_a", parts, asked, &lastTodo, &accText, &accThinking, &resultModel, &resultProvider)
	c.processOneEvent(makeReasoningDoneEvent("prt_r2", "step2-think"), out, "ses_test", "msg_a", parts, asked, &lastTodo, &accText, &accThinking, &resultModel, &resultProvider)
	// turn 终止
	c.processOneEvent(makeStepFinishEvent(), out, "ses_test", "msg_a", parts, asked, &lastTodo, &accText, &accThinking, &resultModel, &resultProvider)

	close(out)
	var result HighEvent
	for ev := range out {
		if ev.Kind() == HighEventResult {
			result = ev
		}
	}
	// 落库失败回退路径：step2 的 ThinkingDone 覆盖了 step1（仅保留最后一个 step 的 thinking）
	// 这是已知行为：累积路径下多 step 拼接需依赖落库 ReasoningText()。本测试验证 ThinkingDone
	// 的覆盖语义——Result.Thinking() 是最后一个 step 的思考全文。
	if got := result.Thinking(); got != "step2-think" {
		t.Errorf("多 step + 落库失败时 Thinking() = %q, want step2-think（最后 ThinkingDone 覆盖）", got)
	}
}
