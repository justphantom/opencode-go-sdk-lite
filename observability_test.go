package opencode

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// syncedBuffer 线程安全的 bytes.Buffer：slog handler 后台写、测试 goroutine 读，必须同步。
type syncedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (s *syncedBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.Write(p)
}

func (s *syncedBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.String()
}

// newTestLogger 构造捕获到 w 的 DEBUG 级 slog logger。
func newTestLogger(w io.Writer) *slog.Logger {
	return slog.New(slog.NewTextHandler(w, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

// TestGlobalStream_LoggerEmitsLifecycle: connect 失败 + 重连场景下，
// 断言 logger 捕获到 connect attempt / Do failed / reconnect 生命周期日志。
func TestGlobalStream_LoggerEmitsLifecycle(t *testing.T) {
	var connCount int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&connCount, 1)
		if n == 1 {
			// 首次：直接返回非 200 触发重连
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		<-r.Context().Done()
	}))
	defer srv.Close()

	var buf syncedBuffer
	c, _ := New(srv.URL, WithLogger(newTestLogger(&buf)))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s, _ := c.NewGlobalEventStream(ctx, nil)
	defer func() { _ = s.Close() }()

	// 等第二次连接成功
	deadline := time.After(3 * time.Second)
	for atomic.LoadInt32(&connCount) < 2 {
		select {
		case <-deadline:
			t.Fatalf("未发生重连：connCount=%d, log=\n%s", atomic.LoadInt32(&connCount), buf.String())
		case <-time.After(20 * time.Millisecond):
		}
	}
	// 等一会让 connect 日志刷出
	time.Sleep(50 * time.Millisecond)

	log := buf.String()
	for _, want := range []string{"connect attempt", "connect non-200"} {
		if !strings.Contains(log, want) {
			t.Errorf("日志缺失 %q\n完整日志:\n%s", want, log)
		}
	}
	// connect attempt 至少 2 次（首次 + 重连）
	if c := strings.Count(log, "connect attempt"); c < 2 {
		t.Errorf("connect attempt 应至少 2 次（首次+重连），实际 %d 次\n%s", c, log)
	}
}

// TestGlobalStream_DispatchDropLogsWarn: 满 chan + 非终止事件 → WARN 日志含 chan_full=true。
func TestGlobalStream_DispatchDropLogsWarn(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		fl := w.(http.Flusher)
		// 灌入 subscriberBuf+10 个非终止事件，远超 chan 容量
		for i := 0; i < subscriberBuf+10; i++ {
			_, _ = w.Write([]byte(sseTextDelta("ses_d", "msg_d", "x", int64(i))))
			fl.Flush()
		}
		<-r.Context().Done()
	}))
	defer srv.Close()

	var buf syncedBuffer
	c, _ := New(srv.URL, WithLogger(newTestLogger(&buf)))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s, _ := c.NewGlobalEventStream(ctx, nil)
	defer func() { _ = s.Close() }()

	ch := s.Subscribe("ses_d")
	// 故意不读 ch，让 chan 迅速填满

	deadline := time.After(2 * time.Second)
	for !strings.Contains(buf.String(), "chan_full=true") {
		select {
		case <-deadline:
			t.Fatalf("未捕获到 drop WARN 日志\nlog=\n%s", buf.String())
		case <-time.After(20 * time.Millisecond):
		}
		// 触发 select 让调度器有机会跑
		_ = ch
	}
}

// TestGlobalStream_DecodeErrResetsHeartbeat: 注入损坏帧 → decode err 走 continue，
// 但 watchdog 仍被重置（这是 2.1 现状，需测试锁死避免回归改坏）。
func TestGlobalStream_DecodeErrResetsHeartbeat(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		fl := w.(http.Flusher)
		// 发一帧损坏数据（非 JSON）
		_, _ = w.Write([]byte("data: not-json\n\n"))
		fl.Flush()
		<-r.Context().Done()
	}))
	defer srv.Close()

	var buf syncedBuffer
	c, _ := New(srv.URL, WithLogger(newTestLogger(&buf)))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s, _ := c.NewGlobalEventStream(ctx, nil)
	defer func() { _ = s.Close() }()

	// backdate lastHeartbeat，等损坏帧到来后看是否被刷新
	s.lastHeartbeatMu.Lock()
	s.lastHeartbeat = time.Now().Add(-1 * time.Hour)
	s.lastHeartbeatMu.Unlock()

	deadline := time.After(2 * time.Second)
	for {
		s.lastHeartbeatMu.Lock()
		hb := s.lastHeartbeat
		s.lastHeartbeatMu.Unlock()
		if time.Since(hb) < 5*time.Second {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("decode err 后 lastHeartbeat 未更新（应被重置）。log=\n%s", buf.String())
		case <-time.After(20 * time.Millisecond):
		}
	}
	// 同时验证 WARN 日志被记
	if !strings.Contains(buf.String(), "decode event failed") {
		t.Errorf("缺失 decode WARN 日志\nlog=\n%s", buf.String())
	}
}

// TestGlobalStream_BusinessIdleTriggersCallback: shrink businessIdleTimeout，
// 注入假 SSE 仅发心跳（无业务事件）→ onIdle 触发。
func TestGlobalStream_BusinessIdleTriggersCallback(t *testing.T) {
	prevProbe := businessIdleProbe
	prevTimeout := businessIdleTimeout
	businessIdleProbe = 20 * time.Millisecond
	businessIdleTimeout = 80 * time.Millisecond
	defer func() {
		businessIdleProbe = prevProbe
		businessIdleTimeout = prevTimeout
	}()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		fl := w.(http.Flusher)
		// 只发心跳注释行（不带 data:），不触发 dispatch
		for {
			_, _ = w.Write([]byte(": heartbeat\n\n"))
			fl.Flush()
			select {
			case <-r.Context().Done():
				return
			case <-time.After(10 * time.Millisecond):
			}
		}
	}))
	defer srv.Close()

	c, _ := New(srv.URL)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s, _ := c.NewGlobalEventStream(ctx, nil)
	defer func() { _ = s.Close() }()

	_ = s.Subscribe("ses_idle")

	var triggered int32
	var mu sync.Mutex
	var gotSID string
	s.OnIdle = func(sid string, _ time.Time) {
		mu.Lock()
		gotSID = sid
		mu.Unlock()
		atomic.AddInt32(&triggered, 1)
	}

	deadline := time.After(2 * time.Second)
	for atomic.LoadInt32(&triggered) < 1 {
		select {
		case <-deadline:
			t.Fatalf("onIdle 未触发：triggered=%d", atomic.LoadInt32(&triggered))
		case <-time.After(10 * time.Millisecond):
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if gotSID != "ses_idle" {
		t.Errorf("onIdle sid = %q, want ses_idle", gotSID)
	}
}

// TestGlobalStream_BusinessIdleSkipsAskedPending: 先投 permission.asked 进入 pending，
// 等过 timeout → onIdle 不触发（pendingAsked 状态主动挂起）。
func TestGlobalStream_BusinessIdleSkipsAskedPending(t *testing.T) {
	prevProbe := businessIdleProbe
	prevTimeout := businessIdleTimeout
	businessIdleProbe = 20 * time.Millisecond
	businessIdleTimeout = 80 * time.Millisecond
	defer func() {
		businessIdleProbe = prevProbe
		businessIdleTimeout = prevTimeout
	}()

	var askedSent int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		fl := w.(http.Flusher)
		// 发一帧 permission.asked，然后只发心跳
		_, _ = w.Write([]byte(
			`data: {"id":"evt_p1","type":"permission.asked","properties":{"id":"per_1","sessionID":"ses_p","permission":"bash","patterns":["ls"]}}` + "\n\n"),
		)
		fl.Flush()
		atomic.AddInt32(&askedSent, 1)
		for {
			_, _ = w.Write([]byte(": heartbeat\n\n"))
			fl.Flush()
			select {
			case <-r.Context().Done():
				return
			case <-time.After(10 * time.Millisecond):
			}
		}
	}))
	defer srv.Close()

	var buf syncedBuffer
	c, _ := New(srv.URL, WithLogger(newTestLogger(&buf)))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s, _ := c.NewGlobalEventStream(ctx, nil)
	defer func() { _ = s.Close() }()

	ch := s.Subscribe("ses_p")
	// 消费 asked 事件（不然 chan 满会丢，dispatch 仍 addAsked；但消费更接近真实）
	go func() {
		for range ch {
		}
	}()

	s.OnIdle = func(string, time.Time) {
		t.Errorf("onIdle 在 pendingAsked 状态下不应触发")
	}

	// 等 asked 投出
	deadline := time.After(500 * time.Millisecond)
	for atomic.LoadInt32(&askedSent) < 1 {
		select {
		case <-deadline:
			t.Fatalf("asked 帧未投出")
		case <-time.After(10 * time.Millisecond):
		}
	}
	// 等 5 倍 timeout，确保 businessWatchdog 多个 tick 都跑过
	time.Sleep(5 * businessIdleTimeout)
	if !strings.Contains(buf.String(), "asked pending") {
		t.Errorf("未捕获 asked pending 跳过日志\nlog=\n%s", buf.String())
	}
}

// TestGlobalStream_BusinessIdleNoCancelConn: 业务空闲场景下 connCancel 不被调用（与心跳 watchdog 区分）。
func TestGlobalStream_BusinessIdleNoCancelConn(t *testing.T) {
	prevProbe := businessIdleProbe
	prevTimeout := businessIdleTimeout
	businessIdleProbe = 20 * time.Millisecond
	businessIdleTimeout = 80 * time.Millisecond
	defer func() {
		businessIdleProbe = prevProbe
		businessIdleTimeout = prevTimeout
	}()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		fl := w.(http.Flusher)
		// 持续发心跳注释行让连接 watchdog 不触发（业务 watchdog 应触发但不应 cancel）
		for {
			_, _ = w.Write([]byte(": heartbeat\n\n"))
			fl.Flush()
			select {
			case <-r.Context().Done():
				return
			case <-time.After(10 * time.Millisecond):
			}
		}
	}))
	defer srv.Close()

	c, _ := New(srv.URL)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s, _ := c.NewGlobalEventStream(ctx, nil)
	defer func() { _ = s.Close() }()

	_ = s.Subscribe("ses_n")

	var idle int32
	s.OnIdle = func(string, time.Time) { atomic.AddInt32(&idle, 1) }

	var cancelCount int32
	s.cancelConnHook = func() { atomic.AddInt32(&cancelCount, 1) }

	// 等 onIdle 至少触发一次（验证业务 watchdog 工作）
	deadline := time.After(2 * time.Second)
	for atomic.LoadInt32(&idle) < 1 {
		select {
		case <-deadline:
			t.Fatalf("onIdle 未触发：idle=%d", atomic.LoadInt32(&idle))
		case <-time.After(10 * time.Millisecond):
		}
	}
	// 再多等几个 tick 确认 cancelConn 没被调
	time.Sleep(3 * businessIdleTimeout)
	if atomic.LoadInt32(&cancelCount) != 0 {
		t.Errorf("业务空闲不应触发 cancelConn：cancelCount=%d", atomic.LoadInt32(&cancelCount))
	}
}

// TestGlobalStream_BusinessIdleCallbackNotUnderLock: 注入阻塞 200ms 的 onIdle，
// 并发调 Subscribe → 不被阻塞（验证回调在锁外执行）。
func TestGlobalStream_BusinessIdleCallbackNotUnderLock(t *testing.T) {
	prevProbe := businessIdleProbe
	prevTimeout := businessIdleTimeout
	businessIdleProbe = 20 * time.Millisecond
	businessIdleTimeout = 40 * time.Millisecond
	defer func() {
		businessIdleProbe = prevProbe
		businessIdleTimeout = prevTimeout
	}()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		fl := w.(http.Flusher)
		for {
			_, _ = w.Write([]byte(": heartbeat\n\n"))
			fl.Flush()
			select {
			case <-r.Context().Done():
				return
			case <-time.After(10 * time.Millisecond):
			}
		}
	}))
	defer srv.Close()

	c, _ := New(srv.URL)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s, _ := c.NewGlobalEventStream(ctx, nil)
	defer func() { _ = s.Close() }()

	_ = s.Subscribe("ses_l")

	var onIdleEntered int32
	s.OnIdle = func(string, time.Time) {
		atomic.AddInt32(&onIdleEntered, 1)
		time.Sleep(200 * time.Millisecond) // 模拟慢回调
	}

	// 等 onIdle 真的进入
	deadline := time.After(2 * time.Second)
	for atomic.LoadInt32(&onIdleEntered) < 1 {
		select {
		case <-deadline:
			t.Fatalf("onIdle 未触发：entered=%d", atomic.LoadInt32(&onIdleEntered))
		case <-time.After(10 * time.Millisecond):
		}
	}
	// 此时 onIdle 应在 sleep，并发调 Subscribe 应能立即返回
	done := make(chan struct{})
	go func() {
		_ = s.Subscribe("ses_l_concurrent")
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Subscribe 在 onIdle 阻塞期间被卡住（说明回调持锁）")
	}
}

// TestGlobalStream_HeartbeatStillCancelsOnNoFrame: 完全无帧 15s（这里 shrink）→
// cancelConn 仍触发（业务 watchdog 不破坏心跳 watchdog 契约）。
func TestGlobalStream_HeartbeatStillCancelsOnNoFrame(t *testing.T) {
	prev := heartbeatTimeout
	heartbeatTimeout = 50 * time.Millisecond
	defer func() { heartbeatTimeout = prev }()

	var cancelCount int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		<-r.Context().Done() // 完全不发任何帧
	}))
	defer srv.Close()

	c, _ := New(srv.URL)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s, _ := c.NewGlobalEventStream(ctx, nil)
	defer func() { _ = s.Close() }()

	s.cancelConnHook = func() { atomic.AddInt32(&cancelCount, 1) }
	// backdate heartbeat
	s.lastHeartbeatMu.Lock()
	s.lastHeartbeat = time.Now().Add(-1 * time.Hour)
	s.lastHeartbeatMu.Unlock()

	deadline := time.After(2 * time.Second)
	for atomic.LoadInt32(&cancelCount) < 1 {
		select {
		case <-deadline:
			t.Fatalf("心跳 watchdog 未触发 cancelConn：cancelCount=%d", atomic.LoadInt32(&cancelCount))
		case <-time.After(20 * time.Millisecond):
		}
	}
}
