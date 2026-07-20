package opencode

import (
	"context"
	"fmt"
	"net/url"
)

// ListSessionsOpt 是 GET /api/session 的查询参数。
type ListSessionsOpt struct {
	Workspace string
	Limit     int
	Order     string // asc | desc
	Search    string
	Directory string
	Project   string
	Subpath   string
	Cursor    string
}

func (o *ListSessionsOpt) toQuery() url.Values {
	q := url.Values{}
	if o == nil {
		return q
	}
	if o.Workspace != "" {
		q.Set("workspace", o.Workspace)
	}
	if o.Limit > 0 {
		q.Set("limit", fmt.Sprintf("%d", o.Limit))
	}
	if o.Order != "" {
		q.Set("order", o.Order)
	}
	if o.Search != "" {
		q.Set("search", o.Search)
	}
	if o.Directory != "" {
		q.Set("directory", o.Directory)
	}
	if o.Project != "" {
		q.Set("project", o.Project)
	}
	if o.Subpath != "" {
		q.Set("subpath", o.Subpath)
	}
	if o.Cursor != "" {
		q.Set("cursor", o.Cursor)
	}
	return q
}

// ListSessions 分页列出 session。
func (c *Client) ListSessions(ctx context.Context, opt *ListSessionsOpt) (*SessionsResponse, error) {
	var wrapped struct {
		Data   []SessionV2Info `json:"data"`
		Cursor *Cursor         `json:"cursor"`
	}
	if err := c.doJSON(ctx, http_GET, "/api/session", opt.toQuery(), nil, &wrapped, 0); err != nil {
		return nil, err
	}
	return &SessionsResponse{Data: wrapped.Data, Cursor: wrapped.Cursor}, nil
}

// CreateSession 创建会话。req 留空时由服务端生成 id 并用默认 agent/model。
func (c *Client) CreateSession(ctx context.Context, req *CreateSessionReq) (*SessionV2Info, error) {
	if req == nil {
		req = &CreateSessionReq{}
	}
	var wrapped struct {
		Data SessionV2Info `json:"data"`
	}
	if err := c.doJSON(ctx, http_POST, "/api/session", nil, req, &wrapped, 0); err != nil {
		return nil, err
	}
	return &wrapped.Data, nil
}

// GetSession 返回单个会话详情。
func (c *Client) GetSession(ctx context.Context, sessionID string) (*SessionV2Info, error) {
	var wrapped struct {
		Data SessionV2Info `json:"data"`
	}
	if err := c.doJSON(ctx, http_GET, "/api/session/"+sessionID, nil, nil, &wrapped, 0); err != nil {
		return nil, err
	}
	return &wrapped.Data, nil
}

// Prompt 发送一条消息并调度 agent-loop。
func (c *Client) Prompt(ctx context.Context, sessionID string, req *PromptReq) (*SessionInputAdmitted, error) {
	if req == nil {
		return nil, fmt.Errorf("opencode: prompt request is nil")
	}
	if req.Prompt.Text == "" {
		return nil, fmt.Errorf("opencode: prompt.text is required")
	}
	var wrapped struct {
		Data SessionInputAdmitted `json:"data"`
	}
	if err := c.doJSON(ctx, http_POST, "/api/session/"+sessionID+"/prompt", nil, req, &wrapped, 0); err != nil {
		return nil, err
	}
	return &wrapped.Data, nil
}

// Interrupt 中断当前 agent-loop。空闲时为 no-op。
func (c *Client) Interrupt(ctx context.Context, sessionID string) error {
	return c.doEmpty(ctx, http_POST, "/api/session/"+sessionID+"/interrupt", nil, nil, 204)
}

// DeleteSession 删除会话。v2 spec 未提供删除端点，实际由 v1 端点 DELETE /session/{id} 承担
// （实测：返 200 + body true，删后 GET /api/session/{id} 返 404）。这是 v2 与 v1 共存的官方行为。
func (c *Client) DeleteSession(ctx context.Context, sessionID string) error {
	var ok bool
	if err := c.doJSON(ctx, http_DELETE, "/session/"+sessionID, nil, nil, &ok, 0); err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("opencode: delete session %s returned false", sessionID)
	}
	return nil
}

// SwitchAgent 切换后续 provider turn 的 agent。
func (c *Client) SwitchAgent(ctx context.Context, sessionID, agent string) error {
	body := map[string]string{"agent": agent}
	return c.doEmpty(ctx, http_POST, "/api/session/"+sessionID+"/agent", nil, body, 204)
}

// SwitchModel 切换后续 provider turn 的 model。
func (c *Client) SwitchModel(ctx context.Context, sessionID string, m ModelRef) error {
	return c.doEmpty(ctx, http_POST, "/api/session/"+sessionID+"/model", nil, m, 204)
}

// ListMessagesOpt 是 GET /api/session/{id}/message 的查询参数。
type ListMessagesOpt struct {
	Limit  int
	Order  string
	Cursor string
}

func (o *ListMessagesOpt) toQuery() url.Values {
	q := url.Values{}
	if o == nil {
		return q
	}
	if o.Limit > 0 {
		q.Set("limit", fmt.Sprintf("%d", o.Limit))
	}
	if o.Order != "" {
		q.Set("order", o.Order)
	}
	if o.Cursor != "" {
		q.Set("cursor", o.Cursor)
	}
	return q
}

// ListMessages 分页列出会话历史消息。
func (c *Client) ListMessages(ctx context.Context, sessionID string, opt *ListMessagesOpt) (*SessionMessagesResponse, error) {
	var wrapped struct {
		Data   []SessionMessage `json:"data"`
		Cursor *Cursor          `json:"cursor"`
	}
	if err := c.doJSON(ctx, http_GET, "/api/session/"+sessionID+"/message", opt.toQuery(), nil, &wrapped, 0); err != nil {
		return nil, err
	}
	return &SessionMessagesResponse{Data: wrapped.Data, Cursor: wrapped.Cursor}, nil
}

// HTTP 方法常量，避免多处裸字符串。
const (
	http_GET    = "GET"
	http_POST   = "POST"
	http_DELETE = "DELETE"
)
