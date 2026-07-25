package opencode

import (
	"context"
	"strings"
	"time"
)

// drainGrace 是 pump 在 ctx 取消后等待 src 投递飞行中终止事件的宽限时间。
// 仅在 src 当前为空时启用——src 有事件直接走 drainSrcOnExit。
// 0 = 关闭宽限（立即退出）。var 便于测试 shrink；WithDrainGrace 注入到 Client.drainGrace 覆盖。
var drainGrace = 500 * time.Millisecond

// effectiveDrainGrace 返回 Client 配置的 drainGrace，否则回退包级默认。
func (c *Client) effectiveDrainGrace() time.Duration {
	if c.drainGrace != 0 {
		return c.drainGrace
	}
	return drainGrace
}

// processOneEvent 处理单个 src 事件转 HighEvent 投 out。
// pump 主循环、drainSrcOnExit、waitForTerminalInGrace 共用——保证三路行为一致
// （asked 去重、todo 去重、HighEventResult 回填 finalText/finalReasoning）。
// 返回 (terminal, outFull)：terminal=true 表示已投出终止事件；outFull=true 表示 out 满放弃。
func (c *Client) processOneEvent(
	ev Event,
	out chan<- HighEvent,
	sessionID, assistantID string,
	parts partTracker,
	asked *askedTracker,
	lastTodo *string,
	accText *strings.Builder,
	accThinking *strings.Builder,
) (terminal, outFull bool) {
	he, emit, term := mapToHighEvent(ev, &assistantID, parts)
	if !emit {
		return false, false
	}
	if !asked.register(&he) {
		return false, false
	}
	if !registerTodo(&he, lastTodo) {
		return false, false
	}
	// 累积 + 终止帧覆盖（与 pump 主循环一致）
	if he.Kind() == HighEventText {
		accText.WriteString(he.Text())
	} else if he.Kind() == HighEventThinking {
		accThinking.WriteString(he.Text())
	} else if he.Kind() == HighEventThinkingDone {
		accThinking.Reset()
		accThinking.WriteString(he.Text())
	}
	if he.Kind() == HighEventResult && he.result == "" {
		he.result = c.finalText(context.Background(), sessionID, assistantID, accText.String())
	}
	if he.Kind() == HighEventResult {
		he.thinking = c.finalReasoning(context.Background(), sessionID, assistantID, accThinking.String())
	}
	select {
	case out <- he:
	default:
		return false, true
	}
	return term, false
}

// drainSrcOnExit 非阻塞读完 src chan 里已到达的事件，转 HighEvent 投到 out。
// 仅在 pump 即将退出（ctx 取消）时调用——救回 dispatch 已投递但 pump 未消费的事件。
// 不等待新事件到达（src 后续事件由 Unsubscribe 关 chan 后丢弃）。
func (c *Client) drainSrcOnExit(
	src <-chan Event,
	out chan<- HighEvent,
	sessionID, assistantID string,
	parts partTracker,
	asked *askedTracker,
	lastTodo *string,
	accText *strings.Builder,
	accThinking *strings.Builder,
) (gotTerminal bool) {
	for {
		select {
		case ev, ok := <-src:
			if !ok {
				return
			}
			term, _ := c.processOneEvent(ev, out, sessionID, assistantID, parts, asked, lastTodo, accText, accThinking)
			if term {
				return true
			}
		default:
			return
		}
	}
}

// waitForTerminalInGrace 在 drainGrace 内阻塞等 src 的飞行中终止事件。
// 仅在 drainSrcOnExit 未取到终止事件（src 当前空）时调用：开 drainGrace 窗口，
// 期间 src 到达的事件经 processOneEvent 处理；取到终止事件或超时则返回。
// grace<=0 时立即返回（drainGrace 被禁用）。
func (c *Client) waitForTerminalInGrace(
	src <-chan Event,
	out chan<- HighEvent,
	sessionID, assistantID string,
	parts partTracker,
	asked *askedTracker,
	lastTodo *string,
	accText *strings.Builder,
	accThinking *strings.Builder,
	grace time.Duration,
) (gotTerminal bool) {
	if grace <= 0 {
		return false
	}
	timer := time.NewTimer(grace)
	defer timer.Stop()
	for {
		select {
		case <-timer.C:
			return false
		case ev, ok := <-src:
			if !ok {
				return false
			}
			term, _ := c.processOneEvent(ev, out, sessionID, assistantID, parts, asked, lastTodo, accText, accThinking)
			if term {
				return true
			}
		}
	}
}

// RunHandle 封装 Run 的输出 chan 与主动等终止事件的能力。
// 订阅者 ctx 取消后，调 WaitTerminal 显式多等一段以接住飞行中的终止事件
// （配合 pump 内部 drainSrcOnExit + drainGrace 双层兜底）。
type RunHandle struct {
	events <-chan HighEvent
}

// Events 返回事件 chan，可直接 for-range（兼容旧 Run 调用形态）。
func (h *RunHandle) Events() <-chan HighEvent { return h.events }

// WaitTerminal 阻塞等终止事件（HighEventResult / HighEventError）或 ctx 取消/chan close。
// 用于订阅者 ctx 取消后，明确"再多等 N 时长"以接住飞行中的终止事件。
// 返回 (事件, true) 表示取到终止事件；返回 (零值, false) 表示 ctx 超时或 chan 已 close。
func (h *RunHandle) WaitTerminal(ctx context.Context) (HighEvent, bool) {
	for {
		select {
		case <-ctx.Done():
			return HighEvent{}, false
		case ev, ok := <-h.events:
			if !ok {
				return HighEvent{}, false
			}
			if ev.Kind() == HighEventResult || ev.Kind() == HighEventError {
				return ev, true
			}
		}
	}
}
