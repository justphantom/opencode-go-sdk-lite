// Command observability 演示 v0.2 引入的可观测性与可靠性能力：
//
//   - WithLogger：注入 *slog.Logger，覆盖 connect/dispatch/watchdog 等内部埋点
//   - WithBusinessIdleTimeout：业务事件空闲阈值（默认 5min），超过触发 OnIdle 回调
//   - GlobalEventStream.OnIdle：业务空闲回调，订阅者决策（GET messages / abort / 继续）
//   - RunWithHandle + WaitTerminal：订阅者 ctx 取消后，主动等飞行中的终止事件
//
// 适用场景：长链路订阅者（如 bridge），需要在 SSE 故障时定位 + ctx 取消时
// 不丢失终止事件（如 HighEventResult 携带的 token 统计与最终回复）。
//
// 用法：
//
//	go run ./examples/observability
//	go run ./examples/observability -url http://127.0.0.1:4096 -token XXX -dir /path/to/repo
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"log/slog"
	"os"
	"time"

	oc "github.com/justphantom/opencode-go-sdk-lite"
)

func main() {
	var (
		baseURL     = flag.String("url", "http://127.0.0.1:6096", "opencode serve 地址")
		token       = flag.String("token", "", "Bearer token")
		dir         = flag.String("dir", ".", "工作区目录")
		prompt      = flag.String("prompt", "解释这个项目的目录结构", "提问")
		modelID     = flag.String("model", "", "模型 id（空则用服务端默认）")
		idleAfter   = flag.Duration("idle-after", 5*time.Minute, "业务空闲阈值（超过触发 OnIdle）")
		cancelAfter = flag.Duration("cancel-after", 0, ">0 时模拟订阅者在 N 后取消 ctx（演示 drain）")
		timeout     = flag.Duration("timeout", 5*time.Minute, "整体超时")
	)
	flag.Parse()

	// 1. 构造 Client，注入 logger（覆盖所有内部埋点）+ 业务空闲阈值。
	// 生产环境 logger 可换成 slog.New(slog.NewJSONHandler(os.Stderr, nil))。
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
	opts := []oc.Option{
		oc.WithUserAgent("opencode-sdk-lite/examples/observability"),
		oc.WithLogger(logger),
		oc.WithBusinessIdleTimeout(*idleAfter),
	}
	if *token != "" {
		opts = append(opts, oc.WithToken(*token))
	}
	client, err := oc.New(*baseURL, opts...)
	if err != nil {
		log.Fatalf("New: %v", err)
	}

	runCtx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	if err := client.Health(runCtx); err != nil {
		log.Fatalf("Health: %v", err)
	}
	fmt.Println("[health] ok")

	loc := &oc.LocationRef{Directory: absDir(*dir)}
	stream, err := client.NewGlobalEventStream(runCtx, loc)
	if err != nil {
		log.Fatalf("NewGlobalEventStream: %v", err)
	}
	defer func() { _ = stream.Close() }()

	// 2. 注册业务空闲回调（OnIdle 在锁外执行，pendingAsked 状态跳过）。
	// 这里只在 stderr 打印提醒；生产场景可调 GetMessage 拉最终回复或主动 abort。
	stream.OnIdle = func(sessionID string, idleSince time.Time) {
		fmt.Fprintf(os.Stderr, "[onIdle] session=%s idle_since=%v (决策: GET messages / abort / 继续)\n",
			sessionID, idleSince)
	}

	// 3. RunWithHandle 返回 *RunHandle，暴露 WaitTerminal（旧 Run 签名仍可用）。
	var model *oc.ModelRef
	if *modelID != "" {
		model = &oc.ModelRef{ID: *modelID}
	}
	handle, err := client.RunWithHandle(runCtx, stream, oc.RunOptions{
		Prompt:   *prompt,
		Model:    model,
		Location: loc,
	})
	if err != nil {
		log.Fatalf("RunWithHandle: %v", err)
	}

	// 4. 演示 drain：cancelAfter > 0 时模拟订阅者主动取消 ctx。
	// 取消后调 WaitTerminal(graceCtx) 等 drainGrace（默认 500ms）窗口内的终止事件。
	if *cancelAfter > 0 {
		go func() {
			time.Sleep(*cancelAfter)
			fmt.Println("[demo] 触发 ctx 取消，等 drainGrace 内的终止事件...")
			cancel()
		}()
	}

	// 5. 消费事件。ctx 取消时改用 handle.WaitTerminal 接住飞行中的终止事件。
	for {
		select {
		case ev, ok := <-handle.Events():
			if !ok {
				return
			}
			switch ev.Kind() {
			case oc.HighEventText:
				fmt.Print(ev.Text())
			case oc.HighEventResult:
				fmt.Printf("\n[done] in=%d out=%d reasoning=%d cost=%.4f model=%s\n",
					ev.InputTokens(), ev.OutputTokens(), ev.ReasoningTokens(),
					ev.Cost(), ev.ModelID())
				return
			case oc.HighEventError:
				fmt.Printf("\n[error] %s\n", ev.Result())
				return
			}
		case <-runCtx.Done():
			// ctx 取消后，给 pump drainGrace（默认 500ms）+ 多等 2s 兜底飞行中事件。
			graceCtx, graceCancel := context.WithTimeout(context.Background(), 2*time.Second)
			ev, ok := handle.WaitTerminal(graceCtx)
			graceCancel()
			if ok {
				fmt.Printf("\n[drained] 接住终止事件: kind=%v result=%q\n", ev.Kind(), ev.Result())
			} else {
				fmt.Println("\n[drained] drainGrace 内无终止事件")
			}
			return
		}
	}
}

func absDir(d string) string {
	if d == "" || d == "." {
		if wd, err := os.Getwd(); err == nil {
			return wd
		}
	}
	return d
}
