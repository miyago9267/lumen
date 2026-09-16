package lumen

import (
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"testing"
)

type nativeRPCWriter struct {
	output  *io.PipeWriter
	handler func(method string, params json.RawMessage) any
}

func (w *nativeRPCWriter) Write(value []byte) (int, error) {
	var request struct {
		ID     int64           `json:"id"`
		Method string          `json:"method"`
		Params json.RawMessage `json:"params"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(string(value))), &request); err != nil {
		return 0, err
	}
	result := w.handler(request.Method, request.Params)
	encoded, err := json.Marshal(map[string]any{"id": request.ID, "result": result})
	if err != nil {
		return 0, err
	}
	if _, err := fmt.Fprintf(w.output, "%s\n", encoded); err != nil {
		return 0, err
	}
	return len(value), nil
}

func (w *nativeRPCWriter) Close() error {
	return w.output.Close()
}

func nativeRPCClient(t *testing.T, handler func(string, json.RawMessage) any) *appServer {
	t.Helper()
	reader, writer := io.Pipe()
	client := newAppServer(&nativeRPCWriter{output: writer, handler: handler}, reader, &exec.Cmd{})
	go client.readLoop()
	t.Cleanup(client.Close)
	return client
}

func TestForkThreadAtSendsLastTurnID(t *testing.T) {
	var gotMethod string
	var gotParams map[string]any
	client := nativeRPCClient(t, func(method string, raw json.RawMessage) any {
		gotMethod = method
		_ = json.Unmarshal(raw, &gotParams)
		return map[string]any{
			"thread":          map[string]any{"id": "forked"},
			"model":           "gpt-test",
			"cwd":             "/tmp/project",
			"approvalPolicy":  "on-request",
			"sandbox":         "read-only",
			"reasoningEffort": "medium",
		}
	})

	thread, err := client.ForkThreadAt("thread-1", "turn-2", runtimeOverrides{})
	if err != nil {
		t.Fatalf("ForkThreadAt() error = %v", err)
	}
	if gotMethod != "thread/fork" || thread.ID != "forked" {
		t.Fatalf("fork response = method:%q thread:%#v", gotMethod, thread)
	}
	if gotParams["threadId"] != "thread-1" || gotParams["lastTurnId"] != "turn-2" {
		t.Fatalf("fork params = %#v", gotParams)
	}
}

func TestListThreadsSupportsGlobalHistory(t *testing.T) {
	var gotParams map[string]any
	client := nativeRPCClient(t, func(method string, raw json.RawMessage) any {
		if method != "thread/list" {
			return map[string]any{}
		}
		_ = json.Unmarshal(raw, &gotParams)
		return map[string]any{
			"data": []map[string]any{{"id": "thread-other", "cwd": "/tmp/other", "preview": "other session"}},
		}
	})
	threads, err := client.ListThreads("", 9)
	if err != nil {
		t.Fatalf("ListThreads() error = %v", err)
	}
	if len(threads) != 1 || threads[0].ID != "thread-other" {
		t.Fatalf("global thread list = %#v", threads)
	}
	if _, ok := gotParams["cwd"]; ok {
		t.Fatalf("global thread list unexpectedly filtered cwd: %#v", gotParams)
	}
	if gotParams["sortKey"] != "updated_at" {
		t.Fatalf("global thread list sort key = %#v, want updated_at", gotParams["sortKey"])
	}
}

func TestDashboardAttachesCrossSessionHistory(t *testing.T) {
	client := nativeRPCClient(t, func(method string, _ json.RawMessage) any {
		switch method {
		case "thread/list":
			return map[string]any{
				"data": []map[string]any{{
					"id": "thread-other", "cwd": "/tmp/other", "name": "other", "preview": "old work",
				}},
			}
		case "thread/resume":
			return map[string]any{
				"thread": map[string]any{
					"id": "thread-other", "cwd": "/tmp/other", "turns": []map[string]any{{
						"id": "turn-old", "items": []map[string]any{
							{"type": "userMessage", "content": []map[string]any{{"type": "text", "text": "old question"}}},
							{"type": "agentMessage", "text": "old answer"},
						},
					}},
				},
				"model": "gpt-test", "cwd": "/tmp/other", "reasoningEffort": "medium",
			}
		default:
			return map[string]any{}
		}
	})
	frontend := &ui{
		client:     client,
		screenMode: true,
		cwd:        "/tmp/project",
		thread:     threadSummary{ID: "thread-current"},
	}
	if err := frontend.openDashboard(); err != nil {
		t.Fatalf("openDashboard() error = %v", err)
	}
	if err := frontend.handleDashboardKey(keyEvent{typ: keyDown}); err != nil {
		t.Fatalf("dashboard selection error = %v", err)
	}
	if err := frontend.handleDashboardKey(keyEvent{typ: keyEnter}); err != nil {
		t.Fatalf("dashboard attach error = %v", err)
	}
	if frontend.cwd != "/tmp/other" || frontend.dashboard {
		t.Fatalf("attached session state = cwd:%q dashboard:%t", frontend.cwd, frontend.dashboard)
	}
	plain := stripANSI(frontend.screenFrame(100, 24))
	if !strings.Contains(plain, "old question") || !strings.Contains(plain, "old answer") {
		t.Fatalf("resumed history missing: %q", plain)
	}
	if strings.Contains(plain, "┌─ you ") || strings.Contains(plain, "┌─ assistant ") {
		t.Fatalf("resumed history is over-boxed: %q", plain)
	}
	if !strings.Contains(plain, "you › old question") || !strings.Contains(plain, "assistant › old answer") {
		t.Fatalf("resumed history role labels missing: %q", plain)
	}
}

func TestNativeModelCommandUsesAppServer(t *testing.T) {
	client := nativeRPCClient(t, func(method string, _ json.RawMessage) any {
		if method != "model/list" {
			return map[string]any{}
		}
		return map[string]any{"data": []map[string]any{{"id": "gpt-test", "displayName": "GPT Test"}}}
	})
	frontend := &ui{
		client:     client,
		screenMode: true,
		thread:     threadSummary{ID: "thread-1"},
		cwd:        "/tmp/project",
	}
	if err := frontend.handleCommand("/model"); err != nil {
		t.Fatalf("model command error = %v", err)
	}
	plain := stripANSI(frontend.screenFrame(100, 16))
	if !strings.Contains(plain, "GPT Test") {
		t.Fatalf("model list was not rendered: %q", plain)
	}
}

func TestNativeReviewCommandStartsInlineReview(t *testing.T) {
	var gotMethod string
	var gotParams map[string]any
	client := nativeRPCClient(t, func(method string, raw json.RawMessage) any {
		gotMethod = method
		_ = json.Unmarshal(raw, &gotParams)
		return map[string]any{"reviewThreadId": "thread-1", "turn": map[string]string{"id": "review-turn"}}
	})
	frontend := &ui{
		client:     client,
		screenMode: true,
		thread:     threadSummary{ID: "thread-1"},
		cwd:        "/tmp/project",
	}
	if err := frontend.handleCommand("/review base main"); err != nil {
		t.Fatalf("review command error = %v", err)
	}
	if gotMethod != "review/start" || frontend.turnID != "review-turn" || !frontend.busy {
		t.Fatalf("review state = method:%q turn:%q busy:%t params:%#v", gotMethod, frontend.turnID, frontend.busy, gotParams)
	}
	target, ok := gotParams["target"].(map[string]any)
	if !ok || target["type"] != "baseBranch" || target["branch"] != "main" || gotParams["delivery"] != "inline" {
		t.Fatalf("review params = %#v", gotParams)
	}
}
