package opencode

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"time"
)

// GlobalEventStream 维护一条到 /event 的全局长连，按 sessionID 路由事件给订阅者。
// 事件总线按 directory 隔离（实测）：loc 必须与目标会话的 directory 一致，
// 否则收不到这些会话的事件。
// 设计要点（移植自 lark-bridge/internal/opencodeserve/stream.go，已验证）：
//   - 指数退避 100ms→5s，连接存活 <2s 视为 flapping 不重置退避
//   - 心跳 watchdog 15s 无帧则强制重连破半开 TCP
//   - panic recover 不让 goroutine 崩溃传播
//   - 终止事件（session.idle/session.error/session.deleted）必送达，非终止满则丢
//
// 注意：全局流不支持续传，断连窗口的 delta 事件会丢失。
type GlobalEventStream struct {
	c    *Client
	http *http.Client
	loc  *LocationRef

	mu   sync.Mutex
	subs map[string]chan Event

	stopCh chan struct{}
	done   chan struct{}

	lastHeartbeat   time.Time
	lastHeartbeatMu sync.Mutex

	connCancelMu sync.Mutex
	connCancel   context.CancelFunc

	heartbeatDone chan struct{}
}

// heartbeatTimeout 用 var 而非 const，便于测试 shrink（gosec/revive 不要建议改 const）。
var heartbeatTimeout = 15 * time.Second

const (
	reconnectBackoffMin    = 100 * time.Millisecond
	reconnectBackoffMax    = 5 * time.Second
	healthyConnMinDuration = 2 * time.Second
	subscriberBuf          = 256
)

// NewGlobalEventStream 构造并启动后台 goroutine（reader + heartbeat watchdog）。
// loc 定位事件总线（按 directory 隔离）；nil 表示服务端默认目录。
// 调用方应在第一次 Prompt 前调用，避免丢首帧。Close 即停止后台。
func (c *Client) NewGlobalEventStream(ctx context.Context, loc *LocationRef) (*GlobalEventStream, error) {
	s := &GlobalEventStream{
		c:             c,
		http:          c.httpClient,
		loc:           loc,
		subs:          make(map[string]chan Event),
		stopCh:        make(chan struct{}),
		done:          make(chan struct{}),
		heartbeatDone: make(chan struct{}),
		lastHeartbeat: time.Now(),
	}
	go s.run(ctx)
	go s.heartbeatWatchdog()
	return s, nil
}

// Subscribe 注册 sessionID 订阅，返回事件 chan。
// 同一 sessionID 重复 Subscribe：关闭旧 chan 再建新的（订阅语义对齐 lark-bridge）。
// chan 在 Unsubscribe 或 Close 时关闭。
func (s *GlobalEventStream) Subscribe(sessionID string) <-chan Event {
	ch := make(chan Event, subscriberBuf)
	s.mu.Lock()
	if old, ok := s.subs[sessionID]; ok {
		// 旧订阅者的 goroutine 会因 chan close 返回
		close(old)
		delete(s.subs, sessionID)
	}
	s.subs[sessionID] = ch
	s.mu.Unlock()
	return ch
}

// Unsubscribe 取消订阅并关闭 chan。幂等。
func (s *GlobalEventStream) Unsubscribe(sessionID string) {
	s.mu.Lock()
	ch, ok := s.subs[sessionID]
	if ok {
		delete(s.subs, sessionID)
	}
	s.mu.Unlock()
	if ok {
		close(ch)
	}
}

// Close 停止后台 goroutine，关闭所有订阅 chan。幂等。
func (s *GlobalEventStream) Close() error {
	select {
	case <-s.stopCh:
		return nil // 已关闭
	default:
		close(s.stopCh)
	}
	// 主动中断当前连接，让 connect 返回，run 才能感知 stopCh
	s.cancelConn()
	<-s.done
	<-s.heartbeatDone

	s.mu.Lock()
	for sid, ch := range s.subs {
		close(ch)
		delete(s.subs, sid)
	}
	s.mu.Unlock()
	return nil
}

// run 是后台主循环：连接 / 读流 / 断线重连。
func (s *GlobalEventStream) run(ctx context.Context) {
	defer close(s.done)
	defer recoverPanic("GlobalEventStream.run")

	backoff := reconnectBackoffMin
	for {
		select {
		case <-s.stopCh:
			return
		case <-ctx.Done():
			return
		default:
		}

		failed, shortLived := s.connect(ctx)
		if !failed {
			return // 正常停止
		}
		if !shortLived {
			backoff = reconnectBackoffMin // 健康连接曾存活，重置退避
		}

		timer := time.NewTimer(backoff)
		select {
		case <-s.stopCh:
			timer.Stop()
			return
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		backoff *= 2
		if backoff > reconnectBackoffMax {
			backoff = reconnectBackoffMax
		}
	}
}

// connect 发起一次 /event 连接并阻塞读流。
// 返回 (failed=true 表示异常退出需重连, shortLived 表示连接存活 <2s 视为 flapping)。
func (s *GlobalEventStream) connect(ctx context.Context) (failed, shortLived bool) {
	connCtx, cancel := context.WithCancel(ctx)
	s.setConnCancel(cancel)
	defer s.clearConnCancel()

	req, err := s.c.newRequest(connCtx, http_GET, "/event", locationQuery(s.loc), nil)
	if err != nil {
		return true, true
	}
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Cache-Control", "no-cache")

	start := time.Now()
	//nolint:bodyclose // Do 返 error 时 resp==nil 无需关；成功路径的 Body 在下方各错误分支统一 drainAndClose
	resp, err := s.http.Do(req)
	if err != nil {
		// ctx 取消属正常退出，不重连
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return false, false
		}
		return true, true
	}

	if resp.StatusCode != http.StatusOK {
		drainAndClose(resp.Body)
		// 4xx（除 429）属于不可恢复错误，但本层不区分——交给上层处理，这里仍重连
		return true, true
	}

	s.updateHeartbeat()
	sc := newSSEScanner(resp.Body)
	for {
		if err := connCtx.Err(); err != nil {
			drainAndClose(resp.Body)
			return false, false
		}
		_, _, data, err := sc.next()
		if err != nil {
			drainAndClose(resp.Body)
			lived := time.Since(start)
			return true, lived < healthyConnMinDuration
		}
		if data == "" {
			continue // 注释行/心跳
		}
		ev, derr := decodeEvent("", "", data)
		if derr != nil {
			s.updateHeartbeat()
			continue
		}
		s.updateHeartbeat()
		s.dispatch(ev)
	}
}

// dispatch 把事件路由到对应 sessionID 的订阅 chan。
func (s *GlobalEventStream) dispatch(ev Event) {
	sid := extractSessionID(ev)
	if sid == "" {
		return // 全局事件（如 server.connected）不路由
	}
	s.mu.Lock()
	ch, ok := s.subs[sid]
	s.mu.Unlock()
	if !ok {
		return
	}
	// 非阻塞投递；终止事件必送达
	if isTerminalEvent(ev.Type) {
		select {
		case <-s.stopCh:
		case ch <- ev:
		}
		return
	}
	select {
	case ch <- ev:
	default:
		// 满则丢非终止事件
	}
}
