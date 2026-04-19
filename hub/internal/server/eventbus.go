package server

import "sync"

// eventBus is an in-process fan-out: handlePostEvent calls Publish after the
// DB insert commits, and each active SSE stream handler subscribes to events
// for one channel. This is the cheap path — no polling tail, no durable queue.
// When the process restarts, clients reconnect and replay via ?since=….
//
// The bus is non-blocking: if a subscriber's buffer is full we drop the
// message for that subscriber (they'll miss it, but they can always backfill
// via the list endpoint). That keeps one slow reader from stalling publishes.
type eventBus struct {
	mu   sync.RWMutex
	subs map[string]map[chan map[string]any]struct{} // channelID → set
}

func newEventBus() *eventBus {
	return &eventBus{subs: make(map[string]map[chan map[string]any]struct{})}
}

// Subscribe returns a channel that receives events for channelID.
// Callers must invoke Unsubscribe(channelID, ch) when done.
func (b *eventBus) Subscribe(channelID string) chan map[string]any {
	ch := make(chan map[string]any, 32)
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.subs[channelID] == nil {
		b.subs[channelID] = make(map[chan map[string]any]struct{})
	}
	b.subs[channelID][ch] = struct{}{}
	return ch
}

func (b *eventBus) Unsubscribe(channelID string, ch chan map[string]any) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if set := b.subs[channelID]; set != nil {
		delete(set, ch)
		if len(set) == 0 {
			delete(b.subs, channelID)
		}
	}
	close(ch)
}

func (b *eventBus) Publish(channelID string, evt map[string]any) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	for ch := range b.subs[channelID] {
		select {
		case ch <- evt:
		default:
			// drop — subscriber is behind; they can replay via ?since=
		}
	}
}
