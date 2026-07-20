package opencode

import (
	"context"
	"encoding/json"
	"fmt"
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
	// 序列化整个 Event envelope 作为 data
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

// 构造 durable 事件，seq 递增。
func durableEvent(seq int64, evType string, payload any) Event {
	return Event{
		ID:      fmt.Sprintf("evt_%d", seq),
		Type:    evType,
		Durable: &Durable{AggregateID: "ses_1", Seq: seq, Version: 1},
		Data:    jsonRaw(mustJSON(payload)),
	}
}

func TestSessionEvents_dedupesAfterReconnect(t *testing.T) {
	// 服务端按 after 游标回复事件。第一次连接发完 seq=1..3 后断开，
	// 客户端应当从 after=3 继续读到 4..5，且不重复 1..3。
	var reqCount int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/session/ses_1/event" {
			t.Errorf("path = %s", r.URL.Path)
		}
		after := int64(0)
		_, _ = fmt.Sscanf(r.URL.Query().Get("after"), "%d", &after)
		n := atomic.AddInt32(&reqCount, 1)

		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		fl := w.(http.Flusher)

		switch n {
		case 1:
			// 首次连接：发 1..3，然后关闭（模拟中途断线）
			for seq := int64(1); seq <= 3; seq++ {
				if after >= seq {
					continue
				}
				_, _ = io.WriteString(w, sseFrame(durableEvent(seq, EventSessionNextTextDelta, TextDeltaData{Delta: "x"})))
				fl.Flush()
			}
			// 主动中止连接：用 hijack 直接关掉
			hj, ok := w.(http.Hijacker)
			if !ok {
				t.Fatal("server doesn't support hijack")
			}
			conn, _, _ := hj.Hijack()
			_ = conn.Close()
		default:
			// 重连：从 after+1 续传，发剩余事件并保持连接（客户端靠 ctx 取消退出）
			for seq := after + 1; seq <= 5; seq++ {
				_, _ = io.WriteString(w, sseFrame(durableEvent(seq, EventSessionNextTextDelta, TextDeltaData{Delta: "x"})))
				fl.Flush()
			}
			// 保持连接，等客户端 ctx 取消
			<-r.Context().Done()
		}
	}))
	defer srv.Close()

	c, _ := New(srv.URL, WithHTTPClient(&http.Client{Timeout: 0}))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events, errc := c.SessionEvents(ctx, "ses_1", &SessionEventsOpt{
		BackoffMin: 10 * time.Millisecond,
		BackoffMax: 50 * time.Millisecond,
	})

	var seen []int64
	for ev := range events {
		if ev.Durable != nil {
			seen = append(seen, ev.Durable.Seq)
		}
		if len(seen) >= 5 {
			cancel()
		}
	}
	if err := <-errc; err != nil && err != context.Canceled {
		t.Fatalf("errc = %v", err)
	}

	if len(seen) != 5 {
		t.Fatalf("seen = %v", seen)
	}
	for i, want := range []int64{1, 2, 3, 4, 5} {
		if seen[i] != want {
			t.Errorf("seen[%d] = %d, want %d; full = %v", i, seen[i], want, seen)
		}
	}
}

func TestSessionEvents_fatalErrorNoRetry(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"type":"UnauthorizedError","message":"bad token"}`))
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
