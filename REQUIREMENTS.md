# 项目需求清单

> 目的：封装一个让客户端易用、友好地与 `opencode serve` 交互的 Go SDK。
> 接口基线：`http://127.0.0.1:4096/doc`（OpenAPI 3.1 JSON），本地快照 `opencode-openapi.json`。
> 范围：v1 接口体系（`/session`、`/event`、`/permission`、`/question`、`/provider`、`/agent` 等），不含 v2 `/api/*`。
> 状态标记：✅ 已实现 / ⚠️ 部分实现 / ❌ 未实现（核对日期 2026-07-21）。

## 1. Client 基础能力

| # | 需求 | 状态 |
|---|---|---|
| 1.1 | 构造 Client：`New(baseURL, opts...)` | ✅ |
| 1.2 | 认证：Bearer Token / HTTP Basic / serve 密码模式快捷方式 | ✅ |
| 1.3 | 自定义 `*http.Client`、自定义请求头、User-Agent | ✅ |
| 1.4 | 健康检查 `GET /global/health` | ✅ |
| 1.5 | 零第三方依赖，仅标准库 | ✅ |

## 2. Session 增删改查

| # | 需求 | HTTP | 状态 |
|---|---|---|---|
| 2.1 | 创建会话（可指定 directory/title/父会话） | `POST /session` | ✅ |
| 2.2 | 会话列表 | `GET /session` | ✅ |
| 2.3 | 会话详情 | `GET /session/{id}` | ✅ |
| 2.4 | 删除会话 | `DELETE /session/{id}` | ✅ |
| 2.5 | 更新会话（PATCH，如改标题） | `PATCH /session/{id}` | ✅ |

## 3. 对话发起与中止

| # | 需求 | HTTP | 状态 |
|---|---|---|---|
| 3.1 | 异步发起对话（立即返回，结果走 SSE） | `POST /session/{id}/prompt_async` | ✅ |
| 3.2 | messageID/partID 由 SDK 生成并经 ack 回传（事件关联句柄） | — | ✅ |
| 3.3 | prompt 可指定 model、agent、variant（思考深度）、system、tools、文件附件 part | — | ✅ |
| 3.4 | 中止当前对话 | `POST /session/{id}/abort` | ✅ |

## 4. 事件订阅（SSE）

| # | 需求 | 状态 |
|---|---|---|
| 4.1 | 订阅全局事件流 `GET /event`，按 sessionID 过滤出单会话流 | ✅ |
| 4.2 | 全局流多路复用：一条长连服务多 session（Subscribe/Unsubscribe/Close） | ✅ |
| 4.3 | 断线自动重连：指数退避、4xx（除 429）不可恢复、心跳 watchdog 破半开 | ✅ |
| 4.4 | 事件总线按 directory 隔离：Location 必须与会话 directory 一致（文档化约束） | ✅ |
| 4.5 | 无续传；断连窗口事件丢失由第 8 节兜底接口弥补（文档化约束） | ✅ |

## 5. SSE 事件友好解析

| # | 需求 | 状态 |
|---|---|---|
| 5.1 | 对话开始标志：Run 首事件携带 sessionID 与 user messageID | ✅ |
| 5.2 | 对话结束标志：step-finish 且 reason="stop" 为主信号，session.idle 兜底，channel 关闭前必有终止事件（result/error） | ✅ |
| 5.3 | 思考内容：reasoning 增量解析为独立事件类型 | ✅ |
| 5.4 | 最终回复组装：`SessionMessage.FinalText()` 拼接非 synthetic/ignored 的 text part；Run 流内累积 delta 回填 result | ✅ |
| 5.5 | 工具调用过程：tool_use（发起，含工具名+输入）/ tool_result（结果，含失败标记） | ✅ |
| 5.6 | 工具类型细分识别：读写文件、执行 shell、搜索、抓取网页、调用 MCP、subagent、todo | ✅ `ClassifyTool` + HighEvent.ToolKind()；MCP 无前缀约定，尽力而为 |
| 5.7 | question 消息解析（questions/options/multiple/custom 结构） | ✅ |
| 5.8 | permission 消息解析（permission/patterns/metadata/tool 关联） | ✅ |
| 5.9 | todo 进展事件（todo.updated）纳入友好事件体系 | ✅ TodoUpdatedData struct |
| 5.10 | 高频事件附 properties struct；全量事件类型常量（V1 经典体系） | ✅ |

## 6. Question / Permission 回复

| # | 需求 | HTTP | 状态 |
|---|---|---|---|
| 6.1 | permission 回复：once / always / reject（可附 message） | `POST /permission/{rid}/reply` | ✅ |
| 6.2 | question 回复：answers 与 questions 一一对应（每题 label 数组） | `POST /question/{rid}/reply` | ✅ |
| 6.3 | question 拒绝 | `POST /question/{rid}/reject` | ✅ |

## 7. 能力查询

| # | 需求 | HTTP | 状态 |
|---|---|---|---|
| 7.1 | 可用模型查询（拍平 provider 内嵌模型，含 Enabled 推导） | `GET /provider` | ✅ |
| 7.2 | 模型上下文大小（limit.context/output/input） | 同上 | ✅ |
| 7.3 | 模型额外变量（variants，如思考深度） | 同上 | ✅ |
| 7.4 | provider 列表与单个查询 | 同上 | ✅ |
| 7.5 | 可用 agent 查询 | `GET /agent` | ✅ |
| 7.6 | 可用 skill 查询 | `GET /skill` | ✅ |
| 7.7 | 可用命令查询 | `GET /command` | ✅ |

## 8. SSE 断连兜底

| # | 需求 | HTTP | 状态 |
|---|---|---|---|
| 8.1 | 待处理 permission 查询（全局拉取后按 sessionID 过滤） | `GET /permission` | ✅ |
| 8.2 | 待处理 question 查询（同上） | `GET /question` | ✅ |
| 8.3 | 最终回复消息查询（历史消息 `{info, parts}`，`FinalText()` 重组最终回复） | `GET /session/{id}/message` | ✅ |

## 9. 高层封装（Run）

| # | 需求 | 状态 |
|---|---|---|
| 9.1 | 一条龙：创建/复用 session → 订阅 → prompt_async → 按 assistant 消息过滤 → 合成终止 | ✅ |
| 9.2 | 高层事件类型 ≤10 种，Getter 访问，channel 关闭前必有 result/error | ✅ |

## 10. 工程约束

| # | 需求 | 状态 |
|---|---|---|
| 10.1 | 仅标准库；原始事件不做强类型 union（Type 常量 + RawMessage + 高频 struct） | ✅ |
| 10.2 | 功能有标准库测试；单文件 ≤300 行 | ✅ |
| 10.3 | 示例：basic / session-crud / auto-reply / concurrent | ✅ |

## 非目标（明确不做）

- v2 全部 `/api/*` 端点与 `session.next.*` / `permission.v2.*` 事件体系
- agent/model 切换独立接口（V1 随 Prompt body 指定）
- 主动创建 permission 请求（由服务端事件推送）
- session 级 SSE 与 after 续传（V1 仅全局 `/event`）
- fs / pty / lsp / formatter / mcp 管理 / integration / credential / tui / sync / vcs / worktree / workspace / share / fork / revert / summarize / diff 等 spec 内但不在上述清单的接口
- 88 种事件强类型 union

## 待办缺口汇总

无。历史缺口（skill/command 查询、会话更新、工具类型细分、todo 事件）已于 2026-07-21 补齐，
集成测试 `go test -tags=integration -run TestIntegration .` 14 个子测试全部实测通过
（含 permission/question 应答、shell 工具分类、skill/command 查询、标题更新回读的线上验证）。
