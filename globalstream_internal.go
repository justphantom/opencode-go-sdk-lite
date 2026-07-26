package opencode

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"runtime/debug"
	"time"
)

// newDefaultLogger 返回丢弃所有日志的 logger（WithLogger 未注入时使用）。
// 用 slog.NewTextHandler(io.Discard, nil) 而非自实现 discardHandler——标准库
// slog.Handler 接口 4 方法可省，调用频率低时 TextHandler 的格式化开销可忽略。
func newDefaultLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// heartbeatWatchdog 独立 goroutine，超时无帧则强制重连。
func (s *GlobalEventStream) heartbeatWatchdog() {
	defer close(s.heartbeatDone)
	defer recoverPanic(s.logger, "GlobalEventStream.heartbeatWatchdog")
	ticker := time.NewTicker(heartbeatTimeout)
	defer ticker.Stop()
	for {
		select {
		case <-s.stopCh:
			return
		case <-ticker.C:
			last := s.getLastHeartbeat()
			if elapsed := time.Since(last); elapsed > heartbeatTimeout {
				s.cancelConn("heartbeat")
			}
		}
	}
}

func (s *GlobalEventStream) updateHeartbeat() {
	s.lastHeartbeatMu.Lock()
	s.lastHeartbeat = time.Now()
	s.lastHeartbeatMu.Unlock()
}

func (s *GlobalEventStream) getLastHeartbeat() time.Time {
	s.lastHeartbeatMu.Lock()
	defer s.lastHeartbeatMu.Unlock()
	return s.lastHeartbeat
}

// businessWatchdog 监控业务事件流空闲。不重连——触发 OnIdle 回调，由订阅者决策。
// 等用户应答 permission/question 是"主动挂起"而非"静默故障"，pendingAsked 状态跳过触发。
func (s *GlobalEventStream) businessWatchdog() {
	defer close(s.businessDone)
	defer recoverPanic(s.logger, "GlobalEventStream.businessWatchdog")
	timeout := s.idleTimeout
	if timeout == 0 {
		timeout = businessIdleTimeout
	}
	ticker := time.NewTicker(businessIdleProbe)
	defer ticker.Stop()
	for {
		select {
		case <-s.stopCh:
			return
		case <-ticker.C:
			last := s.getLastBusinessEvent()
			elapsed := time.Since(last)
			if elapsed <= timeout {
				continue
			}
			// 快照订阅者列表后释放锁，回调在锁外执行（避免阻塞 dispatch/Subscribe）
			s.mu.Lock()
			sids := make([]string, 0, len(s.subs))
			for sid := range s.subs {
				sids = append(sids, sid)
			}
			s.mu.Unlock()
			for _, sid := range sids {
				if s.isAskedPending(sid) {
					s.logger.Debug("business idle skipped, asked pending", "sid", sid, "idle_for", elapsed)
					continue
				}
				if s.OnIdle != nil {
					s.callOnIdle(sid, last)
				}
			}
			s.updateBusinessEvent() // 重置，避免下次 tick 重复触发
		}
	}
}

// callOnIdle 在独立 recover 里执行 OnIdle，回调 panic 不影响下次 tick。
func (s *GlobalEventStream) callOnIdle(sid string, last time.Time) {
	defer recoverPanic(s.logger, "OnIdle callback")
	s.OnIdle(sid, last)
}

func (s *GlobalEventStream) updateBusinessEvent() {
	s.lastBusinessEventMu.Lock()
	s.lastBusinessEvent = time.Now()
	s.lastBusinessEventMu.Unlock()
}

func (s *GlobalEventStream) getLastBusinessEvent() time.Time {
	s.lastBusinessEventMu.Lock()
	defer s.lastBusinessEventMu.Unlock()
	return s.lastBusinessEvent
}

func (s *GlobalEventStream) addAsked(sid string) {
	s.pendingAskedMu.Lock()
	s.pendingAsked[sid] = struct{}{}
	s.pendingAskedMu.Unlock()
}

func (s *GlobalEventStream) clearAsked(sid string) {
	s.pendingAskedMu.Lock()
	delete(s.pendingAsked, sid)
	s.pendingAskedMu.Unlock()
}

func (s *GlobalEventStream) isAskedPending(sid string) bool {
	s.pendingAskedMu.Lock()
	defer s.pendingAskedMu.Unlock()
	_, ok := s.pendingAsked[sid]
	return ok
}

func (s *GlobalEventStream) setConnCancel(cf context.CancelFunc) {
	s.connCancelMu.Lock()
	s.connCancel = cf
	s.connCancelMu.Unlock()
}

func (s *GlobalEventStream) clearConnCancel() {
	s.connCancelMu.Lock()
	s.connCancel = nil
	s.connCancelMu.Unlock()
}

// cancelConn 取消当前连接。reason 标识来源（heartbeat/close/user），用于日志区分。
func (s *GlobalEventStream) cancelConn(reason string) {
	s.connCancelMu.Lock()
	defer s.connCancelMu.Unlock()
	if s.connCancel != nil {
		s.connCancel()
	}
	s.logger.Info("cancelConn", "reason", reason)
	if s.cancelConnHook != nil {
		s.cancelConnHook()
	}
}

// recoverPanic 吞掉 panic 防止 goroutine 崩溃传播。logger 必须非 nil。
func recoverPanic(logger *slog.Logger, where string) {
	if r := recover(); r != nil {
		logger.Error("panic recovered", "where", where, "r", r, "stack", string(debug.Stack()))
	}
}

// extractSessionID 从事件 properties 中提取 sessionID 用于路由。
func extractSessionID(ev Event) string {
	if len(ev.Properties) == 0 {
		return ""
	}
	var probe struct {
		SessionID string `json:"sessionID"`
	}
	if err := json.Unmarshal(ev.Properties, &probe); err != nil {
		return ""
	}
	return probe.SessionID
}

// isTerminalEvent 判断是否为终止事件（必送达，不能丢）。
// 实测 turn 的结束信号：step-finish(reason=stop) → message.updated →
// session.status idle → session.idle；idle/error/deleted 均按终止处理。
// EDGE-1：step-finish(reason=stop) 是 turn 真实结束信号，满 chan 时若走"满则丢"
// 分支会让消费方拿不到 HighEventResult（token 统计丢失），故也按终止处理。
func isTerminalEvent(ev Event) bool {
	switch ev.Type {
	case EventSessionIdle, EventSessionError, EventSessionDeleted:
		return true
	case EventMessagePartUpdated:
		// 内层探测 part.type=step-finish + reason=stop/空。该事件频率低（每 step 一次），
		// 多一次 JSON probe 无性能影响。
		if len(ev.Properties) == 0 {
			return false
		}
		var probe struct {
			Part struct {
				Type   string `json:"type"`
				Reason string `json:"reason"`
			} `json:"part"`
		}
		if err := json.Unmarshal(ev.Properties, &probe); err != nil {
			return false
		}
		if probe.Part.Type == PartTypeStepFinish &&
			(probe.Part.Reason == "stop" || probe.Part.Reason == "") {
			return true
		}
	}
	return false
}
