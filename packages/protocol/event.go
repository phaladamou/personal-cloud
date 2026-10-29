// Package protocol defines the event model exchanged between the
// control plane and the data plane, plus a small in-memory event bus
// for the workspace controller.
package protocol

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// EventType names a domain event.
type EventType string

// Typed event names emitted across Personal Cloud.
const (
	EventWorkspaceCreated EventType = "workspace.created"
	EventWorkspaceStarted EventType = "workspace.started"
	EventWorkspaceStopped EventType = "workspace.stopped"
	EventProcessStarted   EventType = "process.started"
	EventProcessStdout    EventType = "process.stdout"
	EventProcessExited    EventType = "process.exited"
	EventFileChanged      EventType = "file.changed"
	EventSnapshotCreated  EventType = "snapshot.created"
	EventNodeConnected    EventType = "node.connected"
	EventNodeDisconnected EventType = "node.disconnected"
)

// Valid reports whether t is a known event type.
func (t EventType) Valid() bool {
	switch t {
	case EventWorkspaceCreated, EventWorkspaceStarted, EventWorkspaceStopped,
		EventProcessStarted, EventProcessStdout, EventProcessExited,
		EventFileChanged, EventSnapshotCreated,
		EventNodeConnected, EventNodeDisconnected:
		return true
	default:
		return false
	}
}

// String returns the event type as a plain string.
func (t EventType) String() string {
	return string(t)
}

// Event is the envelope of every domain event. Payload carries the
// event-specific data as raw JSON.
type Event struct {
	Type        EventType       `json:"type"`
	WorkspaceID string          `json:"workspace_id"`
	Timestamp   time.Time       `json:"timestamp"`
	Payload     json.RawMessage `json:"payload,omitempty"`
}

// NewEvent builds a validated event. payload, when non-nil, is marshaled
// to JSON; a nil payload leaves the field empty.
func NewEvent(typ EventType, workspaceID string, payload any) (Event, error) {
	if !typ.Valid() {
		return Event{}, fmt.Errorf("protocol: unknown event type %q", typ)
	}
	if strings.TrimSpace(workspaceID) == "" {
		return Event{}, fmt.Errorf("protocol: empty workspace id")
	}
	ev := Event{
		Type:        typ,
		WorkspaceID: workspaceID,
		Timestamp:   time.Now().UTC().Truncate(time.Millisecond),
	}
	if payload != nil {
		raw, err := json.Marshal(payload)
		if err != nil {
			return Event{}, fmt.Errorf("protocol: marshal payload: %w", err)
		}
		ev.Payload = raw
	}
	return ev, nil
}

// Encode serializes the event to JSON.
func (e Event) Encode() ([]byte, error) {
	return json.Marshal(e)
}

// DecodeEvent parses a JSON-encoded event and validates its type.
func DecodeEvent(raw []byte) (Event, error) {
	var e Event
	if err := json.Unmarshal(raw, &e); err != nil {
		return Event{}, fmt.Errorf("protocol: decode event: %w", err)
	}
	if !e.Type.Valid() {
		return Event{}, fmt.Errorf("protocol: unknown event type %q", e.Type)
	}
	return e, nil
}

// DecodePayload unmarshals the event payload into v.
func (e Event) DecodePayload(v any) error {
	if len(e.Payload) == 0 {
		return fmt.Errorf("protocol: empty payload")
	}
	return json.Unmarshal(e.Payload, v)
}
