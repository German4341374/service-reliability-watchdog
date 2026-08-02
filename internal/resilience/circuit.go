package resilience

import (
	"sync"
	"time"
)

type circuit struct {
	failures int
	openedAt time.Time
	halfOpen bool
}

type CircuitBreaker struct {
	mu        sync.Mutex
	threshold int
	cooldown  time.Duration
	items     map[string]*circuit
}

func NewCircuitBreaker(threshold int, cooldown time.Duration) *CircuitBreaker {
	return &CircuitBreaker{
		threshold: threshold,
		cooldown:  cooldown,
		items:     make(map[string]*circuit),
	}
}

func (b *CircuitBreaker) Allow(key string, now time.Time) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	item := b.item(key)
	if item.openedAt.IsZero() {
		return true
	}
	if now.Sub(item.openedAt) < b.cooldown || item.halfOpen {
		return false
	}
	item.halfOpen = true
	return true
}

func (b *CircuitBreaker) Success(key string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.items, key)
}

func (b *CircuitBreaker) Failure(key string, now time.Time) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	item := b.item(key)
	item.halfOpen = false
	item.failures++
	if item.failures >= b.threshold {
		item.openedAt = now
		return true
	}
	return false
}

func (b *CircuitBreaker) IsOpen(key string, now time.Time) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	item, exists := b.items[key]
	return exists && !item.openedAt.IsZero() && now.Sub(item.openedAt) < b.cooldown
}

func (b *CircuitBreaker) item(key string) *circuit {
	item, exists := b.items[key]
	if !exists {
		item = &circuit{}
		b.items[key] = item
	}
	return item
}
