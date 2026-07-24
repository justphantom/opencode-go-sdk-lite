package opencode

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// RunOptions 是 Run 的高层参数。SessionID 空则内部 CreateSession。
type RunOptions struct {
	Prompt    string
	SessionID string
	Model     *ModelRef
	Agent     string
	Location  *LocationRef // directory 在此
}

const (
	runAbortTimeout = 5 * time.Second
)

// pollAskedInterval / pollAskedToolDelay 控制 asked 补偿轮询。
// 用 var 便于测试 shrink（对齐 heartbeatTimeout 模式）。
var (
	pollAskedInterval  = 60 * time.Second
	pollAskedToolDelay = 3 * time.Second
)

// Run 执行一轮对话：建/复用 session → 订阅全局流 → 发 prompt_async →
// 按 assistantMessageID 过滤 → 合成终止事件 → close chan。
//
// 首事件必为 HighEventPrompt（携带 sessionID + user messageID）。
// channel close 前必有 HighEventResult 或 HighEventError（除非 ctx 取消）。
//
// stream 必须是已启动的 GlobalEventStream；Run 会 Subscribe(sessionID) 后 Unsubscribe。
// Agent/Model 随本条消息生效（V1 无 Switch 接口）。
func (c *Client) Run(ctx context.Context, stream *GlobalEventStream, opts RunOptions) (<-chan HighEvent, error) {
	if stream == nil {
		return nil, fmt.Errorf("opencode: stream is nil")
	}
	if opts.Prompt == "" {
		return nil, fmt.Errorf("opencode: prompt is required")
	}

	sessionID := opts.SessionID
	if sessionID == "" {
		req := &CreateSessionReq{Agent: opts.Agent, Model: opts.Model}
		if opts.Location != nil {
			req.Directory = opts.Location.Directory
			req.WorkspaceID = opts.Location.WorkspaceID
		}
		ses, err := c.CreateSession(ctx, req)
		if err != nil {
			return nil, err
		}
		sessionID = ses.ID
	}

	// prompt_async 返 204 无 body，messageID 由 SDK 生成并经 ack 回传
	req := &PromptReq{
		Agent: opts.Agent,
		Model: opts.Model,
		Parts: []PromptPart{{Type: PartTypeText, Text: opts.Prompt}},
	}

	// 先订阅再发 prompt，避免漏掉 turn 的首帧事件
	ch := stream.Subscribe(sessionID)

	ack, err := c.Prompt(ctx, sessionID, req)
	if err != nil {
		stream.Unsubscribe(sessionID)
		return nil, err
	}

	out := make(chan HighEvent, 16)
	go c.pump(ctx, stream, sessionID, ack.MessageID, ch, out)
	return out, nil
}

// pump 把原始 Event 流转换为 HighEvent 流。
// userMessageID 用于首事件 HighEventPrompt；assistantID 跟随最新 step 的
// assistantMessageID（多轮 agent-loop 每轮换 messageID，见 followAssistantID）。
func (c *Client) pump(ctx context.Context, stream *GlobalEventStream, sessionID, userMessageID string, src <-chan Event, out chan<- HighEvent) {
	defer close(out)
	defer stream.Unsubscribe(sessionID)
	defer recoverPanic("Client.pump")

	// 首事件：HighEventPrompt，携带 user messageID
	select {
	case <-ctx.Done():
		c.fireAndForgetAbort(sessionID)
		out <- HighEvent{kind: HighEventError, sessionID: sessionID, messageID: userMessageID, isError: true}
		return
	case out <- HighEvent{kind: HighEventPrompt, sessionID: sessionID, messageID: userMessageID}:
	}

	var assistantID string
	var accText strings.Builder
	parts := partTracker{}

	// asked 补偿轮询：全局 /event 无续传，断连窗口会丢 permission.asked/
	// question.asked，turn 随即永久悬挂。靠 REST pending 列表兜底——asked 在
	// 服务端一直挂起直到 reply/reject，SSE 丢了也能捞回。三触发：
	//   - 首次立即：覆盖订阅前已发出的极端窗口
	//   - 每 pollAskedInterval：被动兜底
	//   - 工具相关事件后 pollAskedToolDelay：asked 紧随工具调用，缩短补偿延迟
	// seenAsked 按 requestID 去重，SSE 与轮询任一先到即登记。
	seenAsked := make(map[string]bool)
	pollTicker := time.NewTicker(pollAskedInterval)
	defer pollTicker.Stop()
	compensateTimer := time.NewTimer(pollAskedToolDelay)
	compensateTimer.Stop() // 默认 disarm，由 tool 事件 armTimer 触发
	defer compensateTimer.Stop()
	// 首次立即轮询一次
	c.pollAsked(ctx, sessionID, seenAsked, out)

	for {
		select {
		case <-ctx.Done():
			c.fireAndForgetAbort(sessionID)
			out <- HighEvent{kind: HighEventError, sessionID: sessionID, messageID: assistantID, isError: true}
			return
		case <-pollTicker.C:
			c.pollAsked(ctx, sessionID, seenAsked, out)
		case <-compensateTimer.C:
			c.pollAsked(ctx, sessionID, seenAsked, out)
		case ev, ok := <-src:
			if !ok {
				// 流被关闭（stream.Close 或 Unsubscribe 由别处触发）；兜底终止。
				// 文本走 text 字段，与 Error 事件约定一致。
				out <- HighEvent{kind: HighEventError, sessionID: sessionID, messageID: assistantID, isError: true, text: "stream closed"}
				return
			}
			he, emit, terminal := mapToHighEvent(ev, &assistantID, parts)
			if !emit {
				continue
			}
			// messageID 过滤：丢弃不属于本 turn 的 part 事件
			if he.MessageID() != "" && assistantID != "" && he.MessageID() != assistantID {
				continue
			}
			// asked 去重：SSE 与轮询任一先到即登记，后到丢弃，避免重复投递
			if !registerAsked(&he, seenAsked) {
				continue
			}
			// 工具相关事件后触发补偿轮询（asked 紧随工具调用，主动缩短补偿延迟）
			if he.Kind() == HighEventToolUse ||
				(he.Kind() == HighEventStepFinish && he.Result() == "tool-calls") {
				armTimer(compensateTimer, pollAskedToolDelay)
			}

			// 累积 assistant 输出文本，供终止事件回填 result。
			if he.Kind() == HighEventText {
				accText.WriteString(he.Text())
			}
			// HighEventResult 的 result 字段由 mapToHighEvent 留空（finish 不是
			// 输出文本）。优先取服务端落库文本（GET message 的 FinalText，
			// 免疫 SSE 丢帧）；取不到/为空则回退累积的 text delta。
			if he.Kind() == HighEventResult && he.result == "" {
				he.result = c.finalText(ctx, sessionID, assistantID, accText.String())
			}

			select {
			case <-ctx.Done():
				c.fireAndForgetAbort(sessionID)
				out <- HighEvent{kind: HighEventError, sessionID: sessionID, messageID: assistantID, isError: true}
				return
			case out <- he:
			}
			if terminal {
				return
			}
		}
	}
}

// finalText 返回 turn 的最终回复：优先服务端落库文本，失败回退 accumulated。
func (c *Client) finalText(ctx context.Context, sessionID, assistantID, accumulated string) string {
	if assistantID != "" {
		if m, err := c.GetMessage(ctx, sessionID, assistantID); err == nil {
			if t := m.FinalText(); t != "" {
				return t
			}
		}
	}
	return accumulated
}

// fireAndForgetAbort ctx 取消时尽力通知服务端中断，超时即放弃。
// 用 context.Background() 派生新 ctx 而非传入父 ctx：调用方此时 ctx 已取消，
// 传进来会让 Interrupt 立即失败，违背 fire-and-forget 语义。
//
//nolint:contextcheck // 有意使用 Background 派生，确保 Interrupt 不被已取消的父 ctx 拖死
func (c *Client) fireAndForgetAbort(sessionID string) {
	abCtx, cancel := context.WithTimeout(context.Background(), runAbortTimeout)
	defer cancel()
	_ = c.Interrupt(abCtx, sessionID)
}

// pollAsked 补偿轮询服务端 pending 列表，捞回 SSE 丢失的 asked 事件。
// 按 requestID 去重（seen）；轮询失败（网络/4xx）吞掉，不终止 pump——
// 补偿路径自身不可成为 turn 失败的原因。
func (c *Client) pollAsked(ctx context.Context, sessionID string, seen map[string]bool, out chan<- HighEvent) {
	emit := func(id string, he HighEvent) {
		if id == "" || seen[id] {
			return
		}
		seen[id] = true
		select {
		case <-ctx.Done():
		case out <- he:
		}
	}
	if perms, err := c.ListPermissions(ctx, sessionID); err == nil {
		for _, p := range perms {
			d := PermissionAskedData{
				ID: p.ID, SessionID: p.SessionID, Permission: p.Permission,
				Patterns: p.Patterns, Metadata: p.Metadata, Always: p.Always, Tool: p.Tool,
			}
			emit(p.ID, HighEvent{kind: HighEventPermissionAsked, sessionID: p.SessionID, permission: &d})
		}
	}
	if qs, err := c.ListQuestions(ctx, sessionID); err == nil {
		for _, q := range qs {
			d := QuestionAskedData{
				ID: q.ID, SessionID: q.SessionID, Questions: q.Questions, Tool: q.Tool,
			}
			emit(q.ID, HighEvent{kind: HighEventQuestionAsked, sessionID: q.SessionID, question: &d})
		}
	}
}

// registerAsked 登记并去重 SSE 来源的 asked 事件。返回 false 表示已见过应丢弃。
func registerAsked(he *HighEvent, seen map[string]bool) bool {
	switch he.kind {
	case HighEventPermissionAsked:
		if he.permission != nil {
			if seen[he.permission.ID] {
				return false
			}
			seen[he.permission.ID] = true
		}
	case HighEventQuestionAsked:
		if he.question != nil {
			if seen[he.question.ID] {
				return false
			}
			seen[he.question.ID] = true
		}
	}
	return true
}

// armTimer 安全重置 timer：先 Stop 并排空已触发的值，再 Reset，规避 time.Reset 的重入风险。
func armTimer(t *time.Timer, d time.Duration) {
	if !t.Stop() {
		select {
		case <-t.C:
		default:
		}
	}
	t.Reset(d)
}
