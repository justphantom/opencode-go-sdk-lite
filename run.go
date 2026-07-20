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
		Parts: []PromptPart{{Type: "text", Text: opts.Prompt}},
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
// userMessageID 用于首事件 HighEventPrompt；assistantID 锁定首轮 part 事件的 assistantMessageID。
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

	for {
		select {
		case <-ctx.Done():
			c.fireAndForgetAbort(sessionID)
			out <- HighEvent{kind: HighEventError, sessionID: sessionID, messageID: assistantID, isError: true}
			return
		case ev, ok := <-src:
			if !ok {
				// 流被关闭（stream.Close 或 Unsubscribe 由别处触发）；兜底终止
				out <- HighEvent{kind: HighEventError, sessionID: sessionID, messageID: assistantID, isError: true, result: "stream closed"}
				return
			}
			he, emit, terminal := mapToHighEvent(ev, &assistantID)
			if !emit {
				continue
			}
			// messageID 过滤：丢弃不属于本 turn 的 part 事件
			if he.MessageID() != "" && assistantID != "" && he.MessageID() != assistantID {
				continue
			}

			// 累积 assistant 输出文本，供终止事件回填 result。
			if he.Kind() == HighEventText {
				accText.WriteString(he.Text())
			}
			// HighEventResult 的 result 字段由 mapToHighEvent 留空（finish 不是
			// 输出文本），此处用累积的 text delta 填充，让消费方拿到的
			// Result() 是助手回复本体而非 "stop" 这类终止原因。
			if he.Kind() == HighEventResult && he.result == "" {
				he.result = accText.String()
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

// fireAndForgetAbort ctx 取消时尽力通知服务端中断，超时即放弃。
func (c *Client) fireAndForgetAbort(sessionID string) {
	abCtx, cancel := context.WithTimeout(context.Background(), runAbortTimeout)
	defer cancel()
	_ = c.Interrupt(abCtx, sessionID)
}
