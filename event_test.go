package opencode

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func jsonMarshal(v any) ([]byte, error) { return json.Marshal(v) }
func jsonRaw(s string) json.RawMessage  { return json.RawMessage(s) }

// sseFrame 构造一帧 SSE 数据。
func sseFrame(ev Event) string {
	data := mustJSON(ev)
	return "data: " + data + "\n\n"
}

func mustJSON(v any) string {
	b, err := jsonMarshal(v)
	if err != nil {
		panic(err)
	}
	return string(b)
}

func deltaEvent(sessionID, delta string) Event {
	return Event{
		Type:       EventMessagePartDelta,
		Properties: jsonRaw(mustJSON(PartDeltaData{SessionID: sessionID, Delta: delta})),
	}
}

// V1 的 SessionEvents 连接全局 /event 并按 sessionID 过滤：
// 其他会话与全局事件（无 sessionID）不应透传。
func TestSessionEvents_filtersBySessionID(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/event" {
			t.Errorf("path = %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		fl := w.(http.Flusher)
		_, _ = io.WriteString(w, sseFrame(deltaEvent("ses_2", "other")))
		_, _ = io.WriteString(w, sseFrame(Event{Type: EventServerConnected}))
		_, _ = io.WriteString(w, sseFrame(deltaEvent("ses_1", "mine")))
		fl.Flush()
		<-r.Context().Done()
	}))
	defer srv.Close()

	c, _ := New(srv.URL, WithHTTPClient(&http.Client{Timeout: 0}))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events, errc := c.SessionEvents(ctx, "ses_1", nil)

	ev, ok := <-events
	if !ok {
		t.Fatal("events closed before first event")
	}
	var d PartDeltaData
	_ = json.Unmarshal(ev.Properties, &d)
	if d.SessionID != "ses_1" || d.Delta != "mine" {
		t.Errorf("ev = %+v", d)
	}
	cancel()
	for range events {
	}
	<-errc
}

// 首次连接中断后应按退避重连并继续收到事件。
func TestSessionEvents_reconnects(t *testing.T) {
	var reqCount int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&reqCount, 1)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		if n == 1 {
			// 主动断线模拟中途断开
			hj, ok := w.(http.Hijacker)
			if !ok {
				t.Fatal("server doesn't support hijack")
			}
			conn, _, _ := hj.Hijack()
			_ = conn.Close()
			return
		}
		_, _ = io.WriteString(w, sseFrame(deltaEvent("ses_1", "after-reconnect")))
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer srv.Close()

	c, _ := New(srv.URL, WithHTTPClient(&http.Client{Timeout: 0}))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events, errc := c.SessionEvents(ctx, "ses_1", &SessionEventsOpt{
		BackoffMin: 10 * time.Millisecond,
		BackoffMax: 50 * time.Millisecond,
	})

	select {
	case ev, ok := <-events:
		if !ok {
			t.Fatal("events closed")
		}
		var d PartDeltaData
		_ = json.Unmarshal(ev.Properties, &d)
		if d.Delta != "after-reconnect" {
			t.Errorf("delta = %q", d.Delta)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no event after reconnect")
	}
	cancel()
	for range events {
	}
	<-errc
}

func TestSessionEvents_fatalErrorNoRetry(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"name":"UnauthorizedError","data":{"message":"bad token"}}`))
	}))
	defer srv.Close()

	c, _ := New(srv.URL)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	events, errc := c.SessionEvents(ctx, "ses_1", nil)

	// events chan 应关闭，errc 返回 *APIError
	for range events {
	}
	err := <-errc
	if err == nil {
		t.Fatal("expected error")
	}
	ae, ok := err.(*APIError)
	if !ok || ae.Status != 401 {
		t.Fatalf("err = %+v (%T)", err, err)
	}
	if ae.Type != "UnauthorizedError" || ae.Message != "bad token" {
		t.Errorf("ae = %+v", ae)
	}
}

func TestSessionEvents_ctxCancelCloses(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		<-r.Context().Done()
	}))
	defer srv.Close()

	c, _ := New(srv.URL)
	ctx, cancel := context.WithCancel(context.Background())
	events, errc := c.SessionEvents(ctx, "ses_1", nil)

	cancel()
	// events 应被关闭
	for range events {
	}
	<-errc
}

func TestBackoff_clamped(t *testing.T) {
	opt := &SessionEventsOpt{BackoffMin: 100 * time.Millisecond, BackoffMax: time.Second}
	cases := []struct {
		attempt int
		want    time.Duration
	}{
		{1, 100 * time.Millisecond},
		{2, 200 * time.Millisecond},
		{3, 400 * time.Millisecond},
		{4, 800 * time.Millisecond},
		{5, time.Second}, // clamp
		{99, time.Second},
	}
	for _, tc := range cases {
		if got := backoff(opt, tc.attempt); got != tc.want {
			t.Errorf("backoff(%d) = %v, want %v", tc.attempt, got, tc.want)
		}
	}
}

func TestIsFatalStatus(t *testing.T) {
	cases := map[int]bool{
		400: true, 401: true, 403: true, 404: true,
		429: false, 500: false, 502: false, 200: false,
	}
	for code, want := range cases {
		if got := isFatalStatus(code); got != want {
			t.Errorf("isFatalStatus(%d) = %v, want %v", code, got, want)
		}
	}
}
