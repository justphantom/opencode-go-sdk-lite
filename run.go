package opencode

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// RunOptions 是 Run 的高层参数。SessionID 空则内部 CreateSession。
type RunOptions struct {
	Prompt    string
	SessionID string
	Model     *ModelRef
	Agent     string
	Location  *LocationRef // directory 在此
}

const (
	runAbortTimeout = 5 * time.Second
)

// pollAskedInterval / pollAskedToolDelay 控制 asked 补偿轮询。
// 用 var 便于测试 shrink（对齐 heartbeatTimeout 模式）。
var (
	pollAskedInterval  = 60 * time.Second
	pollAskedToolDelay = 3 * time.Second
)

// pollChildrenInterval / taskEventDiscoverDelay 控制 subagent 子 session 的发现节奏。
// task 工具在父 turn 进行中 spawn 子 session（异步），订阅时机不可同步等待 task 事件，
// 用三触发：首次立即（覆盖订阅前已 spawn 的极端窗口）、ticker 兜底、task 事件后定时
// 缩短延迟（subagent 创建到首次 asked 之间窗口）。var 便于测试 shrink。
var (
	pollChildrenInterval   = 5 * time.Second
	taskEventDiscoverDelay = 1 * time.Second
)

// Run 执行一轮对话：建/复用 session → 订阅全局流 → 发 prompt_async →
// 按 assistantMessageID 过滤 → 合成终止事件 → close chan。
//
// 首事件必为 HighEventPrompt（携带 sessionID + user messageID）。
// channel close 前必有 HighEventResult 或 HighEventError（除非 ctx 取消）。
//
// stream 必须是已启动的 GlobalEventStream；Run 会 Subscribe(sessionID) 后 Unsubscribe。
// Agent/Model 随本条消息生效（V1 无 Switch 接口）。
func (c *Client) Run(ctx context.Context, stream *GlobalEventStream, opts RunOptions) (<-chan HighEvent, error) {
	if stream == nil {
		return nil, fmt.Errorf("opencode: stream is nil")
	}
	if opts.Prompt == "" {
		return nil, fmt.Errorf("opencode: prompt is required")
	}

	sessionID := opts.SessionID
	resumed := sessionID != "" // 复用 session：pump 首轮 todo 轮询需吞掉上轮残留基线
	if sessionID == "" {
		req := &CreateSessionReq{Agent: opts.Agent, Model: opts.Model}
		if opts.Location != nil {
			req.Directory = opts.Location.Directory
			req.WorkspaceID = opts.Location.WorkspaceID
		}
		ses, err := c.CreateSession(ctx, req)
		if err != nil {
			return nil, err
		}
		sessionID = ses.ID
	}

	// prompt_async 返 204 无 body，messageID 由 SDK 生成并经 ack 回传
	req := &PromptReq{
		Agent: opts.Agent,
		Model: opts.Model,
		Parts: []PromptPart{{Type: PartTypeText, Text: opts.Prompt}},
	}

	// 先订阅再发 prompt，避免漏掉 turn 的首帧事件
	ch := stream.Subscribe(sessionID)

	ack, err := c.Prompt(ctx, sessionID, req)
	if err != nil {
		stream.Unsubscribe(sessionID)
		return nil, err
	}

	out := make(chan HighEvent, 16)
	go c.pump(ctx, stream, sessionID, ack.MessageID, ch, out, resumed)
	return out, nil
}

// pump 把原始 Event 流转换为 HighEvent 流。
// userMessageID 用于首事件 HighEventPrompt；assistantID 跟随最新 step 的
// assistantMessageID（多轮 agent-loop 每轮换 messageID，见 followAssistantID）。
// resumed 标识是否复用既有 session（true 时首轮 todo 轮询吞掉上轮残留基线）。
func (c *Client) pump(ctx context.Context, stream *GlobalEventStream, sessionID, userMessageID string, src <-chan Event, out chan<- HighEvent, resumed bool) {
	defer close(out)
	defer stream.Unsubscribe(sessionID)
	defer recoverPanic("Client.pump")

	// 首事件：HighEventPrompt，携带 user messageID
	select {
	case <-ctx.Done():
		c.fireAndForgetAbort(sessionID)
		out <- HighEvent{kind: HighEventError, sessionID: sessionID, messageID: userMessageID, isError: true}
		return
	case out <- HighEvent{kind: HighEventPrompt, sessionID: sessionID, messageID: userMessageID}:
	}

	var assistantID string
	var accText strings.Builder
	parts := partTracker{}

	// askedTracker 跨主 src 与子 session 转发去重 asked（按 requestID 全局唯一）。
	// 替代旧 seenAsked 局部 map：子 forward goroutine 并发写入，需要锁保护。
	asked := &askedTracker{seen: make(map[string]bool)}

	// childTracker 发现并订阅 subagent 子 session，把子的 asked 转发到 out。
	// directory 从 stream.loc 读：GlobalEventStream 按 directory 隔离事件总线，
	// 子 session 与父同 directory（task.ts 不改 directory），故子的 asked 一定走
	// 本 stream，订阅子 sid 即可收到。directory 同时是 ListChildren 的 query。
	directory := ""
	if stream.loc != nil {
		directory = stream.loc.Directory
	}
	child := newChildTracker(stream, out, asked, directory, ctx)
	// defer 顺序（LIFO）：cancel forward → Wait 全部退出 → Unsubscribe 子 → close(out)。
	// 必须保证 forward 全部停止后再 close(out)，否则写已关 chan 触发 panic。
	defer child.close()

	var lastTodo string
	todoFirstPoll := true // 本 turn 首次 todo 轮询：复用 session 时吞掉上轮残留基线
	poll := func() {
		// asked 轮询覆盖父+全部已发现子 sid：subagent 的 asked 挂在子 sid 上，
		// 只查父 sid 会漏。pollAsked 一次拉全局 pending 后按集合过滤。
		sids := append([]string{sessionID}, child.sids()...)
		c.pollAsked(ctx, sids, asked, out)
		c.pollTodo(ctx, sessionID, &lastTodo, out, resumed && todoFirstPoll)
		todoFirstPoll = false
	}
	pollTicker := time.NewTicker(pollAskedInterval)
	defer pollTicker.Stop()
	compensateTimer := time.NewTimer(pollAskedToolDelay)
	compensateTimer.Stop() // 默认 disarm，由 tool 事件 armTimer 触发
	defer compensateTimer.Stop()

	// 子 session 发现三触发：首次立即（覆盖订阅前已 spawn 的极端窗口）、
	// ticker 兜底（subagent 创建到首次 asked 之间的窗口）、task 工具事件后定时
	// （task 是 subagent 委派信号，主动缩短发现延迟）。
	child.discover(sessionID)
	childTicker := time.NewTicker(pollChildrenInterval)
	defer childTicker.Stop()
	taskDiscover := time.NewTimer(taskEventDiscoverDelay)
	taskDiscover.Stop() // 默认 disarm，由 task tool_use 事件 armTimer 触发
	defer taskDiscover.Stop()

	poll() // 首次立即（asked + todo）

	for {
		select {
		case <-ctx.Done():
			c.fireAndForgetAbort(sessionID)
			out <- HighEvent{kind: HighEventError, sessionID: sessionID, messageID: assistantID, isError: true}
			return
		case <-pollTicker.C:
			poll()
		case <-compensateTimer.C:
			poll()
		case <-childTicker.C:
			child.discover(sessionID)
		case <-taskDiscover.C:
			child.discover(sessionID)
		case ev, ok := <-src:
			if !ok {
				// 流被关闭（stream.Close 或 Unsubscribe 由别处触发）；兜底终止。
				// 文本走 text 字段，与 Error 事件约定一致。
				out <- HighEvent{kind: HighEventError, sessionID: sessionID, messageID: assistantID, isError: true, text: "stream closed"}
				return
			}
			he, emit, terminal := mapToHighEvent(ev, &assistantID, parts)
			if !emit {
				continue
			}
			// messageID 过滤：丢弃不属于本 turn 的 part 事件
			if he.MessageID() != "" && assistantID != "" && he.MessageID() != assistantID {
				continue
			}
			// asked 去重：SSE/轮询/子转发任一先到即登记，后到丢弃，避免重复投递
			if !asked.register(&he) {
				continue
			}
			// todo 去重：按全量快照 json 签名，SSE 与轮询任一先到即登记，后到丢弃
			if !registerTodo(&he, &lastTodo) {
				continue
			}
			// 工具相关事件后触发补偿轮询（asked 紧随工具调用，主动缩短补偿延迟）
			if he.Kind() == HighEventToolUse ||
				(he.Kind() == HighEventStepFinish && he.Result() == "tool-calls") {
				armTimer(compensateTimer, pollAskedToolDelay)
			}
			// task 工具即 subagent 委派：其调用事件后加速发现新 spawn 的子 session。
			// 仅在 ToolUse（spawn 时）触发；ToolResult 时子 session 已 idle，无需再发现。
			if he.Kind() == HighEventToolUse && he.ToolKind() == ToolKindSubagent {
				armTimer(taskDiscover, taskEventDiscoverDelay)
			}

			// 累积 assistant 输出文本，供终止事件回填 result。
			if he.Kind() == HighEventText {
				accText.WriteString(he.Text())
			}
			// HighEventResult 的 result 字段由 mapToHighEvent 留空（finish 不是
			// 输出文本）。优先取服务端落库文本（GET message 的 FinalText，
			// 免疫 SSE 丢帧）；取不到/为空则回退累积的 text delta。
			if he.Kind() == HighEventResult && he.result == "" {
				he.result = c.finalText(ctx, sessionID, assistantID, accText.String())
			}

			select {
			case <-ctx.Done():
				c.fireAndForgetAbort(sessionID)
				out <- HighEvent{kind: HighEventError, sessionID: sessionID, messageID: assistantID, isError: true}
				return
			case out <- he:
			}
			if terminal {
				return
			}
		}
	}
}

// finalText 返回 turn 的最终回复：优先服务端落库文本，失败回退 accumulated。
func (c *Client) finalText(ctx context.Context, sessionID, assistantID, accumulated string) string {
	if assistantID != "" {
		if m, err := c.GetMessage(ctx, sessionID, assistantID); err == nil {
			if t := m.FinalText(); t != "" {
				return t
			}
		}
	}
	return accumulated
}

// fireAndForgetAbort ctx 取消时尽力通知服务端中断，超时即放弃。
// 用 context.Background() 派生新 ctx 而非传入父 ctx：调用方此时 ctx 已取消，
// 传进来会让 Interrupt 立即失败，违背 fire-and-forget 语义。
//
//nolint:contextcheck // 有意使用 Background 派生，确保 Interrupt 不被已取消的父 ctx 拖死
func (c *Client) fireAndForgetAbort(sessionID string) {
	abCtx, cancel := context.WithTimeout(context.Background(), runAbortTimeout)
	defer cancel()
	_ = c.Interrupt(abCtx, sessionID)
}

// pollAsked 补偿轮询服务端 pending 列表，捞回 SSE 丢失的 asked 事件。
// 按 requestID 去重（askedTracker）；轮询失败（网络/4xx）吞掉，不终止 pump——
// 补偿路径自身不可成为 turn 失败的原因。
//
// sids 为父 sid 与全部已发现子 sid 的并集：subagent 的 asked 挂在子 sid 上，
// 只查父 sid 会漏。一次 listAll* 拉全局 pending 后按集合过滤，避免 N 次 RPC。
func (c *Client) pollAsked(ctx context.Context, sids []string, asked *askedTracker, out chan<- HighEvent) {
	want := make(map[string]struct{}, len(sids))
	for _, s := range sids {
		want[s] = struct{}{}
	}
	emit := func(id string, he HighEvent) {
		if id == "" || !asked.register(&he) {
			return
		}
		select {
		case <-ctx.Done():
		case out <- he:
		}
	}
	if perms, err := c.listAllPermissions(ctx); err == nil {
		for _, p := range perms {
			if _, ok := want[p.SessionID]; !ok {
				continue
			}
			d := PermissionAskedData{
				ID: p.ID, SessionID: p.SessionID, Permission: p.Permission,
				Patterns: p.Patterns, Metadata: p.Metadata, Always: p.Always, Tool: p.Tool,
			}
			emit(p.ID, HighEvent{kind: HighEventPermissionAsked, sessionID: p.SessionID, permission: &d})
		}
	}
	if qs, err := c.listAllQuestions(ctx); err == nil {
		for _, q := range qs {
			if _, ok := want[q.SessionID]; !ok {
				continue
			}
			d := QuestionAskedData{
				ID: q.ID, SessionID: q.SessionID, Questions: q.Questions, Tool: q.Tool,
			}
			emit(q.ID, HighEvent{kind: HighEventQuestionAsked, sessionID: q.SessionID, question: &d})
		}
	}
}

// registerAsked 登记并去重 SSE 来源的 asked 事件。返回 false 表示已见过应丢弃。
func registerAsked(he *HighEvent, seen map[string]bool) bool {
	switch he.kind {
	case HighEventPermissionAsked:
		if he.permission != nil {
			if seen[he.permission.ID] {
				return false
			}
			seen[he.permission.ID] = true
		}
	case HighEventQuestionAsked:
		if he.question != nil {
			if seen[he.question.ID] {
				return false
			}
			seen[he.question.ID] = true
		}
	}
	return true
}

// armTimer 安全重置 timer：先 Stop 并排空已触发的值，再 Reset，规避 time.Reset 的重入风险。
func armTimer(t *time.Timer, d time.Duration) {
	if !t.Stop() {
		select {
		case <-t.C:
		default:
		}
	}
	t.Reset(d)
}
