package opencode

import "io"

// drainLimit 限制 drain 读取量，防止恶意/卡死的上游用超大 body 钉住 goroutine。
const drainLimit int64 = 1 << 20 // 1 MiB

// drainAndClose 把 body 读完（上限 1MiB）再关闭。错误静默忽略——用于错误路径清理。
func drainAndClose(body io.ReadCloser) {
	if body == nil {
		return
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(body, drainLimit))
	_ = body.Close()
}
