# opencode-go-sdk-lite

opencode v2 HTTP API 的轻量 Go SDK，纯标准库实现。

覆盖范围：
- 会话发起（创建 / prompt / 中断 / 切 agent / 切 model / 历史）
- SSE 订阅（过程消息、权限请求、问题请求、最终回复）+ 自动断线重连
- 可用模型 / provider 查询
- 权限与问题应答

## 安装

```
go get github.com/justphantom/opencode-go-sdk-lite
```

## 接口清单

按 opencode v2 OpenAPI 的 operationId 列出已实现的方法。未实现项见底部「非目标」。

### Client 配置

| Go API | 说明 |
|---|---|
| `New(baseURL, opts...)` | 构造 Client；baseURL 形如 `http://127.0.0.1:4096` |
| `WithToken(t)` | 设置 `Authorization: Bearer <t>` |
| `WithHTTPClient(c)` | 注入自定义 `*http.Client` |
| `WithHeader(k, v)` | 追加/覆盖单个请求头 |
| `WithUserAgent(ua)` | 设置 `User-Agent` |

### Session 管理 — `session.go`

| Go API | HTTP | spec operationId |
|---|---|---|
| `CreateSession(ctx, *CreateSessionReq)` | `POST /api/session` | `v2.session.create` |
| `ListSessions(ctx, *ListSessionsOpt)` | `GET /api/session` | `v2.session.list`（支持 cursor 分页） |
| `GetSession(ctx, sessionID)` | `GET /api/session/{id}` | `v2.session.get` |
| `DeleteSession(ctx, id)` | `DELETE /session/{id}` | v2 spec 未声明；走 v1 端点（实测真删） |
| `Prompt(ctx, id, *PromptReq)` | `POST /api/session/{id}/prompt` | `v2.session.prompt`（异步入队，立即返回 `SessionInputAdmitted`；模型回复走 SSE） |
| `Interrupt(ctx, id)` | `POST /api/session/{id}/interrupt` | `v2.session.interrupt` |
| `SwitchAgent(ctx, id, agent)` | `POST /api/session/{id}/agent` | `v2.session.switchAgent` |
| `SwitchModel(ctx, id, ModelRef)` | `POST /api/session/{id}/model` | `v2.session.switchModel` |
| `ListMessages(ctx, id, *ListMessagesOpt)` | `GET /api/session/{id}/message` | `v2.session.messages` |

### Client / Agent / Health — `client.go` / `agent.go`

| Go API | HTTP | spec operationId |
|---|---|---|
| `Health(ctx)` | `GET /api/health` | `v2.health.get`（响应 `{healthy:true}`） |
| `ListAgents(ctx, *LocationRef)` | `GET /api/agent` | `v2.agent.list` |

### SSE 订阅与断线重连 — `event.go` / `sse.go` / `globalstream.go`

| Go API | HTTP | spec operationId |
|---|---|---|
| `SessionEvents(ctx, id, *SessionEventsOpt)` 返回 `(<-chan Event, <-chan error)` | `GET /api/session/{id}/event?after=<seq>` | `v2.session.events` |
| `NewGlobalEventStream(ctx)` 返回 `*GlobalEventStream` | `GET /api/event` | `v2.event.subscribe` |
| `stream.Subscribe(sessionID)` / `Unsubscribe(id)` / `Close()` | （基于上一行） | 按 sessionID 路由的全局连接 |
| `Run(ctx, stream, RunOptions)` 返回 `<-chan HighEvent` | 串联 prompt + 订阅 + 过滤 + 合成终止 | 详见「高层 Run + HighEvent」段 |

**session-scoped 重连**（`SessionEvents`）：维护 `lastSeq`，断线后用 `?after=lastSeq` 续传并按 `durable.seq` 去重；指数退避（默认 500ms→30s）；4xx（除 429）视为不可恢复。

**全局流**（`GlobalEventStream`）：spec 无 `?after=` 支持，断连窗口 delta 事件丢失；指数退避 100ms→5s（连接存活 <2s 视为 flapping 不重置退避）；心跳 watchdog 15s 无帧强制重连；panic recover。

事件类型见 `types.go` 的 `EventXxx` 常量（完整覆盖 spec 88 种 type 字符串）。`Event.Data` 为原始 JSON，调用方按 `Type` 自行反序列化；高频事件附 `*Data` struct：`TextDeltaData` / `ToolCalledData` / `ToolSuccessData` / `ToolFailedData` / `StepEndedData` / `PermissionAskedData` / `QuestionAskedData` / `SessionIdleData` / `SessionErrorData`。

### 可用模型与 Provider — `model.go`

| Go API | HTTP | spec operationId |
|---|---|---|
| `ListModels(ctx, *LocationRef)` | `GET /api/model` | `v2.model.list`（query 用 `location[directory]` deepObject） |
| `ListProviders(ctx, *LocationRef)` | `GET /api/provider` | `v2.provider.list` |
| `GetProvider(ctx, providerID, *LocationRef)` | `GET /api/provider/{id}` | `v2.provider.get` |

### 权限应答 — `permission.go`

| Go API | HTTP | spec operationId |
|---|---|---|
| `ListPermissions(ctx, sessionID)` | `GET /api/session/{id}/permission` | `v2.session.permission.list` |
| `CreatePermission(ctx, id, *CreatePermissionReq)` | `POST /api/session/{id}/permission` | `v2.session.permission.create` |
| `ReplyPermission(ctx, sid, rid, reply, message)` | `POST /api/session/{id}/permission/{rid}/reply` | `v2.session.permission.reply`（reply ∈ `once`/`always`/`reject`） |

### 问题应答 — `question.go`

| Go API | HTTP | spec operationId |
|---|---|---|
| `ListQuestions(ctx, sessionID)` | `GET /api/session/{id}/question` | `v2.session.question.list` |
| `ReplyQuestion(ctx, sid, rid, *QuestionReply)` | `POST /api/session/{id}/question/{rid}/reply` | `v2.session.question.reply` |
| `RejectQuestion(ctx, sid, rid)` | `POST /api/session/{id}/question/{rid}/reject` | `v2.session.question.reject` |

### 非目标（明确未实现）

- Session 的 `compact` / `context` / `history` / `wait` / `revert.*` / `permission.get`（单条详情）
- 全局 `/api/permission/request` / `/api/permission/saved*`
- fs / pty / lsp / mcp / integration / credential / tui / sync / vcs / worktree / workspace 等 spec 中存在但不在 scope 的接口
- 88 种事件的强类型 union（仅 scope 内高频事件附 Data struct）

需要扩展时，按上表 operationId 在 spec 中查 schema 即可对齐。

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

	// 2. 创建会话并发送消息
	ses, err := client.CreateSession(ctx, &oc.CreateSessionReq{
		Location: &oc.LocationRef{Directory: "/repo"},
	})
	if err != nil { panic(err) }

	// 3. 订阅事件流（在 prompt 之前打开，避免丢帧）
	events, errc := client.SessionEvents(ctx, ses.ID, nil)

	// 4. 发送消息
	go func() {
		_, _ = client.Prompt(ctx, ses.ID, &oc.PromptReq{
			Prompt: oc.PromptInput{Text: "解释这个项目"},
		})
	}()

	// 5. 消费事件直到会话空闲
	for ev := range events {
		switch ev.Type {
		case oc.EventSessionNextTextDelta:
			var d oc.TextDeltaData
			_ = json.Unmarshal(ev.Data, &d)
			fmt.Print(d.Delta)

		case oc.EventPermissionV2Asked:
			var d oc.PermissionAskedData
			_ = json.Unmarshal(ev.Data, &d)
			// 自动放行一次
			_ = client.ReplyPermission(ctx, ses.ID, d.ID, oc.PermissionReplyOnce, "")

		case oc.EventQuestionV2Asked:
			var d oc.QuestionAskedData
			_ = json.Unmarshal(ev.Data, &d)
			// 第一项作为默认答案
			ans := make([][]string, len(d.Questions))
			for i, q := range d.Questions {
				if len(q.Options) > 0 { ans[i] = []string{q.Options[0].Label} }
			}
			_ = client.ReplyQuestion(ctx, ses.ID, d.ID, &oc.QuestionReply{Answers: ans})

		case oc.EventSessionNextStepEnded:
			// 实测：真实完成信号是 step.ended 且 finish="stop"（spec 写的 session.idle 实际不发）
			fmt.Println("\n[done]")
			return
		}
	}
	if err := <-errc; err != nil {
		fmt.Println("stream error:", err)
	}
}
```

## 重连策略

`SessionEvents`（session-scoped）内部维护 `lastSeq`，断线后用 `?after=lastSeq` 重连并按 `durable.seq` 去重。
退避：`BackoffMin`（默认 500ms）按 2^n 增长，封顶 `BackoffMax`（默认 30s）。
4xx（除 429）视为不可恢复，立即把错误写入 errc 并停止重连。

```go
events, errc := client.SessionEvents(ctx, ses.ID, &oc.SessionEventsOpt{
	After:       1234,                   // 从指定 seq 续传
	BackoffMin:  200 * time.Millisecond,
	BackoffMax:  10 * time.Second,
	MaxAttempts: 10,                     // 0 = 无限
})
```

## 全局事件流（GlobalEventStream）

`GlobalEventStream` 维护一条到 `/api/event` 的全局长连，按 `sessionID` 把事件路由给多个订阅者。
适合需要并发处理多个会话的宿主（HTTP 网关、机器人适配层等）。健壮性移植自 lark-bridge：
指数退避（100ms→5s，连接存活 <2s 视为 flapping 不重置退避）、心跳 watchdog（15s 无帧强制重连破半开 TCP）、
panic recover。

> 全局流 spec 无 `?after=` 续传，断连窗口的 delta 事件会丢失；终止事件（`step.ended`/`step.failed`/`idle`/`error`/`deleted`）保证送达。

```go
ctx, cancel := context.WithCancel(context.Background())
defer cancel()

stream, _ := client.NewGlobalEventStream(ctx)
defer stream.Close()

// 为任意 sessionID 订阅
ch := stream.Subscribe("ses_xxx")
defer stream.Unsubscribe("ses_xxx") // 或 Close 自动关闭

for ev := range ch {
    // ev.Type / ev.Data 同 SessionEvents 的事件结构
}
```

## 高层 Run + HighEvent（推荐）

`Run` 把「创建/复用 session → 发 prompt → 订阅全局流 → 按 assistantMessageID 过滤 → 合成终止事件」打包，
把 88 种原始 type 归纳为 10 种 `HighEventKind`，channel close 前必有终止事件（result/error）。
首事件必为 `HighEventPrompt`（携带 user messageID 与 sessionID）。

```go
stream, _ := client.NewGlobalEventStream(ctx)
defer stream.Close()

out, err := client.Run(ctx, stream, oc.RunOptions{
	Prompt:   "解释这个项目",
	Location: &oc.LocationRef{Directory: "/repo"},
	// SessionID 空则内部 CreateSession；Model/Agent 可选
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
	case oc.HighEventResult:
		fmt.Printf("\n[done] in=%d out=%d cost=%.4f\n",
			ev.InputTokens(), ev.OutputTokens(), ev.Cost())
		return
	case oc.HighEventError:
		return // 出错终止
	}
}
```

完成信号是 `session.next.step.ended` 且 `data.finish="stop"`（实测确认；spec 里写的 `session.idle` 实际服务端不发）。

## 辅助 API

| Go API | 说明 |
|---|---|
| `client.Health(ctx)` | 健康检查；`{healthy:true}` |
| `client.ListAgents(ctx, *LocationRef)` | 列出 agent（build/plan/explore...） |
| `client.DeleteSession(ctx, id)` | 走 v1 `DELETE /session/{id}`（v2 spec 无此端点） |
| `GenerateMessageID()` / `GenerateMessageIDAt(ms)` | 生成 `msg_` 前缀 id，NTP 回拨安全；v2 prompt 可选 |

## 约束

- 零第三方依赖，仅标准库
- 原始事件：`Type` 常量 + `Data json.RawMessage`（不做 88 事件强类型 union，仅高频事件附 `*Data` struct）
- 高层事件：`HighEventKind` 10 种 + Getter（封装在 `Run`）
- 全局流不支持 `?after=` 续传（spec 与实测确认）；session-scoped 流支持
- 其他未覆盖接口见「接口清单 → 非目标」
