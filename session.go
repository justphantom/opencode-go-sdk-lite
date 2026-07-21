package opencode

import (
	"context"
	"fmt"
	"net/url"
	"strings"
)

// serve 1.18.4 实测：GET /session 有未文档化默认 limit=100，超过即截断，
// 无游标无 total 无法翻页，故 SDK 默认上送 200 放宽截断阈值。
const defaultListSessionsLimit = 200

// ListSessionsOpt 是 GET /session 的查询参数。
type ListSessionsOpt struct {
	Directory string
	Workspace string
	Scope     string
	Search    string
	Limit     int // <=0 时用 defaultListSessionsLimit
}

func (o *ListSessionsOpt) toQuery() url.Values {
	q := url.Values{}
	if o == nil {
		q.Set("limit", fmt.Sprintf("%d", defaultListSessionsLimit))
		return q
	}
	if o.Directory != "" {
		q.Set("directory", o.Directory)
	}
	if o.Workspace != "" {
		q.Set("workspace", o.Workspace)
	}
	if o.Scope != "" {
		q.Set("scope", o.Scope)
	}
	if o.Search != "" {
		q.Set("search", o.Search)
	}
	limit := o.Limit
	if limit <= 0 {
		limit = defaultListSessionsLimit
	}
	q.Set("limit", fmt.Sprintf("%d", limit))
	return q
}

// ListSessions 列出 session。serve 无游标分页且默认 limit=100 会静默截断，
// SDK 默认上送 limit=200；会话数超过 200 需显式传更大 Limit。
func (c *Client) ListSessions(ctx context.Context, opt *ListSessionsOpt) ([]SessionInfo, error) {
	var out []SessionInfo
	if err := c.doJSON(ctx, http_GET, "/session", opt.toQuery(), nil, &out, 0); err != nil {
		return nil, err
	}
	return out, nil
}

// CreateSession 创建会话。req 留空时由服务端生成 id 并用默认 agent/model。
func (c *Client) CreateSession(ctx context.Context, req *CreateSessionReq) (*SessionInfo, error) {
	if req == nil {
		req = &CreateSessionReq{}
	}
	q := url.Values{}
	if req.Directory != "" {
		q.Set("directory", req.Directory)
	}
	var out SessionInfo
	if err := c.doJSON(ctx, http_POST, "/session", q, req, &out, 0); err != nil {
		return nil, err
	}
	return &out, nil
}

// GetSession 返回单个会话详情。
func (c *Client) GetSession(ctx context.Context, sessionID string) (*SessionInfo, error) {
	var out SessionInfo
	if err := c.doJSON(ctx, http_GET, "/session/"+sessionID, nil, nil, &out, 0); err != nil {
		return nil, err
	}
	return &out, nil
}

// Prompt 异步发送一条消息并调度 agent-loop（POST /session/{id}/prompt_async）。
// 服务端返 204 无 body：没有 admitted 确认，messageID/partID 由 SDK 生成
// （调用方显式传入且前缀合法时尊重原值），经 PromptAck 回传，
// 用于关联后续 SSE 事件。agent/model 随本条消息生效（V1 无独立的 Switch 接口）。
func (c *Client) Prompt(ctx context.Context, sessionID string, req *PromptReq) (*PromptAck, error) {
	if req == nil {
		return nil, fmt.Errorf("opencode: prompt request is nil")
	}
	if len(req.Parts) == 0 {
		return nil, fmt.Errorf("opencode: prompt.parts is required")
	}

	ack := &PromptAck{MessageID: req.MessageID, PartIDs: make([]string, len(req.Parts))}
	if ack.MessageID == "" {
		id, err := GenerateMessageID()
		if err != nil {
			return nil, err
		}
		ack.MessageID = id
	} else if !strings.HasPrefix(ack.MessageID, msgPrefix) {
		return nil, fmt.Errorf("opencode: messageID %q must start with %q", ack.MessageID, msgPrefix)
	}

	parts := make([]PromptPart, len(req.Parts))
	for i, p := range req.Parts {
		if p.ID == "" {
			id, err := GeneratePartID()
			if err != nil {
				return nil, err
			}
			p.ID = id
		} else if !strings.HasPrefix(p.ID, prtPrefix) {
			return nil, fmt.Errorf("opencode: part id %q must start with %q", p.ID, prtPrefix)
		}
		parts[i] = p
		ack.PartIDs[i] = p.ID
	}

	// wire body：model 键为 modelID（与 ModelRef.ID 不同名），在此转换
	body := struct {
		MessageID string            `json:"messageID,omitempty"`
		Model     *PromptModelRef   `json:"model,omitempty"`
		Agent     string            `json:"agent,omitempty"`
		NoReply   bool              `json:"noReply,omitempty"`
		System    string            `json:"system,omitempty"`
		Variant   string            `json:"variant,omitempty"`
		Tools     map[string]bool   `json:"tools,omitempty"`
		Parts     []PromptPart      `json:"parts"`
	}{
		MessageID: ack.MessageID,
		Agent:     req.Agent,
		NoReply:   req.NoReply,
		System:    req.System,
		Variant:   req.Variant,
		Tools:     req.Tools,
		Parts:     parts,
	}
	if req.Model != nil {
		body.Model = &PromptModelRef{ProviderID: req.Model.ProviderID, ModelID: req.Model.ID}
		if body.Variant == "" {
			body.Variant = req.Model.Variant
		}
	}

	if err := c.doEmpty(ctx, http_POST, "/session/"+sessionID+"/prompt_async", nil, body, 204); err != nil {
		return nil, err
	}
	return ack, nil
}

// Interrupt 中断当前 agent-loop（POST /session/{id}/abort）。空闲时为 no-op。
func (c *Client) Interrupt(ctx context.Context, sessionID string) error {
	return c.doEmpty(ctx, http_POST, "/session/"+sessionID+"/abort", nil, nil, 200)
}

// UpdateSessionReq 是 PATCH /session/{id} 的请求体；零值字段不上送。
type UpdateSessionReq struct {
	Title    string         `json:"title,omitempty"`
	Metadata map[string]any `json:"metadata,omitempty"`
	Archived int64          `json:"-"` // 毫秒时间戳；>0 时上送 time.archived
}

// UpdateSession 更新会话标题/元数据/归档时间，返回更新后的会话。
func (c *Client) UpdateSession(ctx context.Context, sessionID string, req *UpdateSessionReq) (*SessionInfo, error) {
	if req == nil {
		req = &UpdateSessionReq{}
	}
	body := struct {
		Title    string         `json:"title,omitempty"`
		Metadata map[string]any `json:"metadata,omitempty"`
		Time     *struct {
			Archived int64 `json:"archived"`
		} `json:"time,omitempty"`
	}{Title: req.Title, Metadata: req.Metadata}
	if req.Archived > 0 {
		body.Time = &struct {
			Archived int64 `json:"archived"`
		}{Archived: req.Archived}
	}
	var out SessionInfo
	if err := c.doJSON(ctx, http_PATCH, "/session/"+sessionID, nil, body, &out, 0); err != nil {
		return nil, err
	}
	return &out, nil
}

// DeleteSession 删除会话。
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

// DeleteSessionIfIdle 仅在会话非 busy 时删除。
// busy 时拒绝且不发 DELETE；状态查询失败则透传错误，不降级强删。
// 注意：状态查询与删除之间存在竞态，仅为尽力而为的前置检查。
func (c *Client) DeleteSessionIfIdle(ctx context.Context, sessionID string) error {
	st, err := c.SessionStatuses(ctx)
	if err != nil {
		return err
	}
	if st[sessionID].Type == "busy" {
		return fmt.Errorf("opencode: session %s is busy", sessionID)
	}
	return c.DeleteSession(ctx, sessionID)
}

// SessionStatuses 返回所有会话的运行状态（GET /session/status）。
// 键为 sessionID；空闲会话可能缺省，缺省即 idle。
func (c *Client) SessionStatuses(ctx context.Context) (map[string]SessionStatus, error) {
	var out map[string]SessionStatus
	if err := c.doJSON(ctx, http_GET, "/session/status", nil, nil, &out, 0); err != nil {
		return nil, err
	}
	return out, nil
}

// ListMessagesOpt 是 GET /session/{id}/message 的查询参数。
type ListMessagesOpt struct {
	Directory string
	Workspace string
	Limit     int
	Before    string
}

func (o *ListMessagesOpt) toQuery() url.Values {
	q := url.Values{}
	if o == nil {
		return q
	}
	if o.Directory != "" {
		q.Set("directory", o.Directory)
	}
	if o.Workspace != "" {
		q.Set("workspace", o.Workspace)
	}
	if o.Limit > 0 {
		q.Set("limit", fmt.Sprintf("%d", o.Limit))
	}
	if o.Before != "" {
		q.Set("before", o.Before)
	}
	return q
}

// ListMessages 列出会话历史消息（info + parts）。
func (c *Client) ListMessages(ctx context.Context, sessionID string, opt *ListMessagesOpt) ([]SessionMessage, error) {
	var out []SessionMessage
	if err := c.doJSON(ctx, http_GET, "/session/"+sessionID+"/message", opt.toQuery(), nil, &out, 0); err != nil {
		return nil, err
	}
	return out, nil
}

// GetMessage 返回单条消息（info + parts），用于终止后取服务端落库的最终回复。
func (c *Client) GetMessage(ctx context.Context, sessionID, messageID string) (*SessionMessage, error) {
	var out SessionMessage
	if err := c.doJSON(ctx, http_GET, "/session/"+sessionID+"/message/"+messageID, nil, nil, &out, 0); err != nil {
		return nil, err
	}
	return &out, nil
}

// HTTP 方法常量，避免多处裸字符串。
const (
	http_GET    = "GET"
	http_POST   = "POST"
	http_PATCH  = "PATCH"
	http_DELETE = "DELETE"
)
