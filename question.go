package opencode

import "context"

// ListQuestions 列出会话内挂起的问题请求。
// V1 的 GET /question 是全局 pending 列表，此处按 sessionID 过滤。
func (c *Client) ListQuestions(ctx context.Context, sessionID string) ([]QuestionRequest, error) {
	var all []QuestionRequest
	if err := c.doJSON(ctx, http_GET, "/question", nil, nil, &all, 0); err != nil {
		return nil, err
	}
	out := make([]QuestionRequest, 0, len(all))
	for _, q := range all {
		if q.SessionID == sessionID {
			out = append(out, q)
		}
	}
	return out, nil
}

// ReplyQuestion 回答一条挂起的问题请求。answers 与 questions 一一对应。
func (c *Client) ReplyQuestion(ctx context.Context, requestID string, r *QuestionReply) error {
	if r == nil {
		r = &QuestionReply{}
	}
	return c.doEmpty(ctx, http_POST, "/question/"+requestID+"/reply", nil, r, 200)
}

// RejectQuestion 拒绝一条挂起的问题请求。
func (c *Client) RejectQuestion(ctx context.Context, requestID string) error {
	return c.doEmpty(ctx, http_POST, "/question/"+requestID+"/reject", nil, nil, 200)
}
