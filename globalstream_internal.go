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
	if s.cancelConnHook != nil {
		s.cancelConnHook()
	}
}

// recoverPanic 吞掉 panic 防止 goroutine 崩溃传播。
func recoverPanic(where string) {
	if r := recover(); r != nil {
		_ = r
		_ = where
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
