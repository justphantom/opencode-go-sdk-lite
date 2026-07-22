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
// directory 必须与该 question 所在 Run 的 Location 一致：opencode serve 按
// directory 隔离 pending question，不带 directory 时 serve 在默认上下文找不到
// 请求并返回 404。
func (c *Client) ReplyQuestion(ctx context.Context, requestID, directory string, r *QuestionReply) error {
	if r == nil {
		r = &QuestionReply{}
	}
	return c.doEmpty(ctx, http_POST, "/question/"+requestID+"/reply", dirQuery(directory), r, 200)
}

// RejectQuestion 拒绝一条挂起的问题请求。directory 同 ReplyQuestion。
func (c *Client) RejectQuestion(ctx context.Context, requestID, directory string) error {
	return c.doEmpty(ctx, http_POST, "/question/"+requestID+"/reject", dirQuery(directory), nil, 200)
}
