package opencode

import "context"

// CommandInfo 对应 GET /command 响应元素。
// Source 取值：command（自定义命令）/ mcp / skill。
type CommandInfo struct {
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	Agent       string   `json:"agent,omitempty"`
	Model       string   `json:"model,omitempty"`
	Source      string   `json:"source,omitempty"`
	Template    string   `json:"template"`
	Subtask     bool     `json:"subtask,omitempty"`
	Hints       []string `json:"hints"`
}

// ListCommands 列出可用命令。
func (c *Client) ListCommands(ctx context.Context, loc *LocationRef) ([]CommandInfo, error) {
	var out []CommandInfo
	if err := c.doJSON(ctx, http_GET, "/command", locationQuery(loc), nil, &out, 0); err != nil {
		return nil, err
	}
	return out, nil
}
