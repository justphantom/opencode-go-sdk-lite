package opencode

import "strings"

// ToolKind 是工具调用的语义分类，供 tool_use/tool_result 事件消费方
// 区分读写文件、shell、搜索、网页抓取、MCP、subagent、todo 等类别。
type ToolKind string

const (
	ToolKindFileRead  ToolKind = "file_read"  // 读文件
	ToolKindFileWrite ToolKind = "file_write" // 写/改文件
	ToolKindShell     ToolKind = "shell"      // 执行 shell
	ToolKindSearch    ToolKind = "search"     // 搜索（代码/文件/网页）
	ToolKindWebFetch  ToolKind = "webfetch"   // 抓取网页
	ToolKindMCP       ToolKind = "mcp"        // 调用 MCP 工具
	ToolKindSubagent  ToolKind = "subagent"   // 发起 subagent
	ToolKindTodo      ToolKind = "todo"       // todo 读写
	ToolKindOther     ToolKind = "other"
)

var toolKindTable = map[string]ToolKind{
	"read":       ToolKindFileRead,
	"list":       ToolKindFileRead,
	"write":      ToolKindFileWrite,
	"edit":       ToolKindFileWrite,
	"multiedit":  ToolKindFileWrite,
	"patch":      ToolKindFileWrite,
	"apply":      ToolKindFileWrite,
	"bash":       ToolKindShell,
	"shell":      ToolKindShell,
	"grep":       ToolKindSearch,
	"glob":       ToolKindSearch,
	"search":     ToolKindSearch,
	"codesearch": ToolKindSearch,
	"websearch":  ToolKindSearch,
	"webfetch":   ToolKindWebFetch,
	"task":       ToolKindSubagent,
	"subagent":   ToolKindSubagent,
	"todowrite":  ToolKindTodo,
	"todoread":   ToolKindTodo,
}

// ClassifyTool 把工具名归类为 ToolKind。未知名称再按 MCP 命名试探
// （opencode 把 MCP 工具注册为 server_tool 形式，无统一前缀，属尽力而为），
// 都不命中返回 ToolKindOther。
func ClassifyTool(name string) ToolKind {
	n := strings.ToLower(name)
	if k, ok := toolKindTable[n]; ok {
		return k
	}
	if strings.HasPrefix(n, "mcp_") || strings.Contains(n, "_mcp_") {
		return ToolKindMCP
	}
	return ToolKindOther
}
