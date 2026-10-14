package lumen

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
	"time"
)

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type rpcMessage struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
	Result json.RawMessage `json:"result"`
	Error  *rpcError       `json:"error"`
}

type pendingCall struct {
	result chan rpcMessage
}

type serverRequest struct {
	id     json.RawMessage
	method string
	params json.RawMessage
}

const (
	rpcDefaultCallTimeout = 30 * time.Second
	// rpcMaxLine 是單行 JSON 的上限；超過的行整行丟棄並計入 malformed。
	rpcMaxLine = 32 << 20
)

// rpcConn 是 line-delimited JSON-RPC 2.0 連線，appServer（codex）與 acpBackend 共用。
// 每一行一則訊息；無法解析的行只計數並略過，不會結束 read loop。
//
// 訊息分流：response 交給等待中的 Call；server request / notification 在設定了 handle 時
// 由 read loop 依線路順序同步呼叫 handle，否則送進 requests / notifications channel。
// request id 一律以原始 JSON 保留（數值 0 是合法 id）。
type rpcConn struct {
	label   string // 錯誤訊息用的名稱，例如 "app-server"
	jsonrpc bool   // 送出的訊息是否帶 "jsonrpc":"2.0"
	stdin   io.WriteCloser
	stdout  io.ReadCloser

	// handle 非 nil 時，read loop 同步處理 request / notification（panic 會被攔下）。
	handle func(rpcMessage)
	// onClose 在 read loop 結束後呼叫一次（err 為 read 失敗原因，EOF 為 nil）。
	onClose func(err error)

	writeMu sync.Mutex
	mu      sync.Mutex
	nextID  int64
	pending map[string]pendingCall

	notifications chan rpcMessage
	requests      chan serverRequest
	errors        chan error
	closed        chan struct{}
	closeOnce     sync.Once
	failure       error

	malformed atomic.Int64
}

func (c *rpcConn) init(label string, stdin io.WriteCloser, stdout io.ReadCloser) {
	c.label = label
	c.stdin = stdin
	c.stdout = stdout
	c.nextID = 1
	c.pending = make(map[string]pendingCall)
	c.notifications = make(chan rpcMessage, 256)
	c.requests = make(chan serverRequest, 64)
	c.errors = make(chan error, 1)
	c.closed = make(chan struct{})
}

func (c *rpcConn) malformedCount() int64 { return c.malformed.Load() }

func (c *rpcConn) readLoop() {
	reader := bufio.NewReaderSize(c.stdout, 64<<10)
	for {
		line, tooLong, err := readRPCLine(reader)
		if !tooLong {
			if trimmed := bytes.TrimSpace(line); len(trimmed) > 0 {
				if !c.dispatch(trimmed) {
					return
				}
			}
		} else {
			c.malformed.Add(1)
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				c.closeWithError(nil)
				c.finish(nil)
			} else {
				failure := fmt.Errorf("read %s: %w", c.label, err)
				c.closeWithError(failure)
				c.finish(failure)
			}
			return
		}
	}
}

func (c *rpcConn) finish(err error) {
	if c.onClose != nil {
		c.onClose(err)
	}
}

// readRPCLine 讀一行；超過 rpcMaxLine 時丟掉剩餘部分並回報 tooLong。
func readRPCLine(reader *bufio.Reader) (line []byte, tooLong bool, err error) {
	var buffer []byte
	for {
		chunk, readErr := reader.ReadSlice('\n')
		if !tooLong {
			if len(buffer)+len(chunk) > rpcMaxLine {
				tooLong = true
				buffer = nil
			} else {
				buffer = append(buffer, chunk...)
			}
		}
		if errors.Is(readErr, bufio.ErrBufferFull) {
			continue
		}
		return buffer, tooLong, readErr
	}
}

// dispatch 回傳 false 表示 read loop 應結束（連線已關閉）。
func (c *rpcConn) dispatch(line []byte) bool {
	var message rpcMessage
	if err := json.Unmarshal(line, &message); err != nil {
		c.malformed.Add(1)
		return true
	}
	hasID := len(message.ID) > 0 && string(message.ID) != "null"
	switch {
	case hasID && message.Method == "":
		key := string(message.ID)
		c.mu.Lock()
		call, ok := c.pending[key]
		if ok {
			delete(c.pending, key)
		}
		c.mu.Unlock()
		if ok {
			call.result <- message
		}
		return true
	case c.handle != nil:
		c.safeHandle(message)
		return true
	case hasID:
		select {
		case c.requests <- serverRequest{message.ID, message.Method, message.Params}:
		case <-c.closed:
			return false
		}
		return true
	default:
		select {
		case c.notifications <- message:
		case <-c.closed:
			return false
		}
		return true
	}
}

func (c *rpcConn) safeHandle(message rpcMessage) {
	defer func() {
		if recover() != nil {
			c.malformed.Add(1)
		}
	}()
	c.handle(message)
}

func (c *rpcConn) closeWithError(err error) {
	if err != nil {
		c.mu.Lock()
		if c.failure == nil {
			c.failure = err
		}
		c.mu.Unlock()
		select {
		case c.errors <- err:
		default:
		}
	}
	c.closeOnce.Do(func() { close(c.closed) })
}

func (c *rpcConn) failureError() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.failure
}

// closeTransport 標記連線結束並關閉 stdin / stdout。
func (c *rpcConn) closeTransport() {
	c.closeWithError(nil)
	if c.stdin != nil {
		_ = c.stdin.Close()
	}
	if c.stdout != nil {
		_ = c.stdout.Close()
	}
}

func (c *rpcConn) Call(method string, params any, target any) error {
	return c.call(context.Background(), rpcDefaultCallTimeout, method, params, target)
}

// call 的 timeout <= 0 表示不設時限（例如會 streaming 的 session/prompt）。
func (c *rpcConn) call(ctx context.Context, timeout time.Duration, method string, params any, target any) error {
	c.mu.Lock()
	id := c.nextID
	c.nextID++
	rawID := json.RawMessage(fmt.Sprintf("%d", id))
	result := make(chan rpcMessage, 1)
	c.pending[string(rawID)] = pendingCall{result: result}
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		delete(c.pending, string(rawID))
		c.mu.Unlock()
	}()

	request := map[string]any{"method": method, "id": id, "params": params}
	if err := c.writeMessage(request); err != nil {
		return err
	}

	var expired <-chan time.Time
	if timeout > 0 {
		timer := time.NewTimer(timeout)
		defer timer.Stop()
		expired = timer.C
	}
	select {
	case message := <-result:
		if message.Error != nil {
			return fmt.Errorf("%s (%d): %s", method, message.Error.Code, message.Error.Message)
		}
		if target == nil || len(message.Result) == 0 || string(message.Result) == "null" {
			return nil
		}
		if err := json.Unmarshal(message.Result, target); err != nil {
			return fmt.Errorf("decode %s response: %w", method, err)
		}
		return nil
	case err := <-c.errors:
		return err
	case <-c.closed:
		if err := c.failureError(); err != nil {
			return err
		}
		return fmt.Errorf("%s closed before %s completed", c.label, method)
	case <-expired:
		return fmt.Errorf("timeout waiting for %s", method)
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (c *rpcConn) notify(method string, params any) error {
	return c.writeMessage(map[string]any{"method": method, "params": params})
}

func (c *rpcConn) respond(id json.RawMessage, result any) error {
	var raw json.RawMessage
	if result != nil {
		encoded, err := json.Marshal(result)
		if err != nil {
			return err
		}
		raw = encoded
	} else {
		raw = json.RawMessage(`{}`)
	}
	return c.writeMessage(map[string]any{
		"id":     id,
		"result": raw,
	})
}

func (c *rpcConn) respondError(id json.RawMessage, code int, message string) error {
	return c.writeMessage(map[string]any{
		"id": id,
		"error": map[string]any{
			"code":    code,
			"message": message,
		},
	})
}

func (c *rpcConn) writeMessage(message map[string]any) error {
	if c.jsonrpc {
		message["jsonrpc"] = "2.0"
	}
	encoded, err := json.Marshal(message)
	if err != nil {
		return err
	}
	if c.stdin == nil {
		return fmt.Errorf("%s stdin unavailable", c.label)
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	select {
	case <-c.closed:
		return fmt.Errorf("%s closed", c.label)
	default:
	}
	if _, err := c.stdin.Write(append(encoded, '\n')); err != nil {
		return fmt.Errorf("write %s: %w", c.label, err)
	}
	return nil
}
