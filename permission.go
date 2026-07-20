package opencode

import (
	"context"
	"fmt"
)

// ListPermissions 列出会话内挂起的权限请求。
// V1 的 GET /permission 是全局 pending 列表，此处按 sessionID 过滤。
func (c *Client) ListPermissions(ctx context.Context, sessionID string) ([]PermissionRequest, error) {
	var all []PermissionRequest
	if err := c.doJSON(ctx, http_GET, "/permission", nil, nil, &all, 0); err != nil {
		return nil, err
	}
	out := make([]PermissionRequest, 0, len(all))
	for _, p := range all {
		if p.SessionID == sessionID {
			out = append(out, p)
		}
	}
	return out, nil
}

// ReplyPermission 回复一条挂起的权限请求。
// reply 取值 once / always / reject；message 可选，附在回复上。
func (c *Client) ReplyPermission(ctx context.Context, requestID, reply, message string) error {
	switch reply {
	case PermissionReplyOnce, PermissionReplyAlways, PermissionReplyReject:
	default:
		return fmt.Errorf("opencode: invalid permission reply %q", reply)
	}
	body := map[string]any{"reply": reply}
	if message != "" {
		body["message"] = message
	}
	return c.doEmpty(ctx, http_POST, "/permission/"+requestID+"/reply", nil, body, 200)
}
