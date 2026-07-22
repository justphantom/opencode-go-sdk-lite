package opencode

import (
	"encoding/json"
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

// TestSSEGoldenReasoningReplay 回放 testdata/sse_frames_reasoning.txt（由 integration
// 的 GoldenReasoningCapture 从真实 reasoning 模型抓取），锚定「思考内容如何 delivery」
// 这一此前从未被任何测试证实的假设：
//
//  1. reasoning part 经 message.part.updated{type:"reasoning", text:""} 先建块（空文本）；
//  2. 随后 message.part.delta（field 恒为 "text"，text/reasoning 共用）流式喂入同一 partID；
//  3. 该 partID 的 delta 经 mapToHighEvent 映射为 HighEventThinking。
//
// 若服务端改为一次性 part.updated{type:"reasoning", text:"...完整"} 而不再流 delta，
// thinkingEvents 会归零、此测试失败——这正是要防的「思考静默丢失」回归。
// 同时断言 part.updated 早于 delta（boundary-1 不变量的真实流量锚点）。
func TestSSEGoldenReasoningReplay(t *testing.T) {
	raw, err := os.ReadFile("testdata/sse_frames_reasoning.txt")
	if errors.Is(err, fs.ErrNotExist) {
		t.Skip("reasoning golden 未生成：先跑 go test -tags=integration -run TestIntegration/GoldenReasoningCapture")
	}
	if err != nil {
		t.Fatalf("read reasoning golden: %v", err)
	}

	sc := newSSEScanner(strings.NewReader(string(raw)))
	var assistantID string
	parts := partTracker{}
	frames, thinkingEvents := 0, 0
	reasoningPartAt, reasoningDeltaAt := -1, -1
	for {
		id, evType, data, err := sc.next()
		if err != nil {
			break
		}
		frames++
		ev, derr := decodeEvent(id, evType, data)
		if derr != nil {
			t.Fatalf("reasoning golden 帧 %d 解析失败（格式漂移？）: %v; data=%s", frames, derr, data)
		}
		// 记录首个 reasoning part.updated 帧号（boundary-1：建块先于 delta）
		if reasoningPartAt < 0 && ev.Type == EventMessagePartUpdated {
			var d PartUpdatedData
			if json.Unmarshal(ev.Properties, &d) == nil && d.Part.Type == PartTypeReasoning {
				reasoningPartAt = frames
			}
		}
		he, emit, _ := mapToHighEvent(ev, &assistantID, parts)
		if emit && he.Kind() == HighEventThinking {
			if reasoningDeltaAt < 0 {
				reasoningDeltaAt = frames
			}
			thinkingEvents++
		}
	}
	if frames == 0 {
		t.Fatalf("reasoning golden 无可解析帧")
	}
	if thinkingEvents == 0 {
		t.Fatalf("reasoning golden 未映射出任何 HighEventThinking：" +
			"delivery 假设已破裂（reasoning 不再走 part.delta？）")
	}
	// part.updated(type=reasoning) 必须早于 reasoning delta —— 否则 mapToHighEvent 会
	// 因 partID 未登记而把思考误判为文本（boundary-1）。
	if reasoningPartAt < 0 || reasoningDeltaAt < 0 || reasoningPartAt > reasoningDeltaAt {
		t.Errorf("reasoning part.updated 应先于 delta：part@%d delta@%d", reasoningPartAt, reasoningDeltaAt)
	}
	t.Logf("reasoning replay: %d frames, %d HighEventThinking (part@%d → delta@%d)",
		frames, thinkingEvents, reasoningPartAt, reasoningDeltaAt)
}
