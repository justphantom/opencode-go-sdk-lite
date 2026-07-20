// Command concurrent 演示用一条 GlobalEventStream 长连服务多个 session，
// 适用 HTTP 网关 / 机器人适配层等"一进多出"场景。
//
// 用法：
//
//	go run ./examples/concurrent
//	go run ./examples/concurrent -n 3 -prompt "用一句话介绍 Go"
//
// 设计：N 个 worker 各自 CreateSession + Prompt，共用同一个 stream；
// 按 sessionID 路由的语义由 GlobalEventStream 内部完成，本例只消费各自订阅 chan。
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"sync"
	"time"

	oc "github.com/justphantom/opencode-go-sdk-lite"
)

func main() {
	var (
		baseURL = flag.String("url", "http://127.0.0.1:6096", "opencode serve 地址")
		token   = flag.String("token", "", "Bearer token")
		dir     = flag.String("dir", ".", "工作区目录")
		prompt  = flag.String("prompt", "用一句话介绍 Go 语言", "每个 session 的提问")
		n       = flag.Int("n", 3, "并发 session 数")
		timeout = flag.Duration("timeout", 180*time.Second, "整体超时")
	)
	flag.Parse()
	if *n < 1 {
		log.Fatalf("-n 必须 >= 1")
	}

	opts := []oc.Option{oc.WithUserAgent("opencode-sdk-lite/examples/concurrent")}
	if *token != "" {
		opts = append(opts, oc.WithToken(*token))
	}
	client, err := oc.New(*baseURL, opts...)
	if err != nil {
		log.Fatalf("New: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	if err := client.Health(ctx); err != nil {
		log.Fatalf("Health: %v", err)
	}

	// 关键：所有 session 共用同一个全局流。只需建一次。
	loc := &oc.LocationRef{Directory: absDir(*dir)}
	stream, err := client.NewGlobalEventStream(ctx)
	if err != nil {
		log.Fatalf("NewGlobalEventStream: %v", err)
	}
	defer func() { _ = stream.Close() }()

	var wg sync.WaitGroup
	for i := 1; i <= *n; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			if err := runOne(ctx, client, stream, loc, *prompt, idx); err != nil {
				log.Printf("[worker %d] %v", idx, err)
			}
		}(i)
	}
	wg.Wait()
	fmt.Println("[all] done")
}

// runOne 走低层路径：CreateSession → Subscribe（拿到底层 Event chan）→ Prompt → 消费。
// 比 Run 更适合"多路复用"场景：Subscribe 返回的是原始 Event，多 worker 各自处理无干扰。
func runOne(ctx context.Context, client *oc.Client, stream *oc.GlobalEventStream, loc *oc.LocationRef, prompt string, idx int) error {
	ses, err := client.CreateSession(ctx, &oc.CreateSessionReq{Location: loc})
	if err != nil {
		return fmt.Errorf("CreateSession: %w", err)
	}
	defer func() { _ = client.DeleteSession(ctx, ses.ID) }()

	// Subscribe 必须在 Prompt 前调用。
	ch := stream.Subscribe(ses.ID)
	defer stream.Unsubscribe(ses.ID)

	admitted, err := client.Prompt(ctx, ses.ID, &oc.PromptReq{
		Prompt: oc.PromptInput{Text: fmt.Sprintf("[%d] %s", idx, prompt)},
	})
	if err != nil {
		return fmt.Errorf("prompt: %w", err)
	}
	fmt.Printf("[worker %d] session=%s msg=%s\n", idx, ses.ID, admitted.ID)

	var sb strings.Builder
	for ev := range ch {
		switch ev.Type {
		case oc.EventSessionNextTextDelta:
			var d oc.TextDeltaData
			if json.Unmarshal(ev.Data, &d) == nil {
				sb.WriteString(d.Delta)
			}
		case oc.EventSessionNextStepEnded:
			var d oc.StepEndedData
			_ = json.Unmarshal(ev.Data, &d)
			if d.Finish == "stop" || d.Finish == "" {
				fmt.Printf("[worker %d] 回复: %s\n", idx, strings.TrimSpace(sb.String()))
				return nil
			}
		case oc.EventSessionError, oc.EventSessionNextStepFailed:
			return fmt.Errorf("session error: %s", string(ev.Data))
		}
	}
	return errors.New("stream closed before finish")
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
