package lockhub

import (
	"sync"
)

type Hub struct {
	mu        sync.Mutex
	resources map[string]*sync.Mutex
}

type Locker struct {
	mu sync.Mutex
}

type RWLocker struct {
	mu sync.RWMutex
}

func (l *Locker) Lock() {
	l.mu.Lock()
}

func (l *Locker) Unlock() {
	l.mu.Unlock()
}

func (l *RWLocker) Lock() {
	l.mu.Lock()
}

// TryLock reports whether the write lock was acquired without blocking.
func (l *RWLocker) TryLock() bool {
	return l.mu.TryLock()
}

func (l *RWLocker) Unlock() {
	l.mu.Unlock()
}

func (l *RWLocker) RLock() {
	l.mu.RLock()
}

func (l *RWLocker) RUnlock() {
	l.mu.RUnlock()
}

func New() *Hub {
	return &Hub{
		resources: make(map[string]*sync.Mutex),
	}
}

func (h *Hub) WithResource(resource, key, _ string, fn func() error) error {
	l := h.resourceLock(resource + ":" + key)
	l.Lock()
	defer l.Unlock()
	return fn()
}

func (h *Hub) resourceLock(name string) *sync.Mutex {
	h.mu.Lock()
	defer h.mu.Unlock()
	if l := h.resources[name]; l != nil {
		return l
	}
	l := &sync.Mutex{}
	h.resources[name] = l
	return l
}
