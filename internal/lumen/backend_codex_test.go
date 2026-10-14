package lumen

import (
	"encoding/json"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"reflect"
	"sort"
	"strconv"
	"testing"
)

func int64Ptr(value int64) *int64 { return &value }

func notificationOf(method, params string) rpcMessage {
	return rpcMessage{Method: method, Params: json.RawMessage(params)}
}

// codexGoldenCases 是 codex notification -> Event 的 golden table。
// payload 欄位必須與重構前 UI 讀取的值一致，Label 逐字等於 codex 原值。
var codexGoldenCases = []struct {
	name    string
	message rpcMessage
	want    []Event
}{
	{
		name:    "turn/started",
		message: notificationOf("turn/started", `{"threadId":"thread-1","turn":{"id":"turn-1"}}`),
		want:    []Event{EvTurnStarted{SessionID: "thread-1", TurnID: "turn-1"}},
	},
	{
		name:    "turn/started other thread",
		message: notificationOf("turn/started", `{"threadId":"other","turn":{"id":"turn-1"}}`),
	},
	{
		name:    "turn/started malformed",
		message: notificationOf("turn/started", `{`),
	},
	{
		name:    "item/started commandExecution",
		message: notificationOf("item/started", `{"threadId":"thread-1","item":{"type":"commandExecution","id":"item-1","command":"ls -la"}}`),
		want:    []Event{EvToolStarted{ToolID: "item-1", Kind: ToolCommand, Title: "$ ls -la", Command: "ls -la"}},
	},
	{
		name:    "item/started commandExecution without command",
		message: notificationOf("item/started", `{"threadId":"thread-1","item":{"type":"commandExecution","id":"item-1"}}`),
		want:    []Event{EvToolStarted{ToolID: "item-1", Kind: ToolCommand, Title: "$ "}},
	},
	{
		name:    "item/started fileChange",
		message: notificationOf("item/started", `{"threadId":"thread-1","item":{"type":"fileChange","id":"item-2"}}`),
		want:    []Event{EvToolStarted{ToolID: "item-2", Kind: ToolEdit, Title: "✎ file change"}},
	},
	{
		name:    "item/started mcpToolCall",
		message: notificationOf("item/started", `{"threadId":"thread-1","item":{"type":"mcpToolCall","id":"item-3","server":"docs","tool":"search"}}`),
		want:    []Event{EvToolStarted{ToolID: "item-3", Kind: ToolMCP, Title: "◇ docs/search"}},
	},
	{
		name:    "item/started reasoning",
		message: notificationOf("item/started", `{"threadId":"thread-1","turnId":"turn-1","item":{"type":"reasoning","id":"item-4"}}`),
		want:    []Event{EvReasoningStarted{TurnID: "turn-1"}},
	},
	{
		name:    "item/started unknown type",
		message: notificationOf("item/started", `{"threadId":"thread-1","item":{"type":"userMessage"}}`),
	},
	{
		name:    "item/started other thread",
		message: notificationOf("item/started", `{"threadId":"other","item":{"type":"commandExecution","command":"ls"}}`),
	},
	{
		name:    "item/agentMessage/delta",
		message: notificationOf("item/agentMessage/delta", `{"threadId":"thread-1","turnId":"turn-1","delta":"hello"}`),
		want:    []Event{EvAssistantDelta{TurnID: "turn-1", Text: "hello"}},
	},
	{
		name:    "item/agentMessage/delta other thread",
		message: notificationOf("item/agentMessage/delta", `{"threadId":"other","delta":"hello"}`),
	},
	{
		name:    "item/commandExecution/outputDelta",
		message: notificationOf("item/commandExecution/outputDelta", `{"threadId":"thread-1","itemId":"item-1","delta":"out\n"}`),
		want:    []Event{EvToolOutput{ToolID: "item-1", Text: "out\n"}},
	},
	{
		name:    "item/completed commandExecution success has no label",
		message: notificationOf("item/completed", `{"threadId":"thread-1","item":{"type":"commandExecution","id":"item-1","status":"completed","exitCode":0}}`),
		want:    []Event{EvToolCompleted{ToolID: "item-1", Status: ToolOK, Label: ""}},
	},
	{
		name:    "item/completed commandExecution failed exit 1",
		message: notificationOf("item/completed", `{"threadId":"thread-1","item":{"type":"commandExecution","id":"item-1","status":"failed","exitCode":1}}`),
		want:    []Event{EvToolCompleted{ToolID: "item-1", Status: ToolFailed, Label: "failed exit 1"}},
	},
	{
		name:    "item/completed commandExecution exit only",
		message: notificationOf("item/completed", `{"threadId":"thread-1","item":{"type":"commandExecution","id":"item-1","exitCode":2}}`),
		want:    []Event{EvToolCompleted{ToolID: "item-1", Status: ToolFailed, Label: "exit 2"}},
	},
	{
		name:    "item/completed commandExecution declined",
		message: notificationOf("item/completed", `{"threadId":"thread-1","item":{"type":"commandExecution","id":"item-1","status":"declined"}}`),
		want:    []Event{EvToolCompleted{ToolID: "item-1", Status: ToolCancelled, Label: "declined"}},
	},
	{
		name:    "item/completed fileChange raw status",
		message: notificationOf("item/completed", `{"threadId":"thread-1","item":{"type":"fileChange","id":"item-2","status":"completed"}}`),
		want:    []Event{EvToolCompleted{ToolID: "item-2", Status: ToolOK, Label: "completed"}},
	},
	{
		name:    "item/completed fileChange failed",
		message: notificationOf("item/completed", `{"threadId":"thread-1","item":{"type":"fileChange","id":"item-2","status":"failed"}}`),
		want:    []Event{EvToolCompleted{ToolID: "item-2", Status: ToolFailed, Label: "failed"}},
	},
	{
		name:    "item/completed reasoning",
		message: notificationOf("item/completed", `{"threadId":"thread-1","turnId":"turn-1","item":{"type":"reasoning","id":"item-4"}}`),
		want:    []Event{EvReasoningCompleted{TurnID: "turn-1"}},
	},
	{
		name:    "item/completed unknown type",
		message: notificationOf("item/completed", `{"threadId":"thread-1","item":{"type":"agentMessage","text":"hi"}}`),
	},
	{
		name:    "item/completed other thread",
		message: notificationOf("item/completed", `{"threadId":"other","item":{"type":"reasoning"}}`),
	},
	{
		name:    "turn/plan/updated",
		message: notificationOf("turn/plan/updated", `{"threadId":"thread-1","plan":[{"step":"inspect","status":"completed"},{"step":"patch","status":"pending"}]}`),
		want: []Event{EvPlanUpdated{Items: []PlanItem{
			{Text: "inspect", Status: "completed"},
			{Text: "patch", Status: "pending"},
		}}},
	},
	{
		name:    "thread/name/updated",
		message: notificationOf("thread/name/updated", `{"threadId":"thread-1","name":"release"}`),
		want:    []Event{EvSessionTitle{SessionID: "thread-1", Title: "release"}},
	},
	{
		name:    "thread/name/updated other thread",
		message: notificationOf("thread/name/updated", `{"threadId":"other","name":"release"}`),
	},
	{
		name:    "thread/tokenUsage/updated",
		message: notificationOf("thread/tokenUsage/updated", `{"threadId":"thread-1","tokenUsage":{"total":{"totalTokens":900,"inputTokens":500,"outputTokens":400,"cacheReadInputTokens":30,"cacheCreationInputTokens":7},"modelContextWindow":2000}}`),
		want: []Event{EvUsage{
			Used:      900,
			Size:      int64Ptr(2000),
			Breakdown: &UsageBreakdown{Input: 500, Output: 400, CacheRead: 30, CacheCreation: 7},
		}},
	},
	{
		name:    "thread/tokenUsage/updated cachedInputTokens fallback without window",
		message: notificationOf("thread/tokenUsage/updated", `{"threadId":"thread-1","tokenUsage":{"total":{"totalTokens":10,"inputTokens":6,"outputTokens":4,"cachedInputTokens":3}}}`),
		want: []Event{EvUsage{
			Used:      10,
			Breakdown: &UsageBreakdown{Input: 6, Output: 4, CacheRead: 3},
		}},
	},
	{
		name:    "turn/completed completed",
		message: notificationOf("turn/completed", `{"threadId":"thread-1","turn":{"id":"turn-1","status":"completed"}}`),
		want:    []Event{EvTurnCompleted{TurnID: "turn-1", Status: TurnCompleted, Label: "completed"}},
	},
	{
		name:    "turn/completed interrupted",
		message: notificationOf("turn/completed", `{"threadId":"thread-1","turn":{"id":"turn-1","status":"interrupted"}}`),
		want:    []Event{EvTurnCompleted{TurnID: "turn-1", Status: TurnInterrupted, Label: "interrupted"}},
	},
	{
		name:    "turn/completed failed",
		message: notificationOf("turn/completed", `{"threadId":"thread-1","turn":{"id":"turn-1","status":"failed"}}`),
		want:    []Event{EvTurnCompleted{TurnID: "turn-1", Status: TurnFailed, Label: "failed"}},
	},
	{
		name:    "turn/completed empty status",
		message: notificationOf("turn/completed", `{"threadId":"thread-1","turn":{"id":"turn-1"}}`),
		want:    []Event{EvTurnCompleted{TurnID: "turn-1", Status: TurnCompleted, Label: ""}},
	},
	{
		name:    "turn/completed other thread",
		message: notificationOf("turn/completed", `{"threadId":"other","turn":{"status":"completed"}}`),
	},
	{
		name:    "error",
		message: notificationOf("error", `{"message":"boom"}`),
		want:    []Event{EvNotice{Level: NoticeError, Text: "boom"}},
	},
	{
		name:    "error nested in TurnError",
		message: notificationOf("error", `{"error":{"message":"model not supported with ChatGPT account"},"threadId":"thread-1","turnId":"turn-1","willRetry":false}`),
		want:    []Event{EvNotice{Level: NoticeError, Text: "model not supported with ChatGPT account"}},
	},
	{
		name:    "error nested will retry",
		message: notificationOf("error", `{"error":{"message":"stream dropped"},"threadId":"thread-1","turnId":"turn-1","willRetry":true}`),
		want:    []Event{EvNotice{Level: NoticeWarn, Text: "stream dropped · retrying"}},
	},
	{
		name:    "error without message",
		message: notificationOf("error", `{"threadId":"thread-1"}`),
	},
	{
		name:    "turn/completed failed with error",
		message: notificationOf("turn/completed", `{"threadId":"thread-1","turn":{"id":"turn-1","status":"failed","error":{"message":"model not supported with ChatGPT account"}}}`),
		want:    []Event{EvTurnCompleted{TurnID: "turn-1", Status: TurnFailed, Label: "failed", Err: errors.New("model not supported with ChatGPT account")}},
	},
	{
		name:    "warning",
		message: notificationOf("warning", `{"message":"careful"}`),
		want:    []Event{EvNotice{Level: NoticeWarn, Text: "careful"}},
	},
	{
		name:    "configWarning",
		message: notificationOf("configWarning", `{"message":"Under-development features enabled: alpha, beta. extra"}`),
		want:    []Event{EvNotice{Level: NoticeWarn, Text: "Codex warning: unstable features active (alpha, beta)"}},
	},
	{
		name:    "guardianWarning",
		message: notificationOf("guardianWarning", `{"message":"guarded"}`),
		want:    []Event{EvNotice{Level: NoticeWarn, Text: "guarded"}},
	},
	{
		name:    "warning with empty message",
		message: notificationOf("warning", `{"message":""}`),
	},
	{
		name:    "unknown method",
		message: notificationOf("account/updated", `{}`),
	},
}

func TestCodexTranslatorGoldenTable(t *testing.T) {
	translator := codexTranslator{threadID: "thread-1"}
	for _, test := range codexGoldenCases {
		t.Run(test.name, func(t *testing.T) {
			got := translator.notification(test.message)
			if !reflect.DeepEqual(got, test.want) {
				t.Fatalf("notification(%s) = %#v, want %#v", test.message.Method, got, test.want)
			}
		})
	}
}

// 防止漏 case：translator 內每個 method / item type 字串都必須出現在 golden table。
func TestCodexTranslatorGoldenTableCoversEveryCase(t *testing.T) {
	file, err := parser.ParseFile(token.NewFileSet(), "backend_codex.go", nil, 0)
	if err != nil {
		t.Fatalf("parse backend_codex.go: %v", err)
	}
	covered := map[string]bool{}
	for _, test := range codexGoldenCases {
		covered[test.message.Method] = true
		var params struct {
			Item struct {
				Type string `json:"type"`
			} `json:"item"`
		}
		if json.Unmarshal(test.message.Params, &params) == nil && params.Item.Type != "" {
			covered[test.message.Method+"#"+params.Item.Type] = true
		}
	}
	for _, test := range codexRequestCases {
		covered[test.request.method] = true
	}

	missing := map[string]bool{}
	checked := 0
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || function.Body == nil {
			continue
		}
		switch function.Name.Name {
		case "notification", "request", "itemStarted", "itemCompleted":
		default:
			continue
		}
		method := ""
		switch function.Name.Name {
		case "itemStarted":
			method = "item/started"
		case "itemCompleted":
			method = "item/completed"
		}
		ast.Inspect(function.Body, func(node ast.Node) bool {
			clause, ok := node.(*ast.CaseClause)
			if !ok {
				return true
			}
			for _, expression := range clause.List {
				literal, ok := expression.(*ast.BasicLit)
				if !ok || literal.Kind != token.STRING {
					continue
				}
				value, _ := strconv.Unquote(literal.Value)
				checked++
				key := value
				if method != "" {
					key = method + "#" + value
				}
				if !covered[key] {
					missing[key] = true
				}
			}
			return true
		})
	}
	if checked < 15 {
		t.Fatalf("only %d translator cases found; coverage check is not scanning backend_codex.go", checked)
	}
	if len(missing) > 0 {
		keys := make([]string, 0, len(missing))
		for key := range missing {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		t.Fatalf("golden table does not cover translator cases: %v", keys)
	}
}

var codexRequestCases = []struct {
	name    string
	request serverRequest
	turnID  string
	want    codexRequest
}{
	{
		name: "commandExecution approval with decisions",
		request: serverRequest{
			id:     json.RawMessage("3"),
			method: "item/commandExecution/requestApproval",
			params: json.RawMessage(`{"threadId":"thread-1","turnId":"turn-1","command":"touch x","cwd":"/tmp/p","reason":"why","availableDecisions":["accept","decline"]}`),
		},
		want: codexRequest{event: EvApprovalRequest{
			ReqID: "3", TurnID: "turn-1", Title: "touch x", CWD: "/tmp/p", Reason: "why",
			Options: []ApprovalOption{
				{ID: "accept", Label: "accept", Kind: OptionAllowOnce},
				{ID: "decline", Label: "decline", Kind: OptionRejectOnce},
			},
		}},
	},
	{
		name: "commandExecution approval defaults to all decisions",
		request: serverRequest{
			id:     json.RawMessage("4"),
			method: "item/commandExecution/requestApproval",
			params: json.RawMessage(`{"threadId":"thread-1","command":"ls"}`),
		},
		want: codexRequest{event: EvApprovalRequest{
			ReqID: "4", Title: "ls",
			Options: []ApprovalOption{
				{ID: "accept", Label: "accept", Kind: OptionAllowOnce},
				{ID: "acceptForSession", Label: "acceptForSession", Kind: OptionAllowAlways},
				{ID: "decline", Label: "decline", Kind: OptionRejectOnce},
				{ID: "cancel", Label: "cancel", Kind: OptionCancel},
			},
		}},
	},
	{
		name: "commandExecution approval keeps unknown decisions so the card fails closed",
		request: serverRequest{
			id:     json.RawMessage("5"),
			method: "item/commandExecution/requestApproval",
			params: json.RawMessage(`{"threadId":"thread-1","command":"ls","availableDecisions":["applyNetworkPolicy"]}`),
		},
		want: codexRequest{event: EvApprovalRequest{
			ReqID: "5", Title: "ls",
			Options: []ApprovalOption{{ID: "applyNetworkPolicy", Label: "applyNetworkPolicy", Kind: OptionOther}},
		}},
	},
	{
		name: "fileChange approval",
		request: serverRequest{
			id:     json.RawMessage("6"),
			method: "item/fileChange/requestApproval",
			params: json.RawMessage(`{"threadId":"thread-1","turnId":"turn-1","reason":"write","availableDecisions":["accept","acceptForSession","decline","cancel"]}`),
		},
		want: codexRequest{event: EvApprovalRequest{
			ReqID: "6", TurnID: "turn-1", Title: "apply file changes", Reason: "write",
			Options: []ApprovalOption{
				{ID: "accept", Label: "accept", Kind: OptionAllowOnce},
				{ID: "acceptForSession", Label: "acceptForSession", Kind: OptionAllowAlways},
				{ID: "decline", Label: "decline", Kind: OptionRejectOnce},
				{ID: "cancel", Label: "cancel", Kind: OptionCancel},
			},
		}},
	},
	{
		name: "permissions approval is denied by the backend",
		request: serverRequest{
			id:     json.RawMessage("7"),
			method: "item/permissions/requestApproval",
			params: json.RawMessage(`{"threadId":"thread-1","turnId":"turn-1","permissions":{}}`),
		},
		want: codexRequest{denyPermissions: true},
	},
	{
		name: "user input request",
		request: serverRequest{
			id:     json.RawMessage("8"),
			method: "item/tool/requestUserInput",
			params: json.RawMessage(`{"threadId":"thread-1","turnId":"turn-1","questions":[{"header":"Mode","id":"mode","question":"Choose","options":[{"label":"A","description":"a"}],"isOther":true}]}`),
		},
		want: codexRequest{event: EvInputRequest{
			ReqID: "8", TurnID: "turn-1",
			Questions: []userInputQuestion{{Header: "Mode", ID: "mode", Question: "Choose", Options: []userInputOption{{Label: "A", Description: "a"}}, IsOther: true}},
		}},
	},
	{
		name: "user input request without questions is forwarded",
		request: serverRequest{
			id:     json.RawMessage("9"),
			method: "item/tool/requestUserInput",
			params: json.RawMessage(`{"threadId":"thread-1","questions":[]}`),
		},
		want: codexRequest{event: EvInputRequest{ReqID: "9", Questions: []userInputQuestion{}}},
	},
	{
		name: "user input question without id is rejected",
		request: serverRequest{
			id:     json.RawMessage("10"),
			method: "item/tool/requestUserInput",
			params: json.RawMessage(`{"threadId":"thread-1","questions":[{"question":"Choose"}]}`),
		},
		want: codexRequest{reject: "user-input question is missing id or question"},
	},
	{
		name: "invalid approval parameters",
		request: serverRequest{
			id:     json.RawMessage("11"),
			method: "item/commandExecution/requestApproval",
			params: json.RawMessage(`{`),
		},
		want: codexRequest{reject: "invalid approval parameters"},
	},
	{
		name: "invalid file-change approval parameters",
		request: serverRequest{
			id:     json.RawMessage("12"),
			method: "item/fileChange/requestApproval",
			params: json.RawMessage(`{`),
		},
		want: codexRequest{reject: "invalid file-change approval parameters"},
	},
	{
		name: "invalid permission parameters",
		request: serverRequest{
			id:     json.RawMessage("13"),
			method: "item/permissions/requestApproval",
			params: json.RawMessage(`{`),
		},
		want: codexRequest{reject: "invalid permission parameters"},
	},
	{
		name: "invalid user-input parameters",
		request: serverRequest{
			id:     json.RawMessage("14"),
			method: "item/tool/requestUserInput",
			params: json.RawMessage(`{`),
		},
		want: codexRequest{reject: "invalid user-input parameters"},
	},
	{
		name: "thread mismatch",
		request: serverRequest{
			id:     json.RawMessage("15"),
			method: "item/commandExecution/requestApproval",
			params: json.RawMessage(`{"threadId":"other-thread","turnId":"turn-1","command":"touch x"}`),
		},
		want: codexRequest{reject: "server request thread mismatch"},
	},
	{
		name: "missing thread id",
		request: serverRequest{
			id:     json.RawMessage("16"),
			method: "item/fileChange/requestApproval",
			params: json.RawMessage(`{"turnId":"turn-1"}`),
		},
		want: codexRequest{reject: "server request missing thread id"},
	},
	{
		name:   "turn mismatch",
		turnID: "turn-active",
		request: serverRequest{
			id:     json.RawMessage("17"),
			method: "item/permissions/requestApproval",
			params: json.RawMessage(`{"threadId":"thread-1","turnId":"turn-old"}`),
		},
		want: codexRequest{reject: "server request turn mismatch"},
	},
	{
		name: "unsupported request",
		request: serverRequest{
			id:     json.RawMessage("18"),
			method: "mcpServer/elicitation/request",
			params: json.RawMessage(`{}`),
		},
		want: codexRequest{reject: "unsupported server request: mcpServer/elicitation/request"},
	},
}

func TestCodexTranslatorRequests(t *testing.T) {
	for _, test := range codexRequestCases {
		t.Run(test.name, func(t *testing.T) {
			translator := codexTranslator{threadID: "thread-1", turnID: test.turnID}
			got := translator.request(test.request)
			if !reflect.DeepEqual(got, test.want) {
				t.Fatalf("request(%s) = %#v, want %#v", test.request.method, got, test.want)
			}
		})
	}
}
