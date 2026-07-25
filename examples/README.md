# examples/

集成本 SDK 的可运行示例。每个子目录是独立的 `package main`，直接 `go run`。

## 清单

| 示例 | 场景 | 关键 API |
|---|---|---|
| [`basic/`](./basic) | 最小路径：一轮对话 + 完整 turn 报告 | `New` → `Health` → `ListModels` → `NewGlobalEventStream` → `Run` → `HighEventResult`（v0.3.0 完整元数据） |
| [`auto-reply/`](./auto-reply) | 权限/问题自动应答 | `SessionEvents` + `ReplyPermission` / `ReplyQuestion` |
| [`concurrent/`](./concurrent) | 多 session 共享一条全局流 | `GlobalEventStream.Subscribe` + `Prompt` |
| [`observability/`](./observability) | v0.2 可观测性 + ctx 取消兜底（drain） | `WithLogger` + `OnIdle` + `RunWithHandle.WaitTerminal` |
| [`session-crud/`](./session-crud) | session 管理面全生命周期 | `Create/List/Get/UpdateSession/ListMessages/Delete` |

## 前置条件

任意一个 demo 都需要先起 opencode serve：

```
opencode serve --port 6096   # 或用默认 4096
```

无口令模式即可。需要鉴权时用 `-token` 传入。

## 通用参数

所有 demo 共享：

```
-url    opencode serve 地址（默认 http://127.0.0.1:6096）
-token  Bearer token（本地部署可空）
-dir    工作区目录 LocationRef.Directory（默认当前目录）
-timeout 整体超时
```

## 运行

```
go run ./examples/basic
go run ./examples/basic -show-thinking              # 实时打印思考增量
go run ./examples/auto-reply -perm always
go run ./examples/concurrent -n 3
go run ./examples/observability                     # 默认配置
go run ./examples/observability -cancel-after 10s   # 演示 ctx 取消 + drain
go run ./examples/session-crud
```

## 选型建议

- 单轮问答 / ChatOps 机器人 → `basic`
- 无人值守 agent / CI 集成 → `auto-reply`
- 多租户网关 / 一进多出 → `concurrent`
- 长链路订阅者（如 bridge）/ SSE 故障定位 / ctx 取消要不丢终止事件 → `observability`
- 管理面板 / session 列表页 → `session-crud`

## 注意

- 全局流（`GlobalEventStream`）V1 不支持 `?after=`，断连窗口的 delta 事件会丢；终
  止事件保证送达。强一致需求请改用 `SessionEvents`（session-scoped，V1 也不支持
  `?after=` 续传，但 chan 容量更大且无多路复用抢断，单 session 场景比全局流稳）。
- `ListMessages` 是最终一致：prompt 后约 3s 才返回新消息，不要用它判 prompt 成功。
