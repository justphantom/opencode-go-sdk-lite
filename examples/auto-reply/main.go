// Command auto-reply 演示在事件流里自动处理 PermissionAsked / QuestionAsked。
// 适用场景：无人值守的 agent，需要把工具权限请求与多选问题转成自动应答。
//
// 用法：
//
//	go run ./examples/auto-reply
//	go run ./examples/auto-reply -prompt "在当前目录创建一个 hello.txt 写入 hi"
//
// 策略（演示用，集成方按业务替换）：
//   - 权限：默认 always（永久放行）；可用 -perm=once/reject 切换
//   - 问题：每个问题选第 1 个 option；若问题允许自定义（-custom），用 -answer 文本应答
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	oc "github.com/justphantom/opencode-go-sdk-lite"
)

func main() {
	var (
		baseURL = flag.String("url", "http://127.0.0.1:6096", "opencode serve 地址")
		token   = flag.String("token", "", "Bearer token")
		dir     = flag.String("dir", ".", "工作区目录")
		prompt  = flag.String("prompt", "在当前目录建一个 hello.txt 内容写 hi", "提问")
		perm    = flag.String("perm", oc.PermissionReplyAlways, "默认权限应答: once|always|reject")
		custom  = flag.String("custom", "", "问题允许自定义时填入的文本")
		timeout = flag.Duration("timeout", 120*time.Second, "整体超时")
	)
	flag.Parse()

	switch *perm {
	case oc.PermissionReplyOnce, oc.PermissionReplyAlways, oc.PermissionReplyReject:
	default:
		log.Fatalf("-perm 必须是 once/always/reject，当前 %q", *perm)
	}

	opts := []oc.Option{oc.WithUserAgent("opencode-sdk-lite/examples/auto-reply")}
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

	loc := &oc.LocationRef{Directory: absDir(*dir)}
	stream, err := client.NewGlobalEventStream(ctx)
	if err != nil {
		log.Fatalf("NewGlobalEventStream: %v", err)
	}
	defer func() { _ = stream.Close() }()

	events, err := client.Run(ctx, stream, oc.RunOptions{
		Prompt:   *prompt,
		Location: loc,
	})
	if err != nil {
		log.Fatalf("Run: %v", err)
	}

	// Run 返回的是 HighEvent（已归纳）；权限/问题请求的原始事件需要拿到底层 Event。
	// 集成方若要拦截 permission/question，应该直接用 SessionEvents 或 GlobalEventStream
	// 的原始 Event chan——本 demo 切换到原始流。
	//
	// 重新订阅一次原始流（Run 内部已 Subscribe，这里再 Subscribe 同一 session
	// 会顶掉 Run 的订阅，因此本 demo 改用"低层直订阅"模式）。

	// 简化：再起一次 session，用低层 SessionEvents 直接处理。
	ses, err := client.CreateSession(ctx, &oc.CreateSessionReq{Directory: loc.Directory, WorkspaceID: loc.WorkspaceID})
	if err != nil {
		log.Fatalf("CreateSession: %v", err)
	}
	defer func() { _ = client.DeleteSession(ctx, ses.ID) }()
	fmt.Printf("[session] %s\n", ses.ID)

	// 丢弃上面 Run 的事件，改用低层
	go func() {
		for range events {
		}
	}()

	rawEvents, errc := client.SessionEvents(ctx, ses.ID, nil)

	// Prompt 与消费事件并发
	go func() {
		_, perr := client.Prompt(ctx, ses.ID, &oc.PromptReq{
			Parts: []oc.PromptPart{{Type: "text", Text: *prompt}},
		})
		if perr != nil {
			log.Printf("Prompt: %v", perr)
		}
	}()

	for ev := range rawEvents {
		switch ev.Type {
		case oc.EventSessionNextTextDelta:
			var d oc.TextDeltaData
			_ = json.Unmarshal(ev.Data, &d)
			fmt.Print(d.Delta)

		case oc.EventPermissionAsked:
			var d oc.PermissionAskedData
			_ = json.Unmarshal(ev.Data, &d)
			fmt.Printf("\n[perm] permission=%s patterns=%v → %s\n", d.Permission, d.Patterns, *perm)
			if err := client.ReplyPermission(ctx, d.ID, *perm, ""); err != nil {
				log.Printf("ReplyPermission: %v", err)
			}

		case oc.EventQuestionAsked:
			var d oc.QuestionAskedData
			_ = json.Unmarshal(ev.Data, &d)
			ans := buildAnswers(d.Questions, *custom)
			fmt.Printf("\n[question] %d 个问题 → %v\n", len(d.Questions), ans)
			if err := client.ReplyQuestion(ctx, d.ID, &oc.QuestionReply{Answers: ans}); err != nil {
				log.Printf("ReplyQuestion: %v", err)
			}

		case oc.EventSessionNextStepEnded:
			var d oc.StepEndedData
			_ = json.Unmarshal(ev.Data, &d)
			if d.Finish == "stop" || d.Finish == "" {
				fmt.Printf("\n[done] finish=%s in=%d out=%d cost=%.4f\n",
					d.Finish, int(d.Tokens.Input), int(d.Tokens.Output), d.Cost)
				return
			}

		case oc.EventSessionError:
			fmt.Printf("\n[error] %s\n", ev.Data)
			return
		}
	}
	if err := <-errc; err != nil {
		log.Printf("stream: %v", err)
		os.Exit(1)
	}
}

// buildAnswers 给每个问题构造一个 label 列表：允许自定义时用 custom 文本，
// 否则选第 1 个 option（无 option 时空切片）。
func buildAnswers(qs []oc.QuestionInfo, custom string) [][]string {
	out := make([][]string, len(qs))
	for i, q := range qs {
		switch {
		case q.Custom && custom != "":
			out[i] = []string{custom}
		case len(q.Options) > 0:
			out[i] = []string{q.Options[0].Label}
		default:
			out[i] = []string{}
		}
	}
	return out
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

var _ = strings.TrimSpace // 保留 strings 占位（演示里可能按需扩展）
