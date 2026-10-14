package lumen

import (
	"strings"
	"testing"
	"time"
)

type approvalRecorder struct {
	fakeBackend
	resolved [][2]string
}

func (b *approvalRecorder) ResolveApproval(reqID, optionID string) error {
	b.resolved = append(b.resolved, [2]string{reqID, optionID})
	return nil
}

func explicitApproval() EvApprovalRequest {
	return EvApprovalRequest{
		ReqID: "0", Title: "$ rm x", Explicit: true,
		Options: []ApprovalOption{
			{ID: "allow-once", Label: "Allow once", Kind: OptionAllowOnce},
			{ID: "allow-always", Label: "Always allow", Kind: OptionAllowAlways},
			{ID: "reject", Label: "Reject", Kind: OptionRejectOnce},
		},
	}
}

// T-P2-21：explicit approval 沒有預設選項；Enter 不得放行，只有明確按鍵才回覆。
func TestExplicitApprovalHasNoDefaultFocus(t *testing.T) {
	backend := &approvalRecorder{}
	clock := &fakeClock{now: time.Unix(1000, 0)}
	frontend := &ui{screenMode: true, backend: backend, clock: clock.Now}
	frontend.handleEvent(explicitApproval())
	if frontend.approval == nil {
		t.Fatal("approval card not shown")
	}
	clock.now = clock.now.Add(time.Second)
	if err := frontend.handleKey(keyEvent{typ: keyEnter}); err != nil {
		t.Fatal(err)
	}
	if len(backend.resolved) != 0 || frontend.approval == nil {
		t.Fatalf("Enter resolved an explicit approval: %v", backend.resolved)
	}
	for _, key := range []rune{'a', 'y', 's'} {
		_ = frontend.handleKey(keyEvent{typ: keyRune, rune: key})
	}
	if len(backend.resolved) != 0 {
		t.Fatalf("letter keys resolved an explicit approval: %v", backend.resolved)
	}
	if hint := frontend.approval.hint(); !strings.Contains(hint, "[1] Allow once") || !strings.Contains(hint, "[3] Reject") {
		t.Fatalf("hint = %q", hint)
	}

	if err := frontend.handleKey(keyEvent{typ: keyRune, rune: '3'}); err != nil {
		t.Fatal(err)
	}
	if len(backend.resolved) != 1 || backend.resolved[0] != [2]string{"0", "reject"} || frontend.approval != nil {
		t.Fatalf("resolved = %v", backend.resolved)
	}
}

func TestExplicitApprovalEscapeCancels(t *testing.T) {
	backend := &approvalRecorder{}
	frontend := &ui{screenMode: true, backend: backend}
	frontend.handleEvent(explicitApproval())
	if err := frontend.handleKey(keyEvent{typ: keyEscape}); err != nil {
		t.Fatal(err)
	}
	if len(backend.resolved) != 1 || backend.resolved[0][1] != CancelApprovalID {
		t.Fatalf("resolved = %v", backend.resolved)
	}
}

func TestModeWarningStaysInStatusBarUntilCleared(t *testing.T) {
	frontend := &ui{screenMode: true, backend: &fakeBackend{}}
	frontend.handleEvent(EvModeWarning{Mode: "auto", Text: "permissions: auto (from agent settings)"})
	if label := frontend.screenStatusLabel(); !strings.Contains(label, "permissions: auto (from agent settings)") {
		t.Fatalf("status label = %q", label)
	}
	frontend.busy = true
	if label := frontend.screenStatusLabel(); !strings.Contains(label, "permissions: auto") {
		t.Fatalf("busy status label lost the warning: %q", label)
	}
	frontend.handleEvent(EvModeWarning{Mode: "default"})
	if label := frontend.screenStatusLabel(); strings.Contains(label, "permissions:") {
		t.Fatalf("warning not cleared: %q", label)
	}
}

// verifier follow-up：approval 逾時後 card 必須消失並顯示 timed-out 狀態，後續按鍵不得送出任何回覆。
func TestApprovalExpiredClearsCard(t *testing.T) {
	backend := &approvalRecorder{}
	frontend := &ui{screenMode: true, backend: backend}
	frontend.handleEvent(explicitApproval())
	frontend.handleEvent(EvApprovalExpired{ReqID: "0", Text: "permission request timed out · cancelled"})
	if frontend.approval != nil {
		t.Fatal("approval card still pending after timeout")
	}
	if plain := stripANSI(frontend.screenFrame(100, 24)); !strings.Contains(plain, "timed out") {
		t.Fatalf("no timed-out status row: %q", plain)
	}
	if err := frontend.handleKey(keyEvent{typ: keyRune, rune: '1'}); err != nil {
		t.Fatalf("key after timeout error = %v", err)
	}
	if len(backend.resolved) != 0 {
		t.Fatalf("key after timeout resolved an approval: %v", backend.resolved)
	}
}

// 逾時的是別張 card 時不得清掉目前的 card。
func TestApprovalExpiredIgnoresOtherRequest(t *testing.T) {
	frontend := &ui{screenMode: true, backend: &approvalRecorder{}}
	frontend.handleEvent(explicitApproval())
	frontend.handleEvent(EvApprovalExpired{ReqID: "7", Text: "permission request timed out · cancelled"})
	if frontend.approval == nil {
		t.Fatal("unrelated expiry cleared the current approval")
	}
}

type fakeClock struct{ now time.Time }

func (c *fakeClock) Now() time.Time { return c.now }

// verifier follow-up：card 出現後短時間內的數字鍵視為 typeahead，不得選 option；超過視窗後才生效。
func TestExplicitApprovalIgnoresEarlyDigits(t *testing.T) {
	backend := &approvalRecorder{}
	clock := &fakeClock{now: time.Unix(1000, 0)}
	frontend := &ui{screenMode: true, backend: backend, clock: clock.Now}
	frontend.handleEvent(explicitApproval())

	clock.now = clock.now.Add(approvalTypeaheadGuard - time.Millisecond)
	if err := frontend.handleKey(keyEvent{typ: keyRune, rune: '1'}); err != nil {
		t.Fatal(err)
	}
	if len(backend.resolved) != 0 || frontend.approval == nil {
		t.Fatalf("early digit selected an option: %v", backend.resolved)
	}

	clock.now = clock.now.Add(2 * time.Millisecond)
	if err := frontend.handleKey(keyEvent{typ: keyRune, rune: '1'}); err != nil {
		t.Fatal(err)
	}
	if len(backend.resolved) != 1 || backend.resolved[0] != [2]string{"0", "allow-once"} {
		t.Fatalf("digit after the guard window did not select: %v", backend.resolved)
	}
}

// 取消是安全方向，不受 typeahead 視窗限制。
func TestExplicitApprovalCancelNotGuarded(t *testing.T) {
	backend := &approvalRecorder{}
	clock := &fakeClock{now: time.Unix(1000, 0)}
	frontend := &ui{screenMode: true, backend: backend, clock: clock.Now}
	frontend.handleEvent(explicitApproval())
	if err := frontend.handleKey(keyEvent{typ: keyEscape}); err != nil {
		t.Fatal(err)
	}
	if len(backend.resolved) != 1 || backend.resolved[0][1] != CancelApprovalID {
		t.Fatalf("resolved = %v", backend.resolved)
	}
}

// T-P2-12：reconnect 退回新 session 時附 notice，讓使用者知道 agent context 已遺失。
func TestReconnectFreshSessionShowsNotice(t *testing.T) {
	for _, fresh := range []bool{true, false} {
		frontend := &ui{screenMode: true, cwd: "/tmp", thread: threadSummary{ID: "old"}}
		frontend.adoptReconnectResult(reconnectResult{backend: &fakeBackend{}, thread: threadSummary{ID: "new"}, freshSession: fresh})
		plain := stripANSI(frontend.screenFrame(120, 30))
		if got := strings.Contains(plain, "agent context lost · started a new session"); got != fresh {
			t.Fatalf("fresh=%v notice shown=%v: %q", fresh, got, plain)
		}
		if !strings.Contains(plain, "reconnected") {
			t.Fatalf("reconnected status missing: %q", plain)
		}
	}
}
