package protocol

import (
	"encoding/json"
	"strings"
	"sync"
	"testing"
)

func TestEventTypeValid(t *testing.T) {
	tests := []struct {
		typ   EventType
		valid bool
	}{
		{EventWorkspaceCreated, true},
		{EventWorkspaceStarted, true},
		{EventWorkspaceStopped, true},
		{EventProcessStarted, true},
		{EventProcessStdout, true},
		{EventProcessExited, true},
		{EventFileChanged, true},
		{EventSnapshotCreated, true},
		{EventNodeConnected, true},
		{EventNodeDisconnected, true},
		{"workspace.deleted", false},
		{"", false},
	}
	for _, tt := range tests {
		t.Run(string(tt.typ), func(t *testing.T) {
			if got := tt.typ.Valid(); got != tt.valid {
				t.Fatalf("Valid() = %v, want %v", got, tt.valid)
			}
		})
	}
}

func TestNewEvent(t *testing.T) {
	t.Run("valid with payload", func(t *testing.T) {
		ev, err := NewEvent(EventProcessExited, "w-1", map[string]int{"code": 0})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if ev.Type != EventProcessExited || ev.WorkspaceID != "w-1" {
			t.Fatalf("unexpected event: %+v", ev)
		}
		if ev.Timestamp.IsZero() {
			t.Fatalf("missing timestamp")
		}
		var p map[string]int
		if err := ev.DecodePayload(&p); err != nil || p["code"] != 0 {
			t.Fatalf("bad payload: %v %+v", err, p)
		}
	})
	t.Run("nil payload", func(t *testing.T) {
		ev, err := NewEvent(EventWorkspaceCreated, "w-1", nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(ev.Payload) != 0 {
			t.Fatalf("expected empty payload, got %s", ev.Payload)
		}
		if err := ev.DecodePayload(&struct{}{}); err == nil {
			t.Fatalf("expected error decoding empty payload")
		}
	})
	t.Run("unknown type", func(t *testing.T) {
		if _, err := NewEvent("bogus", "w-1", nil); err == nil {
			t.Fatalf("expected error, got nil")
		}
	})
	t.Run("empty workspace", func(t *testing.T) {
		if _, err := NewEvent(EventWorkspaceCreated, "  ", nil); err == nil {
			t.Fatalf("expected error, got nil")
		}
	})
	t.Run("node events are cluster-scoped", func(t *testing.T) {
		for _, typ := range []EventType{EventNodeConnected, EventNodeDisconnected} {
			ev, err := NewEvent(typ, "", map[string]string{"id": "node-1"})
			if err != nil {
				t.Fatalf("%s: unexpected error: %v", typ, err)
			}
			if ev.WorkspaceID != "" {
				t.Fatalf("%s: workspace id = %q, want empty", typ, ev.WorkspaceID)
			}
		}
	})
}

func TestEventEncodeDecode(t *testing.T) {
	ev, err := NewEvent(EventFileChanged, "w-1", map[string]string{"path": "/data/a.txt"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	raw, err := ev.Encode()
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	for _, key := range []string{`"type"`, `"workspace_id"`, `"timestamp"`, `"payload"`} {
		if !strings.Contains(string(raw), key) {
			t.Fatalf("missing key %s in %s", key, raw)
		}
	}
	back, err := DecodeEvent(raw)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if back.Type != ev.Type || back.WorkspaceID != ev.WorkspaceID || !back.Timestamp.Equal(ev.Timestamp) {
		t.Fatalf("round-trip mismatch: %+v vs %+v", back, ev)
	}
	if string(back.Payload) != string(ev.Payload) {
		t.Fatalf("payload mismatch: %s vs %s", back.Payload, ev.Payload)
	}
}

func TestDecodeEventErrors(t *testing.T) {
	tests := []struct {
		name string
		raw  string
	}{
		{"not json", "{"},
		{"unknown type", `{"type":"bogus","workspace_id":"w-1"}`},
		{"missing type", `{"workspace_id":"w-1"}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := DecodeEvent([]byte(tt.raw)); err == nil {
				t.Fatalf("expected error, got nil")
			}
		})
	}
}

func TestEventBusPublishSubscribe(t *testing.T) {
	bus := NewEventBus()
	var mu sync.Mutex
	var got []Event
	unsub := bus.Subscribe(EventWorkspaceStarted, func(e Event) {
		mu.Lock()
		defer mu.Unlock()
		got = append(got, e)
	})
	if bus.SubscriberCount(EventWorkspaceStarted) != 1 {
		t.Fatalf("expected 1 subscriber")
	}
	ev, _ := NewEvent(EventWorkspaceStarted, "w-1", nil)
	other, _ := NewEvent(EventWorkspaceStopped, "w-1", nil)
	bus.Publish(other)
	bus.Publish(ev)
	mu.Lock()
	n := len(got)
	mu.Unlock()
	if n != 1 {
		t.Fatalf("got %d events, want 1", n)
	}
	unsub()
	bus.Publish(ev)
	mu.Lock()
	n = len(got)
	mu.Unlock()
	if n != 1 {
		t.Fatalf("got %d events after unsubscribe, want 1", n)
	}
	if bus.SubscriberCount(EventWorkspaceStarted) != 0 {
		t.Fatalf("expected 0 subscribers after unsubscribe")
	}
}

func TestEventBusEdgeCases(t *testing.T) {
	bus := NewEventBus()
	// Unknown type and nil handler are safe no-ops.
	unsub := bus.Subscribe("bogus", func(Event) {})
	unsub()
	unsub2 := bus.Subscribe(EventFileChanged, nil)
	unsub2()
	if bus.SubscriberCount("bogus") != 0 || bus.SubscriberCount(EventFileChanged) != 0 {
		t.Fatalf("edge subscriptions should not register")
	}
	// Publishing with no subscribers, and on a nil bus, must not panic.
	ev, _ := NewEvent(EventFileChanged, "w-1", nil)
	bus.Publish(ev)
	var nilBus *EventBus
	nilBus.Publish(ev)
	if nilBus.SubscriberCount(EventFileChanged) != 0 {
		t.Fatalf("nil bus should report 0 subscribers")
	}
}

func TestEventBusConcurrent(t *testing.T) {
	bus := NewEventBus()
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			unsub := bus.Subscribe(EventProcessStdout, func(Event) {})
			defer unsub()
			ev, _ := NewEvent(EventProcessStdout, "w-1", json.RawMessage(`{}`))
			for j := 0; j < 50; j++ {
				bus.Publish(ev)
			}
		}()
	}
	wg.Wait()
}
