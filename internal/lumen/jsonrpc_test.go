package lumen

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"sync"
	"testing"
	"time"
)

func newTestConn(t *testing.T) (*rpcConn, *io.PipeWriter, *bytes.Buffer) {
	t.Helper()
	reader, writer := io.Pipe()
	out := &bytes.Buffer{}
	conn := &rpcConn{}
	conn.init("test-agent", &bufferWriteCloser{Buffer: out}, reader)
	t.Cleanup(func() { conn.closeTransport(); _ = writer.Close() })
	return conn, writer, out
}

// T-P2-01：malformed 行不得使 read loop 結束；後續訊息仍正常處理。
func TestRPCConnSurvivesMalformedLines(t *testing.T) {
	conn, writer, _ := newTestConn(t)
	go conn.readLoop()

	_, _ = io.WriteString(writer, "not json at all\n{\"broken\":\n\n")
	_, _ = io.WriteString(writer, `{"method":"note/one","params":{}}`+"\n")
	select {
	case message := <-conn.notifications:
		if message.Method != "note/one" {
			t.Fatalf("notification = %#v", message)
		}
	case <-time.After(time.Second):
		t.Fatal("notification after malformed lines was not delivered")
	}
	if got := conn.malformedCount(); got != 2 {
		t.Fatalf("malformed count = %d, want 2", got)
	}
}

// T-P2-01：單一 handler panic 不得結束 session。
func TestRPCConnHandlerPanicDoesNotEndLoop(t *testing.T) {
	conn, writer, _ := newTestConn(t)
	var mu sync.Mutex
	var seen []string
	done := make(chan struct{})
	conn.handle = func(message rpcMessage) {
		mu.Lock()
		seen = append(seen, message.Method)
		mu.Unlock()
		if message.Method == "boom" {
			panic("handler failure")
		}
		if message.Method == "after" {
			close(done)
		}
	}
	go conn.readLoop()

	_, _ = io.WriteString(writer, `{"method":"boom"}`+"\n"+`{"method":"after"}`+"\n")
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("message after handler panic was not handled")
	}
	select {
	case <-conn.closed:
		t.Fatal("handler panic closed the connection")
	default:
	}
}

// 數值 id 0 的 server request 要被當成 request（不是「沒有 id」），回覆原樣帶回 id。
func TestRPCConnAcceptsNumericIDZeroRequest(t *testing.T) {
	conn, writer, out := newTestConn(t)
	conn.jsonrpc = true
	go conn.readLoop()

	_, _ = io.WriteString(writer, `{"jsonrpc":"2.0","id":0,"method":"session/request_permission","params":{"x":1}}`+"\n")
	select {
	case request := <-conn.requests:
		if string(request.id) != "0" || request.method != "session/request_permission" {
			t.Fatalf("request = %#v", request)
		}
		if err := conn.respond(request.id, map[string]string{"ok": "1"}); err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("id 0 request was not delivered as a request")
	}
	var reply struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      json.RawMessage `json:"id"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(out.Bytes()), &reply); err != nil {
		t.Fatal(err)
	}
	if string(reply.ID) != "0" || reply.JSONRPC != "2.0" {
		t.Fatalf("reply = %s", out.String())
	}
}

func TestRPCConnCallRoundTripAndError(t *testing.T) {
	reader, writer := io.Pipe()
	conn := &rpcConn{}
	responder := &scriptedRPCWriter{output: writer, handler: func(method string) (any, string) {
		if method == "bad" {
			return nil, "nope"
		}
		return map[string]string{"value": "ok"}, ""
	}}
	conn.init("test-agent", responder, reader)
	go conn.readLoop()
	t.Cleanup(conn.closeTransport)

	var result struct{ Value string }
	if err := conn.Call("good", nil, &result); err != nil || result.Value != "ok" {
		t.Fatalf("Call() = %v, %#v", err, result)
	}
	if err := conn.Call("bad", nil, nil); err == nil || !strings.Contains(err.Error(), "nope") {
		t.Fatalf("Call(bad) error = %v", err)
	}
}
