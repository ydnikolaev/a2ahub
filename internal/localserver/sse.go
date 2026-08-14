package localserver

import (
	"errors"
	"sync"
)

// ErrClientLimit reports that the bounded SSE broker cannot accept a client.
var ErrClientLimit = errors.New("localserver: SSE client limit reached")

type revisionBroker struct {
	mu      sync.Mutex
	clients map[uint64]chan dashboardVersion
	nextID  uint64
	limit   int
	closed  bool
}

func newRevisionBroker(limit int) *revisionBroker {
	return &revisionBroker{clients: make(map[uint64]chan dashboardVersion), limit: limit}
}

type dashboardVersion struct {
	revision        string
	viewModelDigest string
}

func (b *revisionBroker) register() (uint64, <-chan dashboardVersion, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed || len(b.clients) >= b.limit {
		return 0, nil, ErrClientLimit
	}
	b.nextID++
	channel := make(chan dashboardVersion, 1)
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

func (b *revisionBroker) publish(version dashboardVersion) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return
	}
	for _, channel := range b.clients {
		select {
		case channel <- version:
		default:
			select {
			case <-channel:
			default:
			}
			channel <- version
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
