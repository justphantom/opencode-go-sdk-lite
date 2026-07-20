package opencode

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sync"
	"time"
)

// messageID 生成对齐 opencode 官方 MessageID.create()：
// 格式 msg_<12 hex><14 base62>，共 30 字符。hex 段由毫秒时间戳+计数器组合而成，
// 单调非递减（防 NTP 回拨导致 prompt 被静默丢弃）；base62 段为随机后缀。
//
// 移植自 lark-bridge/internal/opencodeserve/id.go（已验证）。

const (
	msgPrefix      = "msg"
	msgRandLen     = 14
	msgTimeShift   = 12 // 0x1000
	msgTsMask36    = (uint64(1) << 36) - 1
	base62Alphabet = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"
)

type msgIDState struct {
	mu      sync.Mutex
	lastMs  int64
	counter uint64
}

var defaultMsgIDState = &msgIDState{}

// GenerateMessageID 生成一个新的 message id。
func GenerateMessageID() (string, error) {
	return GenerateMessageIDAt(time.Now().UnixMilli())
}

// GenerateMessageIDAt 用指定毫秒时间戳生成 id，便于测试。
func GenerateMessageIDAt(ms int64) (string, error) {
	ms, seq := defaultMsgIDState.nextSeq(ms)

	now := (uint64(ms) & msgTsMask36) << msgTimeShift
	now |= seq & 0xFFF

	var b [6]byte
	for i := range 6 { //nolint:gosec // 6 字节循环无溢出
		b[i] = byte(now >> (40 - 8*i)) //nolint:gosec // int→byte 截断是预期行为
	}

	rand, err := base62Rand(msgRandLen)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s_%s%s", msgPrefix, hex.EncodeToString(b[:]), rand), nil
}

// nextSeq 推进计数器并按 ms 钳制，保证 hex 段单调非递减。返回钳制后的 ms 与 seq。
func (s *msgIDState) nextSeq(ms int64) (int64, uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if ms <= s.lastMs {
		ms = s.lastMs // 时钟回拨或同毫秒：钳到 lastMs 保证非递减
	} else {
		s.lastMs = ms
	}
	s.counter++
	return ms, s.counter
}

func base62Rand(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	out := make([]byte, n)
	for i := range n { //nolint:gosec // 循环范围 n=14
		out[i] = base62Alphabet[int(buf[i])%len(base62Alphabet)] //nolint:gosec // byte→int 转 base62 索引
	}
	return string(out), nil
}
