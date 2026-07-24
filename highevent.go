package opencode

import "encoding/json"

// HighEventKind 是高层事件的语义类别（12 种）。
// 不同于原始 Event（V1 经典事件体系），HighEvent 把过程流归纳为少数可消费类别。
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

	// asked 两个事件均为非终止：agent 挂起等用户应答，应答后 turn 继续。
	HighEventPermissionAsked HighEventKind = "permission_asked"
	HighEventQuestionAsked   HighEventKind = "question_asked"

	HighEventTodoUpdated HighEventKind = "todo_updated" // 会话级 todo 全量列表更新
)

// HighEvent 是 Run 对外暴露的高层事件。字段非导出，用 Getter 访问，
// 对齐 lark-bridge 接入约定（bridge 零转换接入）。
type HighEvent struct {
	kind         HighEventKind
	sessionID    string
	messageID    string // assistantMessageID；HighEventPrompt 里是 user messageID
	text         string
	toolName     string
	toolKind     ToolKind
	toolInput    string
	isToolError  bool
	result       string
	isError      bool
	inputTokens  int
	outputTokens int
	cacheRead    int
	cacheWrite   int
	cost         float64
	permission   *PermissionAskedData
	question     *QuestionAskedData
	todo         *TodoUpdatedData // 仅 kind==HighEventTodoUpdated 非 nil
}

// Getter
func (e HighEvent) Kind() HighEventKind { return e.kind }
func (e HighEvent) SessionID() string   { return e.sessionID }
func (e HighEvent) MessageID() string   { return e.messageID }
func (e HighEvent) Text() string        { return e.text }
func (e HighEvent) ToolName() string    { return e.toolName }
func (e HighEvent) ToolKind() ToolKind  { return e.toolKind }
func (e HighEvent) ToolInput() string   { return e.toolInput }
func (e HighEvent) IsToolError() bool   { return e.isToolError }
func (e HighEvent) Result() string      { return e.result }
func (e HighEvent) IsError() bool       { return e.isError }
func (e HighEvent) InputTokens() int    { return e.inputTokens }
func (e HighEvent) OutputTokens() int   { return e.outputTokens }
func (e HighEvent) CacheRead() int      { return e.cacheRead }
func (e HighEvent) CacheWrite() int     { return e.cacheWrite }
func (e HighEvent) Cost() float64       { return e.cost }

// PermissionAsked 仅 kind==HighEventPermissionAsked 时非 nil，其余 kind 返回 nil。
func (e HighEvent) PermissionAsked() *PermissionAskedData { return e.permission }

// QuestionAsked 仅 kind==HighEventQuestionAsked 时非 nil，其余 kind 返回 nil。
func (e HighEvent) QuestionAsked() *QuestionAskedData { return e.question }

// TodoUpdated 仅 kind==HighEventTodoUpdated 时非 nil，其余 kind 返回 nil。
func (e HighEvent) TodoUpdated() *TodoUpdatedData { return e.todo }

// partTracker 记录 partID → part.type，供 message.part.delta 路由
// （delta 事件本身不带 part 类型，实测其 field 恒为 "text"）。
type partTracker map[string]string

// mapAskedEvent 反序列化 permission.asked / question.asked 事件为 HighEvent。
// 供 mapToHighEvent 与子 session forward 共享：两路来源（主订阅 src + 子 session
// 转发）必须产出同构 HighEvent，否则去重与下游消费会失配。
// 返回 ok=false 表示非 asked 事件或解析失败。
func mapAskedEvent(ev Event) (HighEvent, bool) {
	switch ev.Type {
	case EventPermissionAsked:
		var d PermissionAskedData
		if err := json.Unmarshal(ev.Properties, &d); err != nil {
			return HighEvent{}, false
		}
		return HighEvent{kind: HighEventPermissionAsked, sessionID: d.SessionID, permission: &d}, true
	case EventQuestionAsked:
		var d QuestionAskedData
		if err := json.Unmarshal(ev.Properties, &d); err != nil {
			return HighEvent{}, false
		}
		return HighEvent{kind: HighEventQuestionAsked, sessionID: d.SessionID, question: &d}, true
	}
	return HighEvent{}, false
}

// mapToHighEvent 把原始 Event 映射为 HighEvent。
// 返回 ok=false 表示该原始事件不产生高层事件（如 session.updated、心跳等）。
// isTerminal 标记终止事件（result/error），调用方据此 close chan。
//
// 映射依据实测（docs/sse-capture 抓取）：服务端发 V1 经典事件——
// 文本/思考走 message.part.delta（part 类型由 message.part.updated 登记），
// 完成信号是 step-finish part 且 reason="stop"（session.idle 兜底）。
func mapToHighEvent(ev Event, assistantID *string, parts partTracker) (HighEvent, bool, bool) {
	switch ev.Type {
	case EventMessagePartUpdated:
		var d PartUpdatedData
		if err := json.Unmarshal(ev.Properties, &d); err != nil {
			return HighEvent{}, false, false
		}
		p := d.Part
		if p.Type != "" {
			parts[p.ID] = p.Type
		}
		switch p.Type {
		case PartTypeStepStart:
			// 只在 assistant 专属 part 上锁定 assistantID：
			// part.updated 也会回显用户输入的 text part（MessageID 是 user 消息），
			// 抢锁会把后续 assistant delta 全部过滤掉（实测踩坑）。
			followAssistantID(assistantID, p.MessageID)
			return HighEvent{kind: HighEventStepStart, sessionID: d.SessionID, messageID: p.MessageID}, true, false
		case PartTypeStepFinish:
			followAssistantID(assistantID, p.MessageID)
			// reason="stop" 是成功终止；其他 reason（如 tool-calls）按 step_finish 报告。
			// result 字段留空，由 pump 在关闭 chan 前回填累积的 text delta。
			he := HighEvent{
				sessionID:    d.SessionID,
				messageID:    p.MessageID,
				inputTokens:  int(p.Tokens.Input),
				outputTokens: int(p.Tokens.Output),
				cacheRead:    int(p.Tokens.Cache.Read),
				cacheWrite:   int(p.Tokens.Cache.Write),
				cost:         p.Cost,
			}
			if p.Reason == "stop" || p.Reason == "" {
				he.kind = HighEventResult
				return he, true, true
			}
			he.kind = HighEventStepFinish
			he.result = p.Reason
			return he, true, false
		case PartTypeTool:
			trackAssistantID(assistantID, p.MessageID)
			if p.State == nil {
				return HighEvent{}, false, false
			}
			switch p.State.Status {
			case "running":
				return HighEvent{
					kind:      HighEventToolUse,
					sessionID: d.SessionID,
					messageID: p.MessageID,
					toolName:  p.Tool,
					toolKind:  ClassifyTool(p.Tool),
					toolInput: string(jsonRawOrNil(p.State.Input)),
				}, true, false
			case "completed":
				return HighEvent{
					kind:      HighEventToolResult,
					sessionID: d.SessionID,
					messageID: p.MessageID,
					toolName:  p.Tool,
					toolKind:  ClassifyTool(p.Tool),
					text:      p.State.Output,
				}, true, false
			case "error":
				return HighEvent{
					kind:        HighEventToolResult,
					sessionID:   d.SessionID,
					messageID:   p.MessageID,
					toolName:    p.Tool,
					toolKind:    ClassifyTool(p.Tool),
					isToolError: true,
					text:        p.State.Error,
				}, true, false
			}
		}
		return HighEvent{}, false, false

	case EventMessagePartDelta:
		var d PartDeltaData
		if err := json.Unmarshal(ev.Properties, &d); err != nil {
			return HighEvent{}, false, false
		}
		trackAssistantID(assistantID, d.MessageID)
		// part 类型未知时按 text 处理。这看似脆弱（若某 reasoning delta 抢在
		// part.updated 登记前到达会被误判为 text），实则由 part 生命周期保证安全：
		// 服务端必先发 part.updated{type} 建块、再流 delta 进去，单条 /event 长连
		// 又保 TCP 顺序，故「type 登记」先于「delta」是架构必然而非实测凑巧。
		// delta 本身不带 part 类型（field 恒为 "text"，text/reasoning 共用），
		// 无更优默认；真误判也只是把思考增量混进文本流观感，最终回复走 FinalText
		// 落库文本不受污染。改动此默认前须先引入「未知 partID 缓冲待决」机制。
		if parts[d.PartID] == PartTypeReasoning {
			return HighEvent{kind: HighEventThinking, sessionID: d.SessionID, messageID: d.MessageID, text: d.Delta}, true, false
		}
		return HighEvent{kind: HighEventText, sessionID: d.SessionID, messageID: d.MessageID, text: d.Delta}, true, false

	case EventSessionIdle:
		// turn 结束兜底信号；step-finish(reason=stop) 通常先到，此事件主要用于
		// 中断/无 step 的场景。result 由 pump 回填。
		var d SessionIdleData
		_ = json.Unmarshal(ev.Properties, &d)
		return HighEvent{kind: HighEventResult, sessionID: d.SessionID}, true, true

	case EventSessionError:
		var d SessionErrorData
		_ = json.Unmarshal(ev.Properties, &d)
		// 错误文本一律走 text，对齐 lark-bridge 旧 CLI 版 {kind:EventError, text:msg} 约定。
		// 调用方 ev.Text() 直接拿到服务端错误（quota/auth/工具详情），不走通用 fallback。
		return HighEvent{kind: HighEventError, sessionID: d.SessionID, isError: true, text: formatErrorMap(d.Error)}, true, true

	case EventPermissionAsked:
		he, ok := mapAskedEvent(ev)
		if !ok {
			return HighEvent{}, false, false
		}
		// messageID 留空：asked 是 session 级请求，不属于某条 assistant 消息；
		// 空值天然绕过 pump 的 assistantID 过滤（该过滤只丢带其他 messageID 的 part 事件）。
		return he, true, false
	case EventQuestionAsked:
		he, ok := mapAskedEvent(ev)
		if !ok {
			return HighEvent{}, false, false
		}
		return he, true, false

	case EventTodoUpdated:
		var d TodoUpdatedData
		if err := json.Unmarshal(ev.Properties, &d); err != nil {
			return HighEvent{}, false, false
		}
		// messageID 留空（session 级）；非终止（agent 写 todo 后 turn 继续）；
		// 空 Todos 正常上抛（语义=清空）。
		return HighEvent{kind: HighEventTodoUpdated, sessionID: d.SessionID, todo: &d}, true, false
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

// followAssistantID 无条件跟随新一轮 assistant message 起点：step-start/step-finish
// 必属 assistant 且先于该 message 的 text/tool/delta 到达，多轮 agent-loop 下
// assistantID 必须换到当前轮 messageID，否则新轮的 part 事件会被 pump 过滤丢弃。
// tool/delta 不用它：delta 可能先于 part.updated 到达，过早跟随会丢首轮文本。
func followAssistantID(p *string, id string) {
	if p == nil || id == "" {
		return
	}
	*p = id
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

// formatErrorMap 把 error map 拍平成一行可读文本。
// 实测 error 至少含 message；无则回退到 JSON 序列化。
func formatErrorMap(errMap map[string]any) string {
	if len(errMap) == 0 {
		return ""
	}
	if msg, ok := errMap["message"].(string); ok && msg != "" {
		return msg
	}
	if b, err := json.Marshal(errMap); err == nil {
		return string(b)
	}
	return ""
}
