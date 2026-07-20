package opencode

import (
	"context"
	"fmt"
)

// ListPermissions 列出会话内挂起的权限请求。
func (c *Client) ListPermissions(ctx context.Context, sessionID string) ([]PermissionV2Request, error) {
	var wrapped struct {
		Data []PermissionV2Request `json:"data"`
	}
	if err := c.doJSON(ctx, http_GET, "/api/session/"+sessionID+"/permission", nil, nil, &wrapped, 0); err != nil {
		return nil, err
	}
	return wrapped.Data, nil
}

// CreatePermission 评估并（如需）创建权限请求。
func (c *Client) CreatePermission(ctx context.Context, sessionID string, req *CreatePermissionReq) (*PermissionV2Created, error) {
	if req == nil {
		return nil, fmt.Errorf("opencode: permission request is nil")
	}
	if req.Action == "" {
		return nil, fmt.Errorf("opencode: permission.action is required")
	}
	if len(req.Resources) == 0 {
		return nil, fmt.Errorf("opencode: permission.resources is required")
	}
	var wrapped struct {
		Data PermissionV2Created `json:"data"`
	}
	if err := c.doJSON(ctx, http_POST, "/api/session/"+sessionID+"/permission", nil, req, &wrapped, 0); err != nil {
		return nil, err
	}
	return &wrapped.Data, nil
}

// ReplyPermission 回复一条挂起的权限请求。
// reply 取值 once / always / reject；message 可选，附在回复上。
func (c *Client) ReplyPermission(ctx context.Context, sessionID, requestID, reply, message string) error {
	switch reply {
	case PermissionReplyOnce, PermissionReplyAlways, PermissionReplyReject:
	default:
		return fmt.Errorf("opencode: invalid permission reply %q", reply)
	}
	body := map[string]any{"reply": reply}
	if message != "" {
		body["message"] = message
	}
	return c.doEmpty(ctx, http_POST, "/api/session/"+sessionID+"/permission/"+requestID+"/reply", nil, body, 204)
}
