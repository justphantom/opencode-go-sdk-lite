package opencode

import "context"

// ListQuestions 列出会话内挂起的问题请求。
func (c *Client) ListQuestions(ctx context.Context, sessionID string) ([]QuestionV2Request, error) {
	var wrapped struct {
		Data []QuestionV2Request `json:"data"`
	}
	if err := c.doJSON(ctx, http_GET, "/api/session/"+sessionID+"/question", nil, nil, &wrapped, 0); err != nil {
		return nil, err
	}
	return wrapped.Data, nil
}

// ReplyQuestion 回答一条挂起的问题请求。answers 与 questions 一一对应。
func (c *Client) ReplyQuestion(ctx context.Context, sessionID, requestID string, r *QuestionReply) error {
	if r == nil {
		r = &QuestionReply{}
	}
	return c.doEmpty(ctx, http_POST, "/api/session/"+sessionID+"/question/"+requestID+"/reply", nil, r, 204)
}

// RejectQuestion 拒绝一条挂起的问题请求。
func (c *Client) RejectQuestion(ctx context.Context, sessionID, requestID string) error {
	return c.doEmpty(ctx, http_POST, "/api/session/"+sessionID+"/question/"+requestID+"/reject", nil, nil, 204)
}
