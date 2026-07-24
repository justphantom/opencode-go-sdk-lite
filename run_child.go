package opencode

import (
	"context"
	"sync"
)

// askedTracker 跨主订阅与子 session 转发去重 asked 事件，按 requestID 全局唯一。
// 父子 session 共享一条 GlobalEventStream，asked 可能从 SSE 与 REST 轮询双路、
// 从父 sid 与子 sid 多来源到达，必须去重避免重复投递给消费方。
type askedTracker struct {
	mu   sync.Mutex
	seen map[string]bool
}

// register 登记 asked 的 requestID；已登记返回 false（应丢弃）。
// 复用包级 registerAsked 的 ID 提取逻辑，单一数据源。
func (a *askedTracker) register(he *HighEvent) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return registerAsked(he, a.seen)
}

// childTracker 维护 subagent 子 session 的订阅与 forward goroutine 生命周期。
//
// task 工具在父 turn 内 spawn 独立子 session（sessions.create{parentID}），
// 子 session 的 permission.asked / question.asked 事件 sessionID 是子 sid，
// GlobalEventStream.dispatch 按 sessionID 严格路由 → 父订阅者收不到。
// childTracker 周期性发现子 session、订阅其事件 chan、起 goroutine 只转发 asked
// 到父 out chan。子 session 的 text/tool/idle 事件一律丢——否则会让父 pump 误判
// turn 结束或污染文本流。
type childTracker struct {
	stream    *GlobalEventStream
	out       chan<- HighEvent
	asked     *askedTracker
	directory string

	ctx    context.Context
	cancel context.CancelFunc

	mu   sync.Mutex
	subs map[string]struct{} // 已订阅的子 sid（防 Subscribe 重入切断旧 chan）
	wg   sync.WaitGroup      // forward goroutine 同步
}

func newChildTracker(stream *GlobalEventStream, out chan<- HighEvent, asked *askedTracker, directory string, parent context.Context) *childTracker {
	ctx, cancel := context.WithCancel(parent)
	return &childTracker{
		stream:    stream,
		out:       out,
		asked:     asked,
		directory: directory,
		ctx:       ctx,
		cancel:    cancel,
		subs:      make(map[string]struct{}),
	}
}

// discover 拉取 parentSID 的全部子孙 session 并对新 sid 建立订阅 + 起 forward。
// 递归覆盖嵌套 subagent（cfg.subagent_depth 默认 1，实际只一层；用户调高时自动覆盖）。
// 失败吞掉：补偿路径不可成为 turn 失败原因，下一轮 ticker 重试。
func (t *childTracker) discover(parentSID string) {
	visited := map[string]bool{parentSID: true}
	var recurse func(sid string)
	recurse = func(sid string) {
		children, err := t.stream.c.ListChildren(t.ctx, sid, t.directory)
		if err != nil {
			return
		}
		for _, ch := range children {
			if ch.ID == "" || visited[ch.ID] {
				continue
			}
			visited[ch.ID] = true
			t.subscribe(ch.ID)
			recurse(ch.ID)
		}
	}
	recurse(parentSID)
}

// subscribe 对一个新子 sid 建立订阅 + 起 forward goroutine。
// GlobalEventStream.Subscribe 对同一 sessionID 会关闭旧 chan 再建新的（订阅语义），
// 故必须靠 subs 集合去重，否则会切断正在转发的 forward。
func (t *childTracker) subscribe(sid string) {
	t.mu.Lock()
	if _, ok := t.subs[sid]; ok {
		t.mu.Unlock()
		return
	}
	t.subs[sid] = struct{}{}
	t.mu.Unlock()

	src := t.stream.Subscribe(sid)
	t.wg.Add(1)
	go t.forwardAsked(src)
}

// forwardAsked 只把 permission.asked / question.asked 转发到 out。
// 转发前经 askedTracker 去重（跨父子+跨 SSE/poll）。子 session 的其他事件类型
// （text/tool-use/step-*/idle 等）一律丢——子 turn 的文本与状态不属于父 turn 输出。
func (t *childTracker) forwardAsked(src <-chan Event) {
	defer t.wg.Done()
	defer recoverPanic("childTracker.forwardAsked")
	for {
		select {
		case <-t.ctx.Done():
			return
		case ev, ok := <-src:
			if !ok {
				return
			}
			he, isAsked := mapAskedEvent(ev)
			if !isAsked {
				continue
			}
			if !t.asked.register(&he) {
				continue
			}
			select {
			case <-t.ctx.Done():
				return
			case t.out <- he:
			}
		}
	}
}

// sids 返回所有已订阅子 sid 快照，供 pollAsked 批量过滤全局 pending。
func (t *childTracker) sids() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]string, 0, len(t.subs))
	for sid := range t.subs {
		out = append(out, sid)
	}
	return out
}

// close 取消所有 forward 并等其退出，最后 unsubscribe 全部子 sid。
// 必须在 pump 关闭 out chan 之前调用：forward goroutine 正在写 out 时若 out 被
// close 会触发 panic，Wait 保证全部 forward 退出后再由调用方 close(out)。
func (t *childTracker) close() {
	t.cancel()
	t.wg.Wait()
	t.mu.Lock()
	subs := t.subs
	t.subs = make(map[string]struct{})
	t.mu.Unlock()
	for sid := range subs {
		t.stream.Unsubscribe(sid)
	}
}
