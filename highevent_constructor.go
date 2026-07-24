package opencode

// HighEventOpt 配置 HighEvent 的可选字段。仅用于测试构造，业务路径走 mapToHighEvent。
type HighEventOpt func(*HighEvent)

// NewHighEvent 构造一个 HighEvent，仅暴露测试需要的能力。
// kind/sessionID/messageID 是必填；其余字段经 opts 设置。
//
// 业务代码不应调用本函数；它是为了让外部包（如 bridge 的 fake）能够在
// 测试中模拟 SDK 在真实 SSE 流里产生的事件。
func NewHighEvent(kind HighEventKind, sessionID, messageID string, opts ...HighEventOpt) HighEvent {
	e := HighEvent{kind: kind, sessionID: sessionID, messageID: messageID}
	for _, opt := range opts {
		opt(&e)
	}
	return e
}

// WithText 设置 text 字段（text/thinking 增量、tool_result 输出等）。
func WithText(s string) HighEventOpt {
	return func(e *HighEvent) { e.text = s }
}

// WithTool 设置 toolName + toolInput（tool_use 事件）。
func WithTool(name, input string) HighEventOpt {
	return func(e *HighEvent) { e.toolName = name; e.toolInput = input }
}

// WithToolError 标记 tool_result 为失败。
func WithToolError() HighEventOpt {
	return func(e *HighEvent) { e.isToolError = true }
}

// WithResult 设置终止事件的最终文本（result/error）。
func WithResult(s string) HighEventOpt {
	return func(e *HighEvent) { e.result = s }
}

// WithError 标记 HighEventError 的 isError。
func WithError() HighEventOpt {
	return func(e *HighEvent) { e.isError = true }
}

// WithTokens 设置 input/output/cacheRead/cacheWrite（step_finish/result）。
func WithTokens(in, out, cacheRead, cacheWrite int) HighEventOpt {
	return func(e *HighEvent) {
		e.inputTokens = in
		e.outputTokens = out
		e.cacheRead = cacheRead
		e.cacheWrite = cacheWrite
	}
}

// WithCost 设置 USD cost（step_finish/result）。
func WithCost(c float64) HighEventOpt {
	return func(e *HighEvent) { e.cost = c }
}

// WithTodoUpdated 设置 todo.updated payload（仅 HighEventTodoUpdated 用）。
func WithTodoUpdated(d *TodoUpdatedData) HighEventOpt {
	return func(e *HighEvent) { e.todo = d }
}

// WithPermissionAsked 设置 permission.asked payload（仅 HighEventPermissionAsked 用）。
func WithPermissionAsked(d *PermissionAskedData) HighEventOpt {
	return func(e *HighEvent) { e.permission = d }
}

// WithQuestionAsked 设置 question.asked payload（仅 HighEventQuestionAsked 用）。
func WithQuestionAsked(d *QuestionAskedData) HighEventOpt {
	return func(e *HighEvent) { e.question = d }
}
