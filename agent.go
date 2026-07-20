package opencode

import (
	"context"
	"encoding/json"
)

// ListAgents 列出当前注册的 agent（build/plan/general/explore 等）。
func (c *Client) ListAgents(ctx context.Context, loc *LocationRef) ([]AgentV2Info, error) {
	var wrapped struct {
		Location json.RawMessage `json:"location"`
		Data     []AgentV2Info   `json:"data"`
	}
	if err := c.doJSON(ctx, http_GET, "/api/agent", locationQuery(loc), nil, &wrapped, 0); err != nil {
		return nil, err
	}
	return wrapped.Data, nil
}
