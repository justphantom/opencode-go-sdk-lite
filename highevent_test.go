package opencode

import (
	"encoding/json"
	"testing"
)

// TestJoinToolContent：tool.success 的 content 数组里只有 type=="text" 的
// 条目参与拼接；空/非 text/坏 JSON 静默跳过；多条用 \n 连接。
func TestJoinToolContent(t *testing.T) {
	mk := func(v any) json.RawMessage {
		b, _ := json.Marshal(v)
		return b
	}
	tests := []struct {
		name    string
		content []json.RawMessage
		want    string
	}{
		{"nil", nil, ""},
		{"empty", []json.RawMessage{}, ""},
		{"single text", []json.RawMessage{mk(map[string]any{"type": "text", "text": "hello"})}, "hello"},
		{"multi text joined with newline", []json.RawMessage{
			mk(map[string]any{"type": "text", "text": "a"}),
			mk(map[string]any{"type": "text", "text": "b"}),
		}, "a\nb"},
		{"non-text skipped", []json.RawMessage{
			mk(map[string]any{"type": "image", "data": "..."}),
			mk(map[string]any{"type": "text", "text": "kept"}),
		}, "kept"},
		{"bad json skipped", []json.RawMessage{
			json.RawMessage(`{not-json`),
			mk(map[string]any{"type": "text", "text": "kept"}),
		}, "kept"},
		{"empty text skipped", []json.RawMessage{
			mk(map[string]any{"type": "text", "text": ""}),
			mk(map[string]any{"type": "text", "text": "kept"}),
		}, "kept"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := joinToolContent(tt.content); got != tt.want {
				t.Errorf("joinToolContent = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestFormatToolError：tool.failed 的 error map 优先取 message 字段；
// 无 message 时退到 JSON 序列化；空 map 返空。
func TestFormatToolError(t *testing.T) {
	tests := []struct {
		name string
		in   map[string]any
		want string
	}{
		{"nil", nil, ""},
		{"empty", map[string]any{}, ""},
		{"message preferred", map[string]any{"message": "boom", "code": 1}, "boom"},
		{"fallback to json", map[string]any{"code": 1}, `{"code":1}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := formatToolError(tt.in); got != tt.want {
				t.Errorf("formatToolError = %q, want %q", got, tt.want)
			}
		})
	}
}
