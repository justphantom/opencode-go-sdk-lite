// Command integration-check 对运行中的 opencode serve 做端到端 SDK 验证。
// 用法: go run ./cmd/integration-check
// 默认指向 http://127.0.0.1:6096，无口令。
package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	oc "github.com/justphantom/opencode-go-sdk-lite"
)

const baseURL = "http://127.0.0.1:6096"

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	c, err := oc.New(baseURL)
	must("New", err)

	// 用例收集：每个用例输出 ok / 失败 / 关键样本
	type result struct {
		name   string
		status string
		detail string
	}
	var results []result
	report := func(name, status, detail string) {
		results = append(results, result{name, status, detail})
		fmt.Printf("[%s] %s: %s\n", status, name, detail)
	}

	// 1. Health
	if err := c.Health(ctx); err != nil {
		report("Health", "FAIL", err.Error())
	} else {
		report("Health", "PASS", "healthy=true")
	}

	// 2. ListAgents
	agents, err := c.ListAgents(ctx, nil)
	if err != nil {
		report("ListAgents", "FAIL", err.Error())
	} else {
		names := make([]string, len(agents))
		for i, a := range agents {
			names[i] = a.Name + "(" + a.Mode + ")"
		}
		report("ListAgents", "PASS", fmt.Sprintf("%d agents: %s", len(agents), strings.Join(names, ", ")))
	}

	// 3. ListModels
	models, err := c.ListModels(ctx, nil)
	if err != nil {
		report("ListModels", "FAIL", err.Error())
	} else {
		// 选第一个 enabled 且 active 的模型用于后续 prompt
		var first *oc.ModelInfo
		for i := range models {
			if models[i].Enabled && models[i].Status == "active" {
				first = &models[i]
				break
			}
		}
		detail := fmt.Sprintf("%d models", len(models))
		if first != nil {
			detail += "; first enabled=" + first.ID + " (" + first.ProviderID + ")"
		}
		report("ListModels", "PASS", detail)
		// 存到外层供后面用
		if first != nil {
			selectedModel = &oc.ModelRef{ID: first.ID, ProviderID: first.ProviderID}
		}
	}

	// 4. ListProviders
	providers, err := c.ListProviders(ctx, nil)
	if err != nil {
		report("ListProviders", "FAIL", err.Error())
	} else {
		names := make([]string, len(providers))
		for i, p := range providers {
			names[i] = p.ID
		}
		report("ListProviders", "PASS", fmt.Sprintf("%d providers: %s", len(providers), strings.Join(names, ", ")))
	}

	// 5. GenerateMessageID
	mid, err := oc.GenerateMessageID()
	if err != nil {
		report("GenerateMessageID", "FAIL", err.Error())
	} else if !strings.HasPrefix(mid, "msg_") || len(mid) != 30 {
		report("GenerateMessageID", "FAIL", fmt.Sprintf("malformed: %q len=%d", mid, len(mid)))
	} else {
		report("GenerateMessageID", "PASS", mid)
	}

	// 6. CreateSession
	ses, err := c.CreateSession(ctx, &oc.CreateSessionReq{
		Agent: "build",
		Model: selectedModel,
	})
	if err != nil {
		report("CreateSession", "FAIL", err.Error())
		os.Exit(1)
	}
	report("CreateSession", "PASS", fmt.Sprintf("id=%s title=%q", ses.ID, ses.Title))

	// 7. GetSession
	if got, err := c.GetSession(ctx, ses.ID); err != nil {
		report("GetSession", "FAIL", err.Error())
	} else if got.ID != ses.ID {
		report("GetSession", "FAIL", "id mismatch")
	} else {
		report("GetSession", "PASS", "id matches")
	}

	// 8. Prompt（messageID/partID 由 SDK 生成并经 ack 回传）
	ack, err := c.Prompt(ctx, ses.ID, &oc.PromptReq{
		Parts: []oc.PromptPart{{Type: "text", Text: "请只回复两个字：你好"}},
	})
	if err != nil {
		report("Prompt", "FAIL", err.Error())
	} else {
		report("Prompt", "PASS", fmt.Sprintf("msg=%s parts=%v", ack.MessageID, ack.PartIDs))
	}

	// 9. ListMessages
	msgs, err := c.ListMessages(ctx, ses.ID, nil)
	if err != nil {
		report("ListMessages", "FAIL", err.Error())
	} else {
		roles := make([]string, len(msgs))
		for i, m := range msgs {
			roles[i] = m.Info.Role
		}
		report("ListMessages", "PASS", fmt.Sprintf("%d messages: %v", len(msgs), roles))
	}

	// 10. SessionEvents (全局 /event 按 sessionID 过滤)
	testSessionEvents(ctx, c, ses.ID, report)

	// 11. GlobalEventStream + Run
	testRun(ctx, c, ses.ID, report)

	// 12. ListSessions
	all, err := c.ListSessions(ctx, &oc.ListSessionsOpt{Limit: 5})
	if err != nil {
		report("ListSessions", "FAIL", err.Error())
	} else {
		report("ListSessions", "PASS", fmt.Sprintf("%d sessions (limit=5)", len(all)))
	}

	// 13. DeleteSession（最后做，删完前面那些就失效）
	// 新建一个专门的来删
	toDel, _ := c.CreateSession(ctx, &oc.CreateSessionReq{Agent: "build", Model: selectedModel})
	if err := c.DeleteSession(ctx, toDel.ID); err != nil {
		report("DeleteSession", "FAIL", err.Error())
	} else {
		// 二次 GET 验证删了
		if _, err := c.GetSession(ctx, toDel.ID); err != nil {
			report("DeleteSession", "PASS", fmt.Sprintf("deleted %s, follow-up GET errored as expected", toDel.ID))
		} else {
			report("DeleteSession", "FAIL", "session still exists after delete")
		}
	}

	// 清理本次创建的 session
	_ = c.DeleteSession(ctx, ses.ID)

	// 汇总
	fmt.Println("\n=== Summary ===")
	pass, fail := 0, 0
	for _, r := range results {
		if r.status == "PASS" {
			pass++
		} else {
			fail++
		}
	}
	fmt.Printf("PASS=%d FAIL=%d\n", pass, fail)
	if fail > 0 {
		os.Exit(1)
	}
}

var selectedModel *oc.ModelRef

// testSessionEvents 订阅会话事件（内部为全局流过滤）。最多等 5 秒。
func testSessionEvents(ctx context.Context, c *oc.Client, sid string, report func(string, string, string)) {
	subCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	events, errc := c.SessionEvents(subCtx, sid, &oc.SessionEventsOpt{
		BackoffMin: 200 * time.Millisecond,
		BackoffMax: 2 * time.Second,
	})

	var seen []string
	timeout := time.After(5 * time.Second)
loop:
	for {
		select {
		case ev, ok := <-events:
			if !ok {
				break loop
			}
			seen = append(seen, ev.Type)
			if len(seen) >= 8 {
				break loop
			}
		case err := <-errc:
			if err != nil && err != context.DeadlineExceeded && err != context.Canceled {
				report("SessionEvents", "FAIL", fmt.Sprintf("errc: %v; seen=%v", err, seen))
				return
			}
			break loop
		case <-timeout:
			break loop
		}
	}
	cancel()
	if len(seen) == 0 {
		report("SessionEvents", "FAIL", "no events received in 5s")
		return
	}
	report("SessionEvents", "PASS", fmt.Sprintf("first %d types: %v", len(seen), seen))
}

// testRun 用 GlobalEventStream + Run 跑一轮对话，断言首事件是 Prompt 且最后是终止事件。
func testRun(ctx context.Context, c *oc.Client, existingSession string, report func(string, string, string)) {
	runCtx, cancel := context.WithTimeout(ctx, 40*time.Second)
	defer cancel()

	stream, err := c.NewGlobalEventStream(runCtx)
	if err != nil {
		report("GlobalEventStream", "FAIL", err.Error())
		return
	}
	defer func() { _ = stream.Close() }()

	out, err := c.Run(runCtx, stream, oc.RunOptions{
		Prompt:    "请只回复两个字：可以",
		SessionID: existingSession, // 复用，便于观察
	})
	if err != nil {
		report("Run", "FAIL", "Run: "+err.Error())
		return
	}

	var kinds []string
	var text strings.Builder
	var last oc.HighEvent
	timeout := time.After(40 * time.Second)
	for ev := range out {
		kinds = append(kinds, string(ev.Kind()))
		if ev.Kind() == oc.HighEventText {
			text.WriteString(ev.Text())
		}
		last = ev
		if ev.Kind() == oc.HighEventResult || ev.Kind() == oc.HighEventError {
			break
		}
		select {
		case <-timeout:
			report("Run", "FAIL", "timeout after kinds="+strings.Join(kinds, ","))
			return
		default:
		}
	}

	if len(kinds) == 0 {
		report("Run", "FAIL", "no events")
		return
	}
	if kinds[0] != string(oc.HighEventPrompt) {
		report("Run", "FAIL", "first event not prompt: "+strings.Join(kinds, ","))
		return
	}
	if last.Kind() != oc.HighEventResult && last.Kind() != oc.HighEventError {
		report("Run", "FAIL", "no terminal event; kinds="+strings.Join(kinds, ","))
		return
	}
	report("Run", "PASS", fmt.Sprintf("kinds=%v text=%q tokens in/out=%d/%d cost=%.4f",
		kinds, text.String(), last.InputTokens(), last.OutputTokens(), last.Cost()))
}

func must(stage string, err error) {
	if err != nil {
		fmt.Fprintf(os.Stderr, "[%s] fatal: %v\n", stage, err)
		os.Exit(2)
	}
}
