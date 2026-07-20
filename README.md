# opencode-go-sdk-lite

opencode v2 HTTP API 的轻量 Go SDK，纯标准库实现。

覆盖范围：
- 会话发起（创建 / prompt / 中断 / 切 agent / 切 model / 历史）
- SSE 订阅（过程消息、权限请求、问题请求、最终回复）+ 自动断线重连
- 可用模型 / provider 查询
- 权限与问题应答

## 安装

```
go get github.com/opencode-ai/opencode-go-sdk-lite
```

## 快速开始

```go
package main

import (
	"context"
	"encoding/json"
	"fmt"

	oc "github.com/opencode-ai/opencode-go-sdk-lite"
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

## 事件类型

`types.go` 中 `EventXxx` 常量完整覆盖 spec 的 88 种事件。`Event.Data` 是原始 JSON，
调用方按 `Type` 自行反序列化；scope 内高频事件提供了对应 `*Data` struct（如 `TextDeltaData`）。

## 约束

- 零第三方依赖，仅标准库
- 不做 88 事件的强类型 union，按 Type 透传 RawMessage
- 不实现全局 `/api/event` 订阅（仅 session-scoped）
