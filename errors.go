package opencode

import (
	"encoding/json"
	"fmt"
)

// APIError 表示服务端返回的非 2xx 响应。
type APIError struct {
	Status  int
	Type    string
	Message string
}

func (e *APIError) Error() string {
	if e.Type == "" {
		return fmt.Sprintf("opencode: status %d: %s", e.Status, e.Message)
	}
	return fmt.Sprintf("opencode: status %d %s: %s", e.Status, e.Type, e.Message)
}

type errorBody struct {
	Type    string `json:"type"`
	Message string `json:"message"`
	Error   string `json:"error"`
}

func parseAPIError(status int, body []byte) *APIError {
	ae := &APIError{Status: status}
	var b errorBody
	if err := json.Unmarshal(body, &b); err == nil {
		ae.Type = b.Type
		if b.Message != "" {
			ae.Message = b.Message
		} else {
			ae.Message = b.Error
		}
	}
	if ae.Message == "" {
		ae.Message = string(body)
	}
	return ae
}
