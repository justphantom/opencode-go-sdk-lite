package opencode

import (
	"encoding/json"
	"io"
	"reflect"
	"strings"
	"testing"
)

func TestSSEScanner_singleFrame(t *testing.T) {
	src := "id: evt_1\nevent: session.idle\ndata: {\"type\":\"session.idle\",\"data\":{\"sessionID\":\"ses_1\"}}\n\n"
	sc := newSSEScanner(strings.NewReader(src))
	id, ev, data, err := sc.next()
	if err != nil {
		t.Fatalf("next: %v", err)
	}
	if id != "evt_1" {
		t.Errorf("id = %q", id)
	}
	if ev != "session.idle" {
		t.Errorf("event = %q", ev)
	}
	if !strings.Contains(data, "session.idle") {
		t.Errorf("data = %q", data)
	}
	// 流尾应返回 EOF
	if _, _, _, err := sc.next(); err != io.EOF {
		t.Errorf("tail err = %v, want EOF", err)
	}
}

func TestSSEScanner_multiLineData(t *testing.T) {
	// 多个 data: 行按 \n 拼接
	src := "data: line1\ndata: line2\n\n"
	sc := newSSEScanner(strings.NewReader(src))
	_, _, data, err := sc.next()
	if err != nil {
		t.Fatalf("next: %v", err)
	}
	if data != "line1\nline2" {
		t.Errorf("data = %q", data)
	}
}

func TestSSEScanner_multipleFrames(t *testing.T) {
	src := "data: {\"type\":\"a\"}\n\ndata: {\"type\":\"b\"}\n\n"
	sc := newSSEScanner(strings.NewReader(src))
	var got []string
	for {
		_, _, data, err := sc.next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("next: %v", err)
		}
		got = append(got, data)
	}
	want := []string{`{"type":"a"}`, `{"type":"b"}`}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestSSEScanner_skipsComment(t *testing.T) {
	src := ": this is a comment\ndata: {\"type\":\"x\"}\n\n"
	sc := newSSEScanner(strings.NewReader(src))
	_, _, data, err := sc.next()
	if err != nil {
		t.Fatalf("next: %v", err)
	}
	if data != `{"type":"x"}` {
		t.Errorf("data = %q", data)
	}
}

func TestDecodeEvent_fullEnvelope(t *testing.T) {
	data := `{"id":"evt_1","type":"session.next.text.delta","durable":{"aggregateID":"ses_1","seq":42,"version":1},"data":{"timestamp":1.5,"sessionID":"ses_1","assistantMessageID":"msg_1","textID":"txt_1","delta":"hi"}}`
	ev, err := decodeEvent("", "", data)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if ev.Type != EventSessionNextTextDelta {
		t.Errorf("type = %q", ev.Type)
	}
	if ev.Durable == nil || ev.Durable.Seq != 42 {
		t.Errorf("durable = %+v", ev.Durable)
	}
	var td TextDeltaData
	if err := json.Unmarshal(ev.Data, &td); err != nil {
		t.Fatalf("data unmarshal: %v", err)
	}
	if td.Delta != "hi" || td.SessionID != "ses_1" {
		t.Errorf("delta data = %+v", td)
	}
}
