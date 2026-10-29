package protocol

import "sync"

// Handler receives an event published on the bus.
type Handler func(Event)

// EventBus is a small in-memory, thread-safe publish/subscribe bus for
// controller use. Handlers run synchronously in the publisher's goroutine.
type EventBus struct {
	mu   sync.RWMutex
	subs map[EventType]map[uint64]Handler
	next uint64
}

// NewEventBus builds an empty event bus.
func NewEventBus() *EventBus {
	return &EventBus{subs: make(map[EventType]map[uint64]Handler)}
}

// Subscribe registers h for events of the given type and returns an
// unsubscribe function. Subscribing to an unknown type is a no-op that
// returns a no-op unsubscribe function.
func (b *EventBus) Subscribe(typ EventType, h Handler) (unsubscribe func()) {
	noop := func() {}
	if b == nil || h == nil || !typ.Valid() {
		return noop
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.subs[typ] == nil {
		b.subs[typ] = make(map[uint64]Handler)
	}
	id := b.next
	b.next++
	b.subs[typ][id] = h
	return func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		delete(b.subs[typ], id)
	}
}

// Publish delivers e to every handler subscribed to its type.
// Handlers run synchronously; the subscriber list is snapshotted so a
// handler may safely subscribe or unsubscribe during dispatch.
func (b *EventBus) Publish(e Event) {
	if b == nil {
		return
	}
	b.mu.RLock()
	handlers := make([]Handler, 0, len(b.subs[e.Type]))
	for _, h := range b.subs[e.Type] {
		handlers = append(handlers, h)
	}
	b.mu.RUnlock()
	for _, h := range handlers {
		h(e)
	}
}

// SubscriberCount returns the number of handlers subscribed to typ.
func (b *EventBus) SubscriberCount(typ EventType) int {
	if b == nil {
		return 0
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	return len(b.subs[typ])
}
