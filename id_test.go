package opencode

import (
	"strings"
	"sync"
	"testing"
)

func TestGenerateMessageID_Format(t *testing.T) {
	id, err := generateMessageIDAt(1_700_000_000_000)
	if err != nil {
		t.Fatalf("GenerateMessageIDAt: %v", err)
	}
	if !strings.HasPrefix(id, "msg_") {
		t.Errorf("missing prefix: %q", id)
	}
	// msg_ + 12 hex + 14 base62 = 4 + 12 + 14 = 30
	if len(id) != 30 {
		t.Errorf("len = %d, want 30; id=%q", len(id), id)
	}
	hexPart := id[4:16]
	for _, c := range hexPart {
		isHex := (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')
		if !isHex {
			t.Errorf("hex part contains non-hex char %q in %q", c, hexPart)
		}
	}
}

func TestGenerateMessageID_MonotonicSameMs(t *testing.T) {
	id1, _ := generateMessageIDAt(1_000)
	id2, _ := generateMessageIDAt(1_000)
	// 同毫秒内 counter 递增，hex 段（前 12 hex 字符）非递减
	if id1[4:16] > id2[4:16] {
		t.Errorf("same-ms not monotonic: hex1=%q > hex2=%q", id1[4:16], id2[4:16])
	}
}

func TestGenerateMessageID_RollbackStillIncreasing(t *testing.T) {
	// 先在 ms=1000 生成，再退回 ms=999，hex 段应钳制为非递减
	id1, _ := generateMessageIDAt(1_000)
	id2, _ := generateMessageIDAt(999)
	if id2[4:16] < id1[4:16] {
		t.Errorf("rollback broke monotonicity: hex1=%q > hex2=%q", id1[4:16], id2[4:16])
	}
}

func TestGenerateMessageID_ConcurrentNoDup(t *testing.T) {
	const goroutines = 100
	const perG = 1000
	var mu sync.Mutex
	seen := make(map[string]struct{}, goroutines*perG)
	var wg sync.WaitGroup
	for range goroutines {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range perG {
				id, err := GenerateMessageID()
				if err != nil {
					t.Errorf("GenerateMessageID: %v", err)
					return
				}
				mu.Lock()
				seen[id] = struct{}{}
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	want := goroutines * perG
	if len(seen) != want {
		t.Errorf("unique count = %d, want %d (collisions)", len(seen), want)
	}
}

func TestGeneratePartID_Format(t *testing.T) {
	id, err := GeneratePartID()
	if err != nil {
		t.Fatalf("GeneratePartID: %v", err)
	}
	if !strings.HasPrefix(id, "prt_") {
		t.Errorf("missing prefix: %q", id)
	}
	if len(id) != 30 {
		t.Errorf("len = %d, want 30; id=%q", len(id), id)
	}
}

// 官方实现中所有前缀共用同一单调状态：交叉生成 msg/prt 仍须非递减。
func TestGenerateID_SharedMonotonicState(t *testing.T) {
	m1, _ := generateMessageIDAt(5_000)
	p1, _ := GeneratePartID()
	m2, _ := generateMessageIDAt(5_000)
	if p1[4:16] < m1[4:16] {
		t.Errorf("prt hex %q < msg hex %q", p1[4:16], m1[4:16])
	}
	if m2[4:16] < p1[4:16] {
		t.Errorf("msg hex %q < prt hex %q", m2[4:16], p1[4:16])
	}
}
