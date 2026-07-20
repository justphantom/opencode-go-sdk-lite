//go:build integration

package opencode

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

// 对运行中的 opencode serve 做端到端验证：
//
//	OPENCODE_TEST_URL=http://127.0.0.1:4096 go test -tags=integration -run TestIntegration -v .
//
// 服务不可达时整体 Skip，不污染普通测试。

func integrationClient(t *testing.T) *Client {
	t.Helper()
	u := os.Getenv("OPENCODE_TEST_URL")
	if u == "" {
		u = "http://127.0.0.1:4096"
	}
	var opts []Option
	if p := os.Getenv("OPENCODE_TEST_PASS"); p != "" {
		opts = append(opts, WithPassword(p))
	}
	c, err := New(u, opts...)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.Health(ctx); err != nil {
		t.Skipf("opencode serve 不可达（%s）: %v", u, err)
	}
	return c
}

func TestIntegration(t *testing.T) {
	c := integrationClient(t)
	t.Run("Health", func(t *testing.T) { testHealth(t, c) })
	t.Run("Metadata", func(t *testing.T) { testMetadata(t, c) })
	t.Run("SessionLifecycle", func(t *testing.T) { testSessionLifecycle(t, c) })
	t.Run("Negative", func(t *testing.T) { testNegative(t, c) })
	t.Run("SessionEvents", func(t *testing.T) { testSessionEventsLive(t, c) })
	t.Run("GoldenCapture", func(t *testing.T) { testGoldenCapture(t, c) })
	t.Run("Interrupt", func(t *testing.T) { testInterrupt(t, c) })
	t.Run("DirectoryRouting", func(t *testing.T) { testDirectoryRouting(t, c) })
	t.Run("GlobalStreamRun", func(t *testing.T) { testGlobalStreamRun(t, c) })
}

func testHealth(t *testing.T, c *Client) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	// Health 内部已校验 healthy=true；这里再取版本号作格式漂移锚点
	if err := c.Health(ctx); err != nil {
		t.Fatalf("Health: %v", err)
	}
	var body struct {
		Healthy bool   `json:"healthy"`
		Version string `json:"version"`
	}
	if err := c.doJSON(ctx, http_GET, "/global/health", nil, nil, &body, 0); err != nil {
		t.Fatalf("health decode: %v", err)
	}
	if body.Version == "" {
		t.Errorf("version 为空，无法锚定服务端版本")
	}
	t.Logf("server version=%s", body.Version)
}

func testMetadata(t *testing.T, c *Client) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	agents, err := c.ListAgents(ctx, nil)
	if err != nil {
		t.Fatalf("ListAgents: %v", err)
	}
	if len(agents) == 0 {
		t.Errorf("ListAgents 返回空")
	}
	models, err := c.ListModels(ctx, nil)
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	t.Logf("agents=%d models=%d", len(agents), len(models))
	if _, err := c.ListProviders(ctx, nil); err != nil {
		t.Fatalf("ListProviders: %v", err)
	}
}

// testSessionLifecycle 覆盖建/查/发/列/删全链路。
func testSessionLifecycle(t *testing.T, c *Client) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	ses, err := c.CreateSession(ctx, &CreateSessionReq{})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	t.Cleanup(func() { _ = c.DeleteSession(context.Background(), ses.ID) })

	got, err := c.GetSession(ctx, ses.ID)
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if got.ID != ses.ID {
		t.Errorf("GetSession id = %s, want %s", got.ID, ses.ID)
	}

	ack, err := c.Prompt(ctx, ses.ID, &PromptReq{
		Parts: []PromptPart{{Type: "text", Text: "请只回复两个字：你好"}},
	})
	if err != nil {
		t.Fatalf("Prompt: %v", err)
	}
	if !strings.HasPrefix(ack.MessageID, msgPrefix) {
		t.Errorf("ack.MessageID = %q, 缺 %q 前缀", ack.MessageID, msgPrefix)
	}
	if len(ack.PartIDs) != 1 || !strings.HasPrefix(ack.PartIDs[0], prtPrefix) {
		t.Errorf("ack.PartIDs = %v", ack.PartIDs)
	}

	if _, err := c.ListSessions(ctx, &ListSessionsOpt{Limit: 5}); err != nil {
		t.Fatalf("ListSessions: %v", err)
	}

	// 等 agent-loop 落定再查历史（消息是异步产生的）
	time.Sleep(2 * time.Second)
	msgs, err := c.ListMessages(ctx, ses.ID, nil)
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	if len(msgs) == 0 {
		t.Errorf("ListMessages 返回空，Prompt 未落库")
	}

	if err := c.DeleteSession(ctx, ses.ID); err != nil {
		t.Fatalf("DeleteSession: %v", err)
	}
	if _, err := c.GetSession(ctx, ses.ID); err == nil {
		t.Errorf("删除后 GetSession 仍成功")
	}
}

// testNegative 覆盖错误分支：404、重复删除、非法 messageID 前缀。
func testNegative(t *testing.T, c *Client) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	_, err := c.GetSession(ctx, "ses_nonexistent0000000000000")
	var ae *APIError
	if !errors.As(err, &ae) {
		t.Fatalf("GetSession 不存在 id：err = %v, 期望 APIError", err)
	}
	if ae.Status != http.StatusNotFound {
		t.Errorf("status = %d, want 404", ae.Status)
	}

	if err := c.DeleteSession(ctx, "ses_nonexistent0000000000000"); err == nil {
		t.Errorf("删除不存在会话未报错")
	}

	ses, err := c.CreateSession(ctx, &CreateSessionReq{})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	t.Cleanup(func() { _ = c.DeleteSession(context.Background(), ses.ID) })
	if _, err := c.Prompt(ctx, ses.ID, &PromptReq{
		MessageID: "bad_prefix_id",
		Parts:     []PromptPart{{Type: "text", Text: "hi"}},
	}); err == nil {
		t.Errorf("非法 messageID 前缀未被本地校验拦截")
	}
}

// testSessionEventsLive 先确认订阅流已建立（收到任意过滤后事件）再断言类型。
// 为避免「Prompt 先于连接到达」的竞态，采用 Prompt 重试而非 sleep。
func testSessionEventsLive(t *testing.T, c *Client) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	ses, err := c.CreateSession(ctx, &CreateSessionReq{})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	t.Cleanup(func() { _ = c.DeleteSession(context.Background(), ses.ID) })

	subCtx, stopSub := context.WithCancel(ctx)
	defer stopSub()
	events, errc := c.SessionEvents(subCtx, ses.ID, &SessionEventsOpt{
		BackoffMin: 200 * time.Millisecond,
		BackoffMax: 2 * time.Second,
	})

	var seen []string
	// 最多 3 轮 Prompt；任一轮收到事件即算流建立
	for attempt := 1; attempt <= 3 && len(seen) == 0; attempt++ {
		if _, err := c.Prompt(ctx, ses.ID, &PromptReq{
			Parts: []PromptPart{{Type: "text", Text: "只回复一个字：好"}},
		}); err != nil {
			t.Fatalf("Prompt: %v", err)
		}
		deadline := time.After(15 * time.Second)
	collect:
		for {
			select {
			case ev, ok := <-events:
				if !ok {
					break collect
				}
				seen = append(seen, ev.Type)
				if len(seen) >= 8 {
					break collect
				}
			case err := <-errc:
				if err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
					t.Fatalf("errc: %v; seen=%v", err, seen)
				}
				break collect
			case <-deadline:
				break collect
			}
		}
	}
	if len(seen) == 0 {
		t.Fatalf("3 轮 Prompt 后仍未收到事件")
	}
	t.Logf("event types: %v", seen)
}

// testGoldenCapture 抓取真实 /event 原始帧写入 testdata/sse_frames.txt，
// 供 sse_golden_test.go（无 build tag）回放，锚定线上帧格式。
func testGoldenCapture(t *testing.T, c *Client) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	ses, err := c.CreateSession(ctx, &CreateSessionReq{})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	t.Cleanup(func() { _ = c.DeleteSession(context.Background(), ses.ID) })

	req, err := c.newRequest(ctx, http_GET, "/event", nil, nil)
	if err != nil {
		t.Fatalf("newRequest: %v", err)
	}
	req.Header.Set("Accept", "text/event-stream")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		t.Fatalf("connect /event: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("/event status = %d", resp.StatusCode)
	}

	go func() {
		time.Sleep(500 * time.Millisecond)
		_, _ = c.Prompt(ctx, ses.ID, &PromptReq{
			Parts: []PromptPart{{Type: "text", Text: "只回复一个字：好"}},
		})
	}()

	sc := newSSEScanner(resp.Body)
	var buf strings.Builder
	frames := 0
	deadline := time.Now().Add(30 * time.Second)
	for frames < 30 && time.Now().Before(deadline) {
		id, evType, data, err := sc.next()
		if err != nil {
			break
		}
		if data == "" {
			continue
		}
		// 抓帧同时验证 decodeEvent 能解析（防格式静默漂移）
		if _, derr := decodeEvent(id, evType, data); derr != nil {
			t.Errorf("帧解析失败（格式漂移？）: %v; data=%s", derr, data)
			continue
		}
		if id != "" {
			fmt.Fprintf(&buf, "id: %s\n", id)
		}
		if evType != "" {
			fmt.Fprintf(&buf, "event: %s\n", evType)
		}
		fmt.Fprintf(&buf, "data: %s\n\n", data)
		frames++
	}
	if frames == 0 {
		t.Fatalf("未抓到任何帧")
	}
	if err := os.MkdirAll("testdata", 0o755); err != nil {
		t.Fatalf("mkdir testdata: %v", err)
	}
	if err := os.WriteFile("testdata/sse_frames.txt", []byte(buf.String()), 0o644); err != nil {
		t.Fatalf("写 golden: %v", err)
	}
	t.Logf("captured %d frames -> testdata/sse_frames.txt", frames)
}

// testInterrupt 发长任务后立即中断，断言收到终止信号。
func testInterrupt(t *testing.T, c *Client) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	ses, err := c.CreateSession(ctx, &CreateSessionReq{})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	t.Cleanup(func() { _ = c.DeleteSession(context.Background(), ses.ID) })

	subCtx, stopSub := context.WithCancel(ctx)
	defer stopSub()
	events, _ := c.SessionEvents(subCtx, ses.ID, nil)

	if _, err := c.Prompt(ctx, ses.ID, &PromptReq{
		Parts: []PromptPart{{Type: "text", Text: "从 1 数到 200，每个数字单独一行，不要省略"}},
	}); err != nil {
		t.Fatalf("Prompt: %v", err)
	}

	// 等 agent-loop 启动（见到首个 part 事件）再中断，避免空窗期 no-op
	started := false
	waitStart := time.After(15 * time.Second)
	for !started {
		select {
		case ev, ok := <-events:
			if !ok {
				t.Skipf("事件流提前关闭（模型无凭证？）")
			}
			if ev.Type == EventMessagePartDelta || ev.Type == EventMessagePartUpdated {
				started = true
			}
			if ev.Type == EventSessionError {
				t.Skipf("session.error（模型无凭证？），跳过中断断言")
			}
		case <-waitStart:
			t.Skipf("15s 内 agent-loop 未启动，跳过")
		}
	}

	if err := c.Interrupt(ctx, ses.ID); err != nil {
		t.Fatalf("Interrupt: %v", err)
	}

	deadline := time.After(15 * time.Second)
	for {
		select {
		case ev, ok := <-events:
			if !ok {
				t.Fatalf("事件流关闭但未收到 session.idle")
			}
			if ev.Type == EventSessionIdle {
				return // 中断生效
			}
		case <-deadline:
			t.Fatalf("Interrupt 后 15s 未收到 session.idle")
		}
	}
}

// testDirectoryRouting 验证事件总线按 directory 隔离：
// 在非默认目录建会话，仅带 LocationRef 的订阅能收到事件。
// 注意：Prompt 不支持 directory 参数，若服务端按目录路由会话，此用例会暴露该缺口。
func testDirectoryRouting(t *testing.T, c *Client) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	dir := t.TempDir()
	loc := &LocationRef{Directory: dir}

	ses, err := c.CreateSession(ctx, &CreateSessionReq{Directory: dir})
	if err != nil {
		t.Fatalf("CreateSession(directory): %v", err)
	}
	t.Cleanup(func() { _ = c.DeleteSession(context.Background(), ses.ID) })

	if _, err := c.Prompt(ctx, ses.ID, &PromptReq{
		Parts: []PromptPart{{Type: "text", Text: "只回复一个字：好"}},
	}); err != nil {
		t.Skipf("Prompt 无法触达 directory 会话（SDK 缺口：Prompt 无 directory 参数）: %v", err)
	}

	subCtx, stopSub := context.WithCancel(ctx)
	defer stopSub()
	events, _ := c.SessionEvents(subCtx, ses.ID, &SessionEventsOpt{Location: loc})

	deadline := time.After(15 * time.Second)
	for {
		select {
		case ev, ok := <-events:
			if !ok {
				t.Fatalf("带 LocationRef 的订阅未收到任何事件")
			}
			t.Logf("directory 隔离下收到事件: %s", ev.Type)
			return
		case <-deadline:
			t.Fatalf("带 LocationRef 的订阅 15s 无事件（directory 路由异常）")
		}
	}
}

// testGlobalStreamRun 跑完整一轮 Run，断言首帧 Prompt、尾帧 Result/Error。
func testGlobalStreamRun(t *testing.T, c *Client) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	stream, err := c.NewGlobalEventStream(ctx, nil)
	if err != nil {
		t.Fatalf("NewGlobalEventStream: %v", err)
	}
	defer func() { _ = stream.Close() }()

	out, err := c.Run(ctx, stream, RunOptions{
		Prompt: "请只回复两个字：可以",
		// 不指定 Model：用服务端默认，避免选到无凭证 provider
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	var kinds []HighEventKind
	var text strings.Builder
	var last HighEvent
	for ev := range out {
		kinds = append(kinds, ev.Kind())
		if ev.Kind() == HighEventText {
			text.WriteString(ev.Text())
		}
		last = ev
	}
	if len(kinds) == 0 {
		t.Fatalf("无事件")
	}
	if kinds[0] != HighEventPrompt {
		t.Errorf("首事件 = %s, want prompt", kinds[0])
	}
	if last.Kind() != HighEventResult && last.Kind() != HighEventError {
		t.Errorf("无终止事件; kinds=%v", kinds)
	}
	t.Logf("kinds=%v text=%q tokens=%d/%d cost=%.4f",
		kinds, text.String(), last.InputTokens(), last.OutputTokens(), last.Cost())
}
