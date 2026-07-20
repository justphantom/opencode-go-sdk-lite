package opencode

import (
	"errors"
	"io/fs"
	"os"
	"strings"
	"testing"
)

// TestSSEGoldenReplay 回放 testdata/sse_frames.txt（由 integration 测试的
// GoldenCapture 用例从真实 /event 抓取），锚定线上帧格式：
// 若服务端格式漂移（如包装 payload），此测试会在普通 go test 中直接失败。
func TestSSEGoldenReplay(t *testing.T) {
	raw, err := os.ReadFile("testdata/sse_frames.txt")
	if errors.Is(err, fs.ErrNotExist) {
		t.Skip("golden 未生成：先跑 go test -tags=integration -run TestIntegration/GoldenCapture")
	}
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}

	sc := newSSEScanner(strings.NewReader(string(raw)))
	frames, typed := 0, 0
	for {
		id, evType, data, err := sc.next()
		if err != nil {
			break
		}
		frames++
		ev, derr := decodeEvent(id, evType, data)
		if derr != nil {
			t.Errorf("golden 帧 %d 解析失败（格式漂移？）: %v", frames, derr)
			continue
		}
		if ev.Type != "" {
			typed++
		}
	}
	if frames == 0 {
		t.Fatalf("golden 文件无可解析帧")
	}
	// 线上帧必须绝大多数带 type；全空意味着格式已包装化
	if typed == 0 {
		t.Errorf("golden 共 %d 帧，无一解析出 type（疑似 payload 包装漂移）", frames)
	}
	t.Logf("replayed %d frames, %d typed", frames, typed)
}
