# opencode-go-sdk-lite

opencode v1 HTTP API 的轻量 Go SDK，纯标准库实现。

覆盖范围：
- 会话管理（创建 / 查询 / 更新 / 删除）与对话发起（prompt / 中断 / 历史；agent/model 随 prompt body 指定）
- SSE 订阅（过程消息、权限请求、问题请求、最终回复）+ 自动断线重连
- 可用模型 / provider / agent / skill / 命令查询
- 权限与问题应答

## 安装

```
go get github.com/justphantom/opencode-go-sdk-lite
```

## 接口清单

### Client 配置

| Go API | 说明 |
|---|---|
| `New(baseURL, opts...)` | 构造 Client；baseURL 形如 `http://127.0.0.1:4096` |
| `WithToken(t)` | 设置 `Authorization: Bearer <t>` |
| `WithBasicAuth(u, p)` | 设置 HTTP Basic 认证（与 WithToken 互斥，后者覆盖前者） |
| `WithPassword(p)` | serve 密码模式快捷方式（Basic，用户名固定 `opencode`） |
| `WithHTTPClient(c)` | 注入自定义 `*http.Client` |
| `WithHeader(k, v)` | 追加/覆盖单个请求头 |
| `WithUserAgent(ua)` | 设置 `User-Agent` |

### Session 管理 — `session.go`

| Go API | HTTP | 说明 |
|---|---|---|
| `CreateSession(ctx, *CreateSessionReq)` | `POST /session` | Directory 走平铺 query，其余进 body |
| `ListSessions(ctx, *ListSessionsOpt)` | `GET /session` | 裸数组无游标；serve 默认 limit=100 会截断，SDK 默认上送 limit=200，更多需显式 Limit |
| `GetSession(ctx, sessionID)` | `GET /session/{id}` | |
| `DeleteSession(ctx, id)` | `DELETE /session/{id}` | 返回 false 视为错误 |
| `SessionStatuses(ctx)` | `GET /session/status` | map[sessionID]运行状态；空闲会话可能缺省，缺省即 idle（session.go:218） |
| `DeleteSessionIfIdle(ctx, id)` | （内部 SessionStatuses + DELETE） | busy 时拒绝且不发 DELETE；状态查询失败透传错误；查询与删除间有竞态，尽力而为（session.go:205） |
| `Prompt(ctx, id, *PromptReq) (*PromptAck, error)` | `POST /session/{id}/prompt_async` | 204 无 body；messageID/partID 由 SDK 生成并经 ack 回传；支持 model/agent/variant/system/tools 开关与 text/file 附件 part |
| `Interrupt(ctx, id)` | `POST /session/{id}/abort` | 空闲时为 no-op |
| `UpdateSession(ctx, id, *UpdateSessionReq)` | `PATCH /session/{id}` | 改标题/元数据/归档时间，返回更新后会话 |
| `ListMessages(ctx, id, *ListMessagesOpt)` | `GET /session/{id}/message` | 元素为 `{info, parts}`；`SessionMessage.FinalText()` 重组最终回复（过滤 synthetic/ignored），SSE 断连后兜底用 |
| `GetMessage(ctx, sessionID, messageID)` | `GET /session/{id}/message/{messageID}` | 单条消息（info+parts）；终止后取服务端落库最终回复用（session.go:264） |

### Client / Agent / Health — `client.go` / `agent.go`

| Go API | HTTP | 说明 |
|---|---|---|
| `Health(ctx)` | `GET /global/health` | 响应 `{healthy:true}` |
| `ListAgents(ctx, *LocationRef)` | `GET /agent` | 定位参数为平铺 query（directory/workspace） |
| `ListSkills(ctx, *LocationRef)` | `GET /skill` | 可用 skill（name/description/location/content） |
| `ListCommands(ctx, *LocationRef)` | `GET /command` | 可用命令（source ∈ command/mcp/skill） |

### SSE 订阅与断线重连 — `event.go` / `sse.go` / `globalstream.go`

| Go API | HTTP | 说明 |
|---|---|---|
| `SessionEvents(ctx, id, *SessionEventsOpt)` 返回 `(<-chan Event, <-chan error)` | `GET /event` | 全局流按 sessionID 过滤；无续传 |
| `NewGlobalEventStream(ctx, *LocationRef)` 返回 `*GlobalEventStream` | `GET /event` | 按 sessionID 路由的全局连接 |
| `stream.Subscribe(sessionID)` / `Unsubscribe(id)` / `Close()` | （基于上一行） | 多路复用 |
| `Run(ctx, stream, RunOptions)` 返回 `(<-chan HighEvent, error)` | 串联 prompt + 订阅 + 过滤 + 合成终止 | 详见「高层 Run + HighEvent」段 |

**SessionEvents**：连接全局 `/event` 后按 sessionID 过滤；指数退避（默认 500ms→30s）；
4xx（除 429）视为不可恢复写 errc 后停止。无续传，断连窗口的事件会丢失。

> **事件总线按 directory 隔离（实测）**：`SessionEventsOpt.Location` 与
> `NewGlobalEventStream` 的 `loc` 必须与目标会话的 directory 一致，
> 否则收不到这些会话的事件（只能收到默认目录的）。

**全局流**（`GlobalEventStream`）：指数退避 100ms→5s（连接存活 <2s 视为 flapping 不重置退避）；
心跳 watchdog 15s 无帧强制重连；panic recover。

事件类型见 `types.go` 的 `EventXxx` 常量（V1 经典事件体系：`message.part.*` / `message.updated` /
`session.*` / `permission.asked` / `question.asked` 等；实测不产生 `session.next.*` 事件）。
`Event.Properties` 为原始 JSON，调用方按 `Type` 自行反序列化；高频事件附 struct：
`PartDeltaData` / `PartUpdatedData` / `MessageUpdatedData` / `PermissionAskedData` /
`QuestionAskedData` / `SessionIdleData` / `SessionErrorData` / `TodoUpdatedData`。

工具调用事件可用 `ClassifyTool(name)` 归类：file_read / file_write / shell / search /
webfetch / mcp / subagent / todo / other（MCP 工具无统一前缀，归类为尽力而为）；
`Run` 产出的 tool_use / tool_result 事件经 `ToolKind()` 直接读取。

### 可用模型与 Provider — `model.go`

V1 无独立模型目录，三者统一走 `GET /provider`（响应 `{all, default, connected}`，模型内嵌于 provider）：

| Go API | 说明 |
|---|---|
| `ListModels(ctx, *LocationRef)` | 拍平 `all[].models`；`Enabled` 由 `status=="active"` 推导 |
| `ListProviders(ctx, *LocationRef)` | 返回 `all` |
| `GetProvider(ctx, providerID, *LocationRef)` | 从 `all` 按 id 筛选；未命中返回错误 |
| `ListConnectedProviders(ctx)` | 返回 `connected`（已连接 provider id 列表；不带 LocationRef，model.go:64） |

### 权限应答 — `permission.go`

V1 的权限接口是全局的；ListPermissions 拉全量后按 sessionID 过滤。

| Go API | HTTP | 说明 |
|---|---|---|
| `ListPermissions(ctx, sessionID)` | `GET /permission` | 全局 pending 列表，客户端过滤 |
| `ReplyPermission(ctx, rid, reply, message)` | `POST /permission/{rid}/reply` | reply ∈ `once`/`always`/`reject` |

### 问题应答 — `question.go`

| Go API | HTTP | 说明 |
|---|---|---|
| `ListQuestions(ctx, sessionID)` | `GET /question` | 全局 pending 列表，客户端过滤 |
| `ReplyQuestion(ctx, rid, *QuestionReply)` | `POST /question/{rid}/reply` | answers 与 questions 一一对应 |
| `RejectQuestion(ctx, rid)` | `POST /question/{rid}/reject` | |

### 非目标（明确未实现）

- agent/model 切换（V1 无独立 Switch 接口，随 Prompt body 指定）
- 主动创建权限请求（权限请求由服务端经事件推送）
- session 级 SSE（V1 仅全局 `/event`，无 after 续传）
- v2 全部 `/api/*` 端点
- fs / pty / lsp / mcp / integration / credential / tui / sync / vcs / worktree / workspace 等 spec 中存在但不在 scope 的接口
- 事件强类型 union（仅 scope 内高频事件附 properties struct）

## 快速开始

```go
package main

import (
	"context"
	"encoding/json"
	"fmt"

	oc "github.com/justphantom/opencode-go-sdk-lite"
)

func main() {
	client, err := oc.New("http://127.0.0.1:4096",
		oc.WithToken("your-token"),           // 可选；本地部署通常省略
	)
	if err != nil { panic(err) }

	ctx := context.Background()

	// 1. 列出可用模型
	models, _ := client.ListModels(ctx, &oc.LocationRef{Directory: "/repo"})
	fmt.Println("models:", len(models))

	// 2. 创建会话
	ses, err := client.CreateSession(ctx, &oc.CreateSessionReq{Directory: "/repo"})
	if err != nil { panic(err) }

	// 3. 订阅事件流（在 prompt 之前打开，避免丢帧；
	//    Location 必须与会话 directory 一致——事件总线按 directory 隔离）
	events, errc := client.SessionEvents(ctx, ses.ID, &oc.SessionEventsOpt{
		Location: &oc.LocationRef{Directory: "/repo"},
	})

	// 4. 发送消息；messageID/partID 由 SDK 生成并经 ack 回传
	go func() {
		_, _ = client.Prompt(ctx, ses.ID, &oc.PromptReq{
			Parts: []oc.PromptPart{{Type: "text", Text: "解释这个项目"}},
		})
	}()

	// 5. 消费事件直到 turn 结束
	for ev := range events {
		switch ev.Type {
		case oc.EventMessagePartDelta:
			var d oc.PartDeltaData
			_ = json.Unmarshal(ev.Properties, &d)
			fmt.Print(d.Delta)

		case oc.EventPermissionAsked:
			var d oc.PermissionAskedData
			_ = json.Unmarshal(ev.Properties, &d)
			// 自动放行一次
			_ = client.ReplyPermission(ctx, d.ID, oc.PermissionReplyOnce, "")

		case oc.EventQuestionAsked:
			var d oc.QuestionAskedData
			_ = json.Unmarshal(ev.Properties, &d)
			// 第一项作为默认答案
			ans := make([][]string, len(d.Questions))
			for i, q := range d.Questions {
				if len(q.Options) > 0 { ans[i] = []string{q.Options[0].Label} }
			}
			_ = client.ReplyQuestion(ctx, d.ID, &oc.QuestionReply{Answers: ans})

		case oc.EventMessagePartUpdated:
			// 实测：真实完成信号是 step-finish part 且 reason="stop"（session.idle 兜底）
			var d oc.PartUpdatedData
			_ = json.Unmarshal(ev.Properties, &d)
			if d.Part.Type == "step-finish" && d.Part.Reason == "stop" {
				fmt.Println("\n[done]")
				return
			}
		}
	}
	if err := <-errc; err != nil {
		fmt.Println("stream error:", err)
	}
}
```

## 重连策略

`SessionEvents` 与 `GlobalEventStream` 均内置指数退避重连：
`BackoffMin` 按 2^n 增长，封顶 `BackoffMax`；4xx（除 429）不可恢复。

```go
events, errc := client.SessionEvents(ctx, ses.ID, &oc.SessionEventsOpt{
	BackoffMin:  200 * time.Millisecond,
	BackoffMax:  10 * time.Second,
	MaxAttempts: 10,                     // 0 = 无限
})
```

注意：全局 `/event` 不支持续传，断连窗口的事件会丢失。

## 全局事件流（GlobalEventStream）

`GlobalEventStream` 维护一条到 `/event` 的全局长连，按 `sessionID` 把事件路由给多个订阅者。
适合需要并发处理多个会话的宿主（HTTP 网关、机器人适配层等）。健壮性移植自 lark-bridge：
指数退避（100ms→5s，连接存活 <2s 视为 flapping 不重置退避）、心跳 watchdog（15s 无帧强制重连破半开 TCP）、
panic recover。

> 断连窗口的 delta 事件会丢失；终止事件（`idle`/`error`/`deleted`）保证送达。
> `loc` 必须与目标会话 directory 一致（事件总线按 directory 隔离）。

```go
ctx, cancel := context.WithCancel(context.Background())
defer cancel()

stream, _ := client.NewGlobalEventStream(ctx, &oc.LocationRef{Directory: "/repo"})
defer stream.Close()

// 为任意 sessionID 订阅
ch := stream.Subscribe("ses_xxx")
defer stream.Unsubscribe("ses_xxx") // 或 Close 自动关闭

for ev := range ch {
    // ev.Type / ev.Properties 同 SessionEvents 的事件结构
}
```

## 高层 Run + HighEvent（推荐）

`Run` 把「创建/复用 session → 订阅全局流 → 发 prompt_async → 按 assistantMessageID 过滤 → 合成终止事件」打包，
把原始事件归纳为 11 种 `HighEventKind`，channel close 前必有终止事件（result/error）。
首事件必为 `HighEventPrompt`（携带 SDK 生成的 user messageID 与 sessionID）。

```go
loc := &oc.LocationRef{Directory: "/repo"}
stream, _ := client.NewGlobalEventStream(ctx, loc)
defer stream.Close()

out, err := client.Run(ctx, stream, oc.RunOptions{
	Prompt:   "解释这个项目",
	Location: loc,
	// SessionID 空则内部 CreateSession；Model/Agent 随本条消息生效
})
if err != nil { return err }

for ev := range out {
	switch ev.Kind() {
	case oc.HighEventPrompt:
		fmt.Println("session:", ev.SessionID(), "user msg:", ev.MessageID())
	case oc.HighEventText:
		fmt.Print(ev.Text())
	case oc.HighEventThinking:
		// 推理增量（如模型支持）
	case oc.HighEventToolUse:
		fmt.Printf("\n[tool: %s] %s\n", ev.ToolName(), ev.ToolInput())
	case oc.HighEventToolResult:
		if ev.IsToolError() { fmt.Println("  (failed)") }
	case oc.HighEventPermissionAsked:
		// agent 请求权限：ev.PermissionAsked() → ReplyPermission 应答（见下文）
	case oc.HighEventQuestionAsked:
		// agent 向用户提问：ev.QuestionAsked() → ReplyQuestion/RejectQuestion 应答
	case oc.HighEventResult:
		fmt.Printf("\n[done] in=%d out=%d cost=%.4f\n",
			ev.InputTokens(), ev.OutputTokens(), ev.Cost())
		return
	case oc.HighEventError:
		return // 出错终止
	}
}
```

完成信号是 step-finish part 且 `reason="stop"`（实测确认；`session.idle` 作兜底终止）。
`HighEventResult` 的结果文本优先取服务端落库文本（`GetMessage` 的 `FinalText`，免疫 SSE 丢帧）；
取不到或为空则回退 SSE 累积的 text delta（run.go:120-151）。

### 权限/提问事件（permission_asked / question_asked）

agent 运行中请求权限（bash 等）或向用户提问时，`Run` 透出 `HighEventPermissionAsked` /
`HighEventQuestionAsked`（非终止：chan 不 close，turn 挂起等应答）。消费模式：

1. 收到 asked 事件，取 `ev.PermissionAsked()` / `ev.QuestionAsked()`（仅对应 kind 非 nil）；
2. 调 `ReplyPermission` / `ReplyQuestion` / `RejectQuestion` 应答；
3. 应答后 turn 继续流式输出。

不回复 serve 端 agent 会一直挂起——中断/超时务必回复 reject。
关联工具调用用 payload 的 `Tool.CallID`，不要靠时序：asked 相对对应 tool_use（running）
事件的先后不稳定（实测 permission.asked 在 running 之后、question.asked 在 running 之前）。

## 辅助 API

| Go API | 说明 |
|---|---|
| `client.Health(ctx)` | 健康检查；`{healthy:true}` |
| `client.ListAgents(ctx, *LocationRef)` | 列出 agent（build/plan/explore...） |
| `client.DeleteSession(ctx, id)` | 删除会话 |
| `GenerateMessageID()` / `GeneratePartID()` | 生成 `msg_`/`prt_` 前缀 id，NTP 回拨安全；`Prompt` 内部自动调用，仅需要预关联时手动用 |
| `ClassifyTool(name)` | 工具名归类（file_read/file_write/shell/search/webfetch/mcp/subagent/todo/other） |
| `NewHighEvent(...)` | 构造 HighEvent；仅供外部包测试 fake 用，业务代码不应调用 |
| `PromptAck` | `Prompt` 回执；prompt_async 返 204 无 body，ack 的 `MessageID`/`PartIDs` 是关联后续 SSE 事件的唯一句柄 |

## 约束

- 零第三方依赖，仅标准库
- 原始事件：`Type` 常量 + `Properties json.RawMessage`（不做 88 事件强类型 union，仅高频事件附 `*Data` struct）
- 高层事件：`HighEventKind` 11 种 + Getter（封装在 `Run`）
- 全局流不支持续传，断连窗口事件丢失
- 其他未覆盖接口见「接口清单 → 非目标」
