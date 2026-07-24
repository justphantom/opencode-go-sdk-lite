package opencode

import (
	"context"
	"fmt"
	"net/url"
)

// ListPermissions 列出会话内挂起的权限请求。
// V1 的 GET /permission 是全局 pending 列表，此处按 sessionID 过滤。
func (c *Client) ListPermissions(ctx context.Context, sessionID string) ([]PermissionRequest, error) {
	all, err := c.listAllPermissions(ctx)
	if err != nil {
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

// listAllPermissions 拉取全局 pending 权限列表（无 sessionID 过滤）。
// pollAsked 在父+多子 session 场景下用一次拉取替代 N 次按 sid 查询。
func (c *Client) listAllPermissions(ctx context.Context) ([]PermissionRequest, error) {
	var all []PermissionRequest
	if err := c.doJSON(ctx, http_GET, "/permission", nil, nil, &all, 0); err != nil {
		return nil, err
	}
	return all, nil
}

// ReplyPermission 回复一条挂起的权限请求。
// reply 取值 once / always / reject；message 可选，附在回复上。
// directory 必须与该 permission 所在 Run 的 Location 一致：opencode serve 按
// directory 隔离 pending permission，不带 directory 时 serve 返回 404。
func (c *Client) ReplyPermission(ctx context.Context, requestID, directory, reply, message string) error {
	switch reply {
	case PermissionReplyOnce, PermissionReplyAlways, PermissionReplyReject:
	default:
		return fmt.Errorf("opencode: invalid permission reply %q", reply)
	}
	body := map[string]any{"reply": reply}
	if message != "" {
		body["message"] = message
	}
	return c.doEmpty(ctx, http_POST, "/permission/"+requestID+"/reply", dirQuery(directory), body, 200)
}

// dirQuery builds the directory query used to scope a reply to the serve
// workspace that owns the pending request. Returns nil when directory is empty
// so doEmpty omits the query entirely (newRequest skips an empty Values).
func dirQuery(directory string) url.Values {
	if directory == "" {
		return nil
	}
	return url.Values{"directory": {directory}}
}
