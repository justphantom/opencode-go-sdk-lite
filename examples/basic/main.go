// Command basic 演示最小集成路径：Health → ListModels → CreateSession →
// Run → 收 HighEvent → 清理。这是集成方最常用的入口组合。
//
// 用法（默认指向本机 6096 无口令 serve）：
//
//	go run ./examples/basic
//	go run ./examples/basic -url http://127.0.0.1:4096 -token XXX -dir /path/to/repo -prompt "解释这个项目"
//
// 预期输出：依次打印模型数、sessionID、文本增量与最终 token 用量。
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	oc "github.com/justphantom/opencode-go-sdk-lite"
)

func main() {
	var (
		baseURL = flag.String("url", "http://127.0.0.1:6096", "opencode serve 地址")
		token   = flag.String("token", "", "Bearer token（本地部署可空）")
		dir     = flag.String("dir", ".", "工作区目录（LocationRef.Directory）")
		prompt  = flag.String("prompt", "用一句话介绍你自己", "提问内容")
		modelID = flag.String("model", "", "模型 id（空则用服务端默认）")
		timeout = flag.Duration("timeout", 90*time.Second, "整体超时")
	)
	flag.Parse()

	// 1. 构造 Client。WithHTTPClient 可注入，这里用默认。
	opts := []oc.Option{oc.WithUserAgent("opencode-sdk-lite/examples/basic")}
	if *token != "" {
		opts = append(opts, oc.WithToken(*token))
	}
	client, err := oc.New(*baseURL, opts...)
	if err != nil {
		log.Fatalf("New: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	// 2. 探活。失败立即退出，不浪费后续请求。
	if err := client.Health(ctx); err != nil {
		log.Fatalf("Health: %v", err)
	}
	fmt.Println("[health] ok")

	// 3. 列模型，顺便挑一个（演示用，集成方可缓存这份结果）。
	loc := &oc.LocationRef{Directory: absDir(*dir)}
	models, err := client.ListModels(ctx, loc)
	if err != nil {
		log.Fatalf("ListModels: %v", err)
	}
	fmt.Printf("[models] %d 个\n", len(models))
	if *modelID == "" && len(models) > 0 {
		*modelID = models[0].ID
		fmt.Printf("[models] 默认选第 1 个: %s\n", *modelID)
	}

	// 4. 必须在 Prompt 前建全局流，避免丢首帧。
	stream, err := client.NewGlobalEventStream(ctx)
	if err != nil {
		log.Fatalf("NewGlobalEventStream: %v", err)
	}
	defer func() { _ = stream.Close() }()

	// 5. 一站式 Run：内部 CreateSession + Prompt + Subscribe + 合成终止事件。
	var model *oc.ModelRef
	if *modelID != "" {
		model = &oc.ModelRef{ID: *modelID, ProviderID: providerOf(models, *modelID)}
	}
	events, err := client.Run(ctx, stream, oc.RunOptions{
		Prompt:   *prompt,
		Model:    model,
		Location: loc,
	})
	if err != nil {
		log.Fatalf("Run: %v", err)
	}

	// 6. 消费 HighEvent 直到 chan close（Run 保证 close 前必发 result/error）。
	var sessionID string
	for ev := range events {
		switch ev.Kind() {
		case oc.HighEventPrompt:
			sessionID = ev.SessionID()
			fmt.Printf("[prompt] session=%s userMsg=%s\n", ev.SessionID(), ev.MessageID())
		case oc.HighEventText:
			fmt.Print(ev.Text())
		case oc.HighEventThinking:
			// 思考增量默认不打，避免刷屏；按需处理
		case oc.HighEventToolUse:
			fmt.Printf("\n[tool] %s\n", ev.ToolName())
		case oc.HighEventResult:
			fmt.Printf("\n[result] finish=%s in=%d out=%d cost=%.4f\n",
				ev.Result(), ev.InputTokens(), ev.OutputTokens(), ev.Cost())
		case oc.HighEventError:
			fmt.Printf("\n[error] %s\n", ev.Result())
			os.Exit(1)
		}
	}

	// 7. 清理（可选）。集成方若要复用 session 做 multi-turn，这里别删。
	if sessionID != "" {
		_ = client.DeleteSession(ctx, sessionID)
		fmt.Printf("[cleanup] session %s 已删除\n", sessionID)
	}
}

func absDir(d string) string {
	if d == "" || d == "." {
		wd, err := os.Getwd()
		if err == nil {
			return wd
		}
	}
	return d
}

func providerOf(models []oc.ModelInfo, id string) string {
	for _, m := range models {
		if m.ID == id {
			return m.ProviderID
		}
	}
	return ""
}
