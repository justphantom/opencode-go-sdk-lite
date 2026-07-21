package opencode

import "context"

// SkillInfo 对应 GET /skill 响应元素；Content 为 skill 全文（含 frontmatter）。
type SkillInfo struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Location    string `json:"location"`
	Content     string `json:"content"`
}

// ListSkills 列出可用 skill。
func (c *Client) ListSkills(ctx context.Context, loc *LocationRef) ([]SkillInfo, error) {
	var out []SkillInfo
	if err := c.doJSON(ctx, http_GET, "/skill", locationQuery(loc), nil, &out, 0); err != nil {
		return nil, err
	}
	return out, nil
}
