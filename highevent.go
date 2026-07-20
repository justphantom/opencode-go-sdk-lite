package opencode

import "encoding/json"

// HighEventKind 是高层事件的语义类别，对齐 lark-bridge event.go 的 10 个 kind。
// 不同于原始 Event（88 种 type 字符串），HighEvent 把过程流归纳为少数可消费类别。
type HighEventKind string

const (
	HighEventPrompt     HighEventKind = "prompt"      // Run 首事件，携带 user messageID
	HighEventText       HighEventKind = "text"        // 文本增量
	HighEventThinking   HighEventKind = "thinking"    // 思考增量
	HighEventToolUse    HighEventKind = "tool_use"    // 工具调用发起
	HighEventToolResult HighEventKind = "tool_result" // 工具调用结果
	HighEventStepStart  HighEventKind = "step_start"
	HighEventStepFinish HighEventKind = "step_finish"
	HighEventResult     HighEventKind = "result" // 终止-成功
	HighEventError      HighEventKind = "error"  // 终止-失败
)

// HighEvent 是 Run 对外暴露的高层事件。字段非导出，用 Getter 访问，
// 对齐 lark-bridge 接入约定（bridge 零转换接入）。
type HighEvent struct {
	kind         HighEventKind
	sessionID    string
	messageID    string // assistantMessageID；HighEventPrompt 里是 user messageID
	text         string
	toolName     string
	toolInput    string
	isToolError  bool
	result       string
	isError      bool
	inputTokens  int
	outputTokens int
	cacheRead    int
	cacheWrite   int
	cost         float64
}

// Getter
func (e HighEvent) Kind() HighEventKind { return e.kind }
func (e HighEvent) SessionID() string   { return e.sessionID }
func (e HighEvent) MessageID() string   { return e.messageID }
func (e HighEvent) Text() string        { return e.text }
func (e HighEvent) ToolName() string    { return e.toolName }
func (e HighEvent) ToolInput() string   { return e.toolInput }
func (e HighEvent) IsToolError() bool   { return e.isToolError }
func (e HighEvent) Result() string      { return e.result }
func (e HighEvent) IsError() bool       { return e.isError }
func (e HighEvent) InputTokens() int    { return e.inputTokens }
func (e HighEvent) OutputTokens() int   { return e.outputTokens }
func (e HighEvent) CacheRead() int      { return e.cacheRead }
func (e HighEvent) CacheWrite() int     { return e.cacheWrite }
func (e HighEvent) Cost() float64       { return e.cost }

// mapToHighEvent 把原始 Event 映射为 HighEvent。
// 返回 ok=false 表示该原始事件不产生高层事件（如 location-only、心跳等）。
// isTerminal 标记终止事件（result/error），调用方据此 close chan。
//
// 映射依据实测：真实服务端发 session.next.* 而非 message.part.*。
// 完成信号是 session.next.step.ended 且 data.finish="stop"（不是 session.idle）。
func mapToHighEvent(ev Event, assistantID *string) (HighEvent, bool, bool) {
	switch ev.Type {
	case EventSessionNextTextDelta:
		var d TextDeltaData
		if err := json.Unmarshal(ev.Data, &d); err != nil {
			return HighEvent{}, false, false
		}
		trackAssistantID(assistantID, d.AssistantMessageID)
		return HighEvent{kind: HighEventText, sessionID: d.SessionID, messageID: d.AssistantMessageID, text: d.Delta}, true, false

	case EventSessionNextReasoningDelta:
		var d struct {
			SessionID          string `json:"sessionID"`
			AssistantMessageID string `json:"assistantMessageID"`
			Delta              string `json:"delta"`
		}
		if err := json.Unmarshal(ev.Data, &d); err != nil {
			return HighEvent{}, false, false
		}
		trackAssistantID(assistantID, d.AssistantMessageID)
		return HighEvent{kind: HighEventThinking, sessionID: d.SessionID, messageID: d.AssistantMessageID, text: d.Delta}, true, false

	case EventSessionNextStepStarted:
		var d struct {
			SessionID          string `json:"sessionID"`
			AssistantMessageID string `json:"assistantMessageID"`
		}
		_ = json.Unmarshal(ev.Data, &d)
		trackAssistantID(assistantID, d.AssistantMessageID)
		return HighEvent{kind: HighEventStepStart, sessionID: d.SessionID, messageID: d.AssistantMessageID}, true, false

	case EventSessionNextToolCalled:
		var d ToolCalledData
		if err := json.Unmarshal(ev.Data, &d); err != nil {
			return HighEvent{}, false, false
		}
		trackAssistantID(assistantID, d.AssistantMessageID)
		return HighEvent{
			kind:      HighEventToolUse,
			sessionID: d.SessionID,
			messageID: d.AssistantMessageID,
			toolName:  d.Tool,
			toolInput: string(jsonRawOrNil(d.Input)),
		}, true, false

	case EventSessionNextToolSuccess:
		var d ToolSuccessData
		if err := json.Unmarshal(ev.Data, &d); err != nil {
			return HighEvent{}, false, false
		}
		trackAssistantID(assistantID, d.AssistantMessageID)
		return HighEvent{
			kind:      HighEventToolResult,
			sessionID: d.SessionID,
			messageID: d.AssistantMessageID,
		}, true, false

	case EventSessionNextToolFailed:
		var d ToolFailedData
		if err := json.Unmarshal(ev.Data, &d); err != nil {
			return HighEvent{}, false, false
		}
		trackAssistantID(assistantID, d.AssistantMessageID)
		return HighEvent{
			kind:        HighEventToolResult,
			sessionID:   d.SessionID,
			messageID:   d.AssistantMessageID,
			isToolError: true,
		}, true, false

	case EventSessionNextStepEnded:
		var d StepEndedData
		if err := json.Unmarshal(ev.Data, &d); err != nil {
			return HighEvent{}, false, false
		}
		trackAssistantID(assistantID, d.AssistantMessageID)
		// finish="stop" 是成功终止；其他 finish 值（如 length/tooluns）按 step_finish 报告。
		// result 字段在此留空：终止事件本身不携带 assistant 输出文本，
		// 由 pump 在关闭 chan 前回填累积的 text delta（见 Client.pump）。
		if d.Finish == "stop" || d.Finish == "" {
			return HighEvent{
				kind:         HighEventResult,
				sessionID:    d.SessionID,
				messageID:    d.AssistantMessageID,
				inputTokens:  int(d.Tokens.Input),
				outputTokens: int(d.Tokens.Output),
				cacheRead:    int(d.Tokens.Cache.Read),
				cacheWrite:   int(d.Tokens.Cache.Write),
				cost:         d.Cost,
			}, true, true
		}
		return HighEvent{
			kind:         HighEventStepFinish,
			sessionID:    d.SessionID,
			messageID:    d.AssistantMessageID,
			result:       d.Finish,
			inputTokens:  int(d.Tokens.Input),
			outputTokens: int(d.Tokens.Output),
			cacheRead:    int(d.Tokens.Cache.Read),
			cacheWrite:   int(d.Tokens.Cache.Write),
			cost:         d.Cost,
		}, true, false

	case EventSessionNextStepFailed:
		var d struct {
			SessionID          string         `json:"sessionID"`
			AssistantMessageID string         `json:"assistantMessageID"`
			Error              map[string]any `json:"error"`
		}
		_ = json.Unmarshal(ev.Data, &d)
		trackAssistantID(assistantID, d.AssistantMessageID)
		return HighEvent{
			kind:      HighEventError,
			sessionID: d.SessionID,
			messageID: d.AssistantMessageID,
			isError:   true,
		}, true, true

	case EventSessionError:
		var d SessionErrorData
		_ = json.Unmarshal(ev.Data, &d)
		return HighEvent{kind: HighEventError, sessionID: d.SessionID, isError: true}, true, true
	}
	return HighEvent{}, false, false
}

func trackAssistantID(p *string, id string) {
	if p == nil || id == "" {
		return
	}
	if *p == "" {
		*p = id
	}
}

func jsonRawOrNil(m map[string]any) []byte {
	if len(m) == 0 {
		return nil
	}
	b, err := json.Marshal(m)
	if err != nil {
		return nil
	}
	return b
}
