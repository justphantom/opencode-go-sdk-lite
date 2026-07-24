package opencode

import (
	"context"
	"encoding/json"
)

// ListTodos 返回会话当前的 todo 全量列表（GET /session/{id}/todo）。
// 用作 todo.updated SSE 丢帧时的补偿恢复源；Todos 为全量覆盖列表，非增量。
func (c *Client) ListTodos(ctx context.Context, sessionID string) ([]Todo, error) {
	var out []Todo
	if err := c.doJSON(ctx, http_GET, "/session/"+sessionID+"/todo", nil, nil, &out, 0); err != nil {
		return nil, err
	}
	return out, nil
}

// pollTodo 补偿轮询 GET /session/{id}/todo，按快照签名去重：仅在 todos 实际变化时投递。
// 首次接触且为空时静默（turn 开始无 todo 不算事件，避免噪声），但登记签名使后续可比对。
// 轮询失败（网络/4xx）吞掉，不终止 pump——补偿路径自身不可成为 turn 失败的原因。
//
// baselineOnly=true 时仅登记签名后吞掉，不投递：复用 session 的 turn 首轮专用。
// 服务端持久化的 todo 是上一轮残留（Todo 无 ID/时间戳，无法逐项区分新旧），
// 当作基线吞掉；本 turn 内 todo 若有变化，靠 SSE 或后续 ticker 轮询按签名差异补投。
func (c *Client) pollTodo(ctx context.Context, sessionID string, lastSig *string, out chan<- HighEvent, baselineOnly bool) {
	todos, err := c.ListTodos(ctx, sessionID)
	if err != nil {
		return
	}
	b, _ := json.Marshal(todos)
	sig := string(b)
	if sig == *lastSig {
		return
	}
	if baselineOnly {
		*lastSig = sig // 复用 session 首轮：登记残留为基线，不投递
		return
	}
	if len(todos) == 0 && *lastSig == "" {
		*lastSig = sig // 首次接触且空：登记但不投递
		return
	}
	*lastSig = sig
	d := TodoUpdatedData{SessionID: sessionID, Todos: todos}
	select {
	case <-ctx.Done():
	case out <- HighEvent{kind: HighEventTodoUpdated, sessionID: sessionID, todo: &d}:
	}
}

// registerTodo 登记并去重 SSE 来源的 todo.updated 事件，按全量快照的 json 签名去重：
// 已见相同快照（SSE 或轮询任一先登记）则返回 false 丢弃。Todo 无稳定 ID，
// 故用 json.Marshal(Todos) 作签名；与 pollTodo 共享同一 lastSig 指针，双向去重。
func registerTodo(he *HighEvent, lastSig *string) bool {
	if he.kind != HighEventTodoUpdated || he.todo == nil {
		return true
	}
	b, err := json.Marshal(he.todo.Todos)
	if err != nil {
		return true // 无法计算签名，不拦截（容错）
	}
	sig := string(b)
	if sig == *lastSig {
		return false
	}
	*lastSig = sig
	return true
}
