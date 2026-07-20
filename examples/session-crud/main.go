// Command session-crud 串起 session 管理面：Create → List → Get →
// ListMessages → Delete，演示一次完整生命周期。
//
// 用法：
//
//	go run ./examples/session-crud
//	go run ./examples/session-crud -dir /path/to/repo
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
		token   = flag.String("token", "", "Bearer token")
		dir     = flag.String("dir", ".", "工作区目录")
		timeout = flag.Duration("timeout", 60*time.Second, "整体超时")
	)
	flag.Parse()

	opts := []oc.Option{oc.WithUserAgent("opencode-sdk-lite/examples/session-crud")}
	if *token != "" {
		opts = append(opts, oc.WithToken(*token))
	}
	client, err := oc.New(*baseURL, opts...)
	if err != nil {
		log.Fatalf("New: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	must := func(what string, err error) {
		if err != nil {
			log.Fatalf("%s: %v", what, err)
		}
	}

	loc := &oc.LocationRef{Directory: absDir(*dir)}

	// 1. Create
	ses, err := client.CreateSession(ctx, &oc.CreateSessionReq{Directory: loc.Directory, WorkspaceID: loc.WorkspaceID})
	must("CreateSession", err)
	fmt.Printf("[create] id=%s agent=%s\n", ses.ID, ses.Agent)
	defer func() {
		_ = client.DeleteSession(ctx, ses.ID)
		fmt.Printf("[delete] id=%s removed\n", ses.ID)
	}()

	// 2. List（演示截断，limit=5）
	listed, err := client.ListSessions(ctx, &oc.ListSessionsOpt{
		Directory: loc.Directory,
		Limit:     5,
	})
	must("ListSessions", err)
	fmt.Printf("[list] 返回 %d 条（上限 5）\n", len(listed))

	// 3. Get
	got, err := client.GetSession(ctx, ses.ID)
	must("GetSession", err)
	if got.ID != ses.ID {
		log.Fatalf("GetSession id mismatch: %s != %s", got.ID, ses.ID)
	}
	fmt.Printf("[get] id=%s title=%q cost=%.4f\n", got.ID, got.Title, got.Cost)

	// 4. 列出可用模型与 agent（V1 无 Switch 接口，agent/model 随 Prompt body 指定）
	if m := pickModel(ctx, client, loc); m != nil {
		fmt.Printf("[model] 默认 → %s/%s\n", m.ProviderID, m.ID)
	}
	if a := pickAgent(ctx, client, loc, "plan"); a != "" {
		fmt.Printf("[agent] 可用 → %s\n", a)
	}

	// 5. ListMessages（注意：未发 prompt 时通常为空；prompt 后 eventual consistent）
	msgs, err := client.ListMessages(ctx, ses.ID, &oc.ListMessagesOpt{Limit: 10})
	must("ListMessages", err)
	fmt.Printf("[messages] %d 条（空属正常，prompt 后约 3s 才最终一致）\n", len(msgs))

	// 6. Delete 在 defer 里执行
}

func pickModel(ctx context.Context, client *oc.Client, loc *oc.LocationRef) *oc.ModelRef {
	ms, err := client.ListModels(ctx, loc)
	if err != nil || len(ms) == 0 {
		return nil
	}
	return &oc.ModelRef{ID: ms[0].ID, ProviderID: ms[0].ProviderID}
}

func pickAgent(ctx context.Context, client *oc.Client, loc *oc.LocationRef, want string) string {
	as, err := client.ListAgents(ctx, loc)
	if err != nil {
		return ""
	}
	for _, a := range as {
		if a.Name == want {
			return a.Name
		}
	}
	return ""
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
