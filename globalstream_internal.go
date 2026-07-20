package opencode

import (
	"context"
	"encoding/json"
	"time"
)

// heartbeatWatchdog 独立 goroutine，超时无帧则强制重连。
func (s *GlobalEventStream) heartbeatWatchdog() {
	defer close(s.heartbeatDone)
	defer recoverPanic("GlobalEventStream.heartbeatWatchdog")
	ticker := time.NewTicker(heartbeatTimeout)
	defer ticker.Stop()
	for {
		select {
		case <-s.stopCh:
			return
		case <-ticker.C:
			last := s.getLastHeartbeat()
			if elapsed := time.Since(last); elapsed > heartbeatTimeout {
				s.cancelConn()
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

func (s *GlobalEventStream) cancelConn() {
	s.connCancelMu.Lock()
	defer s.connCancelMu.Unlock()
	if s.connCancel != nil {
		s.connCancel()
	}
}

// recoverPanic 吞掉 panic 防止 goroutine 崩溃传播。
func recoverPanic(where string) {
	if r := recover(); r != nil {
		_ = r
		_ = where
	}
}

// extractSessionID 从事件 data 中提取 sessionID 用于路由。
func extractSessionID(ev Event) string {
	if len(ev.Data) == 0 {
		return ""
	}
	var probe struct {
		SessionID string `json:"sessionID"`
	}
	if err := json.Unmarshal(ev.Data, &probe); err != nil {
		return ""
	}
	return probe.SessionID
}

// isTerminalEvent 判断是否为终止事件（必送达，不能丢）。
// 注意：实测中真实完成信号是 session.next.step.ended+finish=stop，但 idle/error/deleted
// 也是终止语义，这里都按终止处理。
func isTerminalEvent(t string) bool {
	switch t {
	case EventSessionIdle, EventSessionError, EventSessionDeleted,
		EventSessionNextStepEnded, EventSessionNextStepFailed:
		return true
	}
	return false
}
