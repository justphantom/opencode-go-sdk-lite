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

// V1 错误格式为 {"name":"BadRequest","data":{"message":"..."}}；
// type/message/error 兼容其他历史格式。
type errorBody struct {
	Name string `json:"name"`
	Data struct {
		Message string `json:"message"`
	} `json:"data"`
	Type    string `json:"type"`
	Message string `json:"message"`
	Error   string `json:"error"`
}

func parseAPIError(status int, body []byte) *APIError {
	ae := &APIError{Status: status}
	var b errorBody
	if err := json.Unmarshal(body, &b); err == nil {
		ae.Type = b.Name
		if ae.Type == "" {
			ae.Type = b.Type
		}
		switch {
		case b.Data.Message != "":
			ae.Message = b.Data.Message
		case b.Message != "":
			ae.Message = b.Message
		default:
			ae.Message = b.Error
		}
	}
	if ae.Message == "" {
		ae.Message = string(body)
	}
	return ae
}
