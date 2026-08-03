package localserver

import (
	"errors"
	"sync"
)

// ErrClientLimit reports that the bounded SSE broker cannot accept a client.
var ErrClientLimit = errors.New("localserver: SSE client limit reached")

type revisionBroker struct {
	mu      sync.Mutex
	clients map[uint64]chan string
	nextID  uint64
	limit   int
	closed  bool
}

func newRevisionBroker(limit int) *revisionBroker {
	return &revisionBroker{clients: make(map[uint64]chan string), limit: limit}
}

func (b *revisionBroker) register() (uint64, <-chan string, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed || len(b.clients) >= b.limit {
		return 0, nil, ErrClientLimit
	}
	b.nextID++
	channel := make(chan string, 1)
	b.clients[b.nextID] = channel
	return b.nextID, channel, nil
}

func (b *revisionBroker) deregister(id uint64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if channel, ok := b.clients[id]; ok {
		delete(b.clients, id)
		close(channel)
	}
}

func (b *revisionBroker) publish(revision string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return
	}
	for _, channel := range b.clients {
		select {
		case channel <- revision:
		default:
			select {
			case <-channel:
			default:
			}
			channel <- revision
		}
	}
}

func (b *revisionBroker) close() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return
	}
	b.closed = true
	for id, channel := range b.clients {
		delete(b.clients, id)
		close(channel)
	}
}

func (b *revisionBroker) count() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.clients)
}
