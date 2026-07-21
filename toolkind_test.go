package opencode

import "testing"

func TestClassifyTool(t *testing.T) {
	cases := map[string]ToolKind{
		"read":         ToolKindFileRead,
		"list":         ToolKindFileRead,
		"write":        ToolKindFileWrite,
		"edit":         ToolKindFileWrite,
		"multiedit":    ToolKindFileWrite,
		"bash":         ToolKindShell,
		"grep":         ToolKindSearch,
		"glob":         ToolKindSearch,
		"codesearch":   ToolKindSearch,
		"websearch":    ToolKindSearch,
		"webfetch":     ToolKindWebFetch,
		"task":         ToolKindSubagent,
		"todowrite":    ToolKindTodo,
		"todoread":     ToolKindTodo,
		"Read":         ToolKindFileRead, // 大小写不敏感
		"mcp_docs_get": ToolKindMCP,
		"unknown_xyz":  ToolKindOther,
		"":             ToolKindOther,
	}
	for name, want := range cases {
		if got := ClassifyTool(name); got != want {
			t.Errorf("ClassifyTool(%q) = %s, want %s", name, got, want)
		}
	}
}
