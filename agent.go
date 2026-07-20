package opencode

import "context"

// ListAgents 列出当前注册的 agent（build/plan/general/explore 等）。
func (c *Client) ListAgents(ctx context.Context, loc *LocationRef) ([]AgentInfo, error) {
	var out []AgentInfo
	if err := c.doJSON(ctx, http_GET, "/agent", locationQuery(loc), nil, &out, 0); err != nil {
		return nil, err
	}
	return out, nil
}
