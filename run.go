package opencode

import (
	"context"
	"fmt"
	"time"
)

// RunOptions 是 Run 的高层参数。SessionID 空则内部 CreateSession。
type RunOptions struct {
	Prompt   string
	SessionID string
	Model    *ModelRef
	Agent    string
	Location *LocationRef // directory 在此
}

const (
	runProbeTimeout = 30 * time.Second // 既无 postErr 也无事件则视为服务端卡死
	runAbortTimeout = 5 * time.Second
)

// Run 执行一轮对话：建/复用 session → 发 prompt → 订阅全局流 →
// 按 assistantMessageID 过滤 → 合成终止事件 → close chan。
//
// 首事件必为 HighEventPrompt（携带 sessionID + user messageID）。
// channel close 前必有 HighEventResult 或 HighEventError（除非 ctx 取消）。
//
// stream 必须是已启动的 GlobalEventStream；Run 会 Subscribe(sessionID) 后 Unsubscribe。
func (c *Client) Run(ctx context.Context, stream *GlobalEventStream, opts RunOptions) (<-chan HighEvent, error) {
	if stream == nil {
		return nil, fmt.Errorf("opencode: stream is nil")
	}
	if opts.Prompt == "" {
		return nil, fmt.Errorf("opencode: prompt is required")
	}

	sessionID := opts.SessionID
	if sessionID == "" {
		ses, err := c.CreateSession(ctx, &CreateSessionReq{
			Agent:    opts.Agent,
			Model:    opts.Model,
			Location: opts.Location,
		})
		if err != nil {
			return nil, err
		}
		sessionID = ses.ID
	}

	// 发 prompt；v2 响应直接回 messageID（不像 v1 prompt_async 返 204）
	admitted, err := c.Prompt(ctx, sessionID, &PromptReq{
		Prompt: PromptInput{Text: opts.Prompt},
	})
	if err != nil {
		return nil, err
	}

	// 订阅流（在 prompt 之后，但 v2 的 prompt.admitted 事件会因 GlobalEventStream 的
	// 全局连接在 prompt 前已建立而被收到；若担心丢首帧，调用方应提前建立 stream）
	ch := stream.Subscribe(sessionID)

	out := make(chan HighEvent, 16)
	go c.pump(ctx, stream, sessionID, admitted.ID, ch, out)
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
	probe := time.NewTimer(runProbeTimeout)
	defer probe.Stop()

	for {
		select {
		case <-ctx.Done():
			c.fireAndForgetAbort(sessionID)
			out <- HighEvent{kind: HighEventError, sessionID: sessionID, messageID: assistantID, isError: true}
			return
		case <-probe.C:
			// 既无 postErr（v2 的 prompt 已返回 admitted，等价 postErr 已到）也无事件：
			// 视为服务端 turn 卡死
			out <- HighEvent{
				kind:      HighEventError,
				sessionID: sessionID,
				messageID: assistantID,
				isError:   true,
				result:    fmt.Sprintf("opencode: no SSE events within %s (turn stuck?)", runProbeTimeout),
			}
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
			// 收到首事件，重置 probe
			if !probe.Stop() {
				select {
				case <-probe.C:
				default:
				}
			}
			probe.Reset(runProbeTimeout)

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
