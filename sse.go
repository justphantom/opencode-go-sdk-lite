package opencode

import (
	"bufio"
	"encoding/json"
	"io"
	"strings"
)

// sseFrame 是 SSE 帧解析的中间产物。每帧由空行分隔；data 行可多行拼接。
type sseScanner struct {
	r *bufio.Reader
}

func newSSEScanner(r io.Reader) *sseScanner {
	return &sseScanner{r: bufio.NewReader(r)}
}

// next 读取并解析下一帧。返回的 data 是该帧所有 data: 行的拼接（按 \n 分隔）。
// io.EOF 表示流正常结束且无缓存数据。
func (s *sseScanner) next() (id, eventType, data string, err error) {
	var dataLines []string
	for {
		line, rerr := s.r.ReadString('\n')
		if rerr != nil && rerr != io.EOF {
			return "", "", "", rerr
		}
		trimmed := strings.TrimRight(line, "\r\n")

		if trimmed == "" {
			// 帧边界。若已累积数据则返回；否则可能是首部空行，跳过。
			if len(dataLines) > 0 || id != "" || eventType != "" || rerr == io.EOF {
				if len(dataLines) > 0 {
					data = strings.Join(dataLines, "\n")
				}
				if rerr == io.EOF && len(dataLines) == 0 && id == "" && eventType == "" {
					return "", "", "", io.EOF
				}
				return id, eventType, data, nil
			}
			if rerr == io.EOF {
				return "", "", "", io.EOF
			}
			continue
		}

		var field, value string
		if idx := strings.IndexByte(trimmed, ':'); idx >= 0 {
			field = trimmed[:idx]
			value = strings.TrimPrefix(trimmed[idx+1:], " ")
		} else {
			field = trimmed
		}

		switch field {
		case "id":
			id = value
		case "event":
			eventType = value
		case "data":
			dataLines = append(dataLines, value)
		case "retry", "comment", "":
			// 忽略
		}

		if rerr == io.EOF {
			if len(dataLines) > 0 || id != "" || eventType != "" {
				if len(dataLines) > 0 {
					data = strings.Join(dataLines, "\n")
				}
				return id, eventType, data, nil
			}
			return "", "", "", io.EOF
		}
	}
}

// decodeEvent 把一帧 data（JSON）解析成 Event。
// spec 中 SSE data 是 SessionDurableEventStream 的 oneOf 之一，结构同 Event。
func decodeEvent(id, eventType, data string) (Event, error) {
	ev := Event{}
	if data == "" {
		return ev, nil
	}
	if err := json.Unmarshal([]byte(data), &ev); err != nil {
		return ev, err
	}
	// spec 的 SSE 帧头部 event 字段可能与 JSON.type 一致；以 JSON 内字段为准，空则回退。
	if ev.Type == "" {
		ev.Type = eventType
	}
	if ev.ID == "" {
		ev.ID = id
	}
	return ev, nil
}
