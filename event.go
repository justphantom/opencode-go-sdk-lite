package opencode

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

const (
	defaultBackoffMin = 500 * time.Millisecond
	defaultBackoffMax = 30 * time.Second
)

// SessionEventsOpt 配置 SessionEvents 订阅。
type SessionEventsOpt struct {
	// Location 定位工作区。事件总线按 directory 隔离（实测）：不带 directory
	// 的连接收不到其他 directory 下会话的事件。必须与目标 session 的 directory 一致。
	Location *LocationRef
	// BackoffMin / BackoffMax 限制指数退避区间。零值走默认。
	BackoffMin time.Duration
	BackoffMax time.Duration
	// MaxAttempts 重连尝试上限；0 表示无限重试。
	MaxAttempts int
}

func (o *SessionEventsOpt) normalize() *SessionEventsOpt {
	if o == nil {
		return &SessionEventsOpt{
			BackoffMin: defaultBackoffMin,
			BackoffMax: defaultBackoffMax,
		}
	}
	out := *o
	if out.BackoffMin <= 0 {
		out.BackoffMin = defaultBackoffMin
	}
	if out.BackoffMax <= 0 {
		out.BackoffMax = defaultBackoffMax
	}
	return &out
}

// SessionEvents 订阅会话级事件流，返回事件 chan 与错误 chan。
// V1 无会话级 SSE 端点，实际连接全局 GET /event 后按 sessionID 过滤；
// 全局流不支持 after 续传，断连窗口的事件会丢失。
// 内部循环：连接 → 解析 → 写 chan → 断线指数退避重连。
// 不可恢复的 HTTP 错误（4xx，除 429）写 errc 后停止。ctx 取消即关闭 chan。
//
// 调用方典型用法：
//
//	events, errc := client.SessionEvents(ctx, id, nil)
//	for ev := range events {
//	    switch ev.Type {
//	    case opencode.EventSessionNextTextDelta: ...
//	    case opencode.EventSessionIdle: return
//	    }
//	}
//	if err := <-errc; err != nil { ... }
func (c *Client) SessionEvents(ctx context.Context, sessionID string, opt *SessionEventsOpt) (<-chan Event, <-chan error) {
	opt = opt.normalize()
	events := make(chan Event, 16)
	errc := make(chan error, 1)
	go c.runSessionEvents(ctx, sessionID, opt, events, errc)
	return events, errc
}

func (c *Client) runSessionEvents(ctx context.Context, sessionID string, opt *SessionEventsOpt, events chan<- Event, errc chan<- error) {
	defer close(events)
	defer close(errc)

	var attempt int
	// retryAfter 累加 attempt、按上限与退避决定是否继续重连；返回 false 表示已耗尽
	// attempts 或 ctx 取消（错误已写入 errc），调用方应 return。消除两处同函数内的重复块。
	retryAfter := func(cause error) bool {
		attempt++
		if opt.MaxAttempts > 0 && attempt > opt.MaxAttempts {
			errc <- fmt.Errorf("opencode: reconnect attempts exhausted: %w", cause)
			return false
		}
		if !sleep(ctx, backoff(opt, attempt)) {
			errc <- ctx.Err()
			return false
		}
		return true
	}
	for {
		if err := ctx.Err(); err != nil {
			errc <- err
			return
		}

		resp, err := c.connectStream(ctx, opt.Location)
		if err != nil {
			// ctx 取消属于正常退出
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				errc <- err
				return
			}
			// 不可恢复 HTTP 错误直接终止
			var ae *APIError
			if errors.As(err, &ae) && isFatalStatus(ae.Status) {
				errc <- err
				return
			}
			// 可恢复：网络层错误或 5xx/429
			if !retryAfter(err) {
				return
			}
			continue
		}

		// 连接成功，重置 attempt
		attempt = 0
		streamErr := c.pumpStream(ctx, resp, sessionID, events)
		_ = resp.Body.Close()

		if streamErr != nil {
			if errors.Is(streamErr, context.Canceled) || errors.Is(streamErr, context.DeadlineExceeded) {
				errc <- streamErr
				return
			}
			// 普通 io 错误：进入重连
		}
		if err := ctx.Err(); err != nil {
			errc <- err
			return
		}
		if !retryAfter(streamErr) {
			return
		}
	}
}

func (c *Client) connectStream(ctx context.Context, loc *LocationRef) (*http.Response, error) {
	req, err := c.newEventRequest(ctx, loc)
	if err != nil {
		return nil, err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	if !statusOK(resp.StatusCode, 0) {
		raw, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		return nil, parseAPIError(resp.StatusCode, raw)
	}
	return resp, nil
}

// pumpStream 阻塞读取 SSE 流直到结束或 ctx 取消，仅透传属于 sessionID 的事件。
func (c *Client) pumpStream(ctx context.Context, resp *http.Response, sessionID string, events chan<- Event) error {
	sc := newSSEScanner(resp.Body)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		id, eventType, data, err := sc.next()
		if err != nil {
			return err
		}
		ev, derr := decodeEvent(id, eventType, data)
		if derr != nil {
			// 跳过无法解析的帧，不中断流
			continue
		}
		if ev.Type == "" {
			continue
		}
		if extractSessionID(ev) != sessionID {
			continue // 全局事件或其他会话的事件
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case events <- ev:
		}
	}
}

func isFatalStatus(code int) bool {
	return code >= 400 && code < 500 && code != http.StatusTooManyRequests
}

func backoff(opt *SessionEventsOpt, attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	// 500ms, 1s, 2s, 4s ... 上限 BackoffMax
	d := opt.BackoffMin << uint(attempt-1)
	if d <= 0 || d > opt.BackoffMax {
		d = opt.BackoffMax
	}
	return d
}

func sleep(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
