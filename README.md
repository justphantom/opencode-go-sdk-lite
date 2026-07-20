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
| `Prompt(ctx, id, *PromptReq)` | `POST /api/session/{id}/prompt` | `v2.session.prompt` |
| `Interrupt(ctx, id)` | `POST /api/session/{id}/interrupt` | `v2.session.interrupt` |
| `SwitchAgent(ctx, id, agent)` | `POST /api/session/{id}/agent` | `v2.session.switchAgent` |
| `SwitchModel(ctx, id, ModelRef)` | `POST /api/session/{id}/model` | `v2.session.switchModel` |
| `ListMessages(ctx, id, *ListMessagesOpt)` | `GET /api/session/{id}/message` | `v2.session.messages` |

### SSE 订阅与断线重连 — `event.go` / `sse.go`

| Go API | HTTP | spec operationId |
|---|---|---|
| `SessionEvents(ctx, id, *SessionEventsOpt)` 返回 `(<-chan Event, <-chan error)` | `GET /api/session/{id}/event?after=<seq>` | `v2.session.events` |

重连策略：维护 `lastSeq`，断线后用 `?after=lastSeq` 续传并按 `durable.seq` 去重；指数退避（默认 500ms→30s）；4xx（除 429）视为不可恢复。

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

- 全局事件流 `GET /api/event`（`v2.event.subscribe`）— scope 限定 session-scoped
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

		case oc.EventSessionIdle:
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

`SessionEvents` 内部维护 `lastSeq`，断线后用 `?after=lastSeq` 重连并按 `durable.seq` 去重。
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

## 约束

- 零第三方依赖，仅标准库
- 事件建模：`Type` 常量 + `Data json.RawMessage`（不做 88 事件强类型 union，仅 scope 内高频事件附 `*Data` struct）
- 不实现全局 `/api/event` 订阅（仅 session-scoped）
- 其他未覆盖接口见「接口清单 → 非目标」
