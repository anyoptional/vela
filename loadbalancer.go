package jinx

import (
	"context"
	"sync"
)

// LoadBalancer chooses one instance from the supplied candidates.
type LoadBalancer interface {
	Choose(ctx context.Context, serviceID string, candidates []Instance) (Instance, error)
}

type roundRobin struct {
	mu   sync.Mutex
	next map[string]int
}

func newRoundRobin() *roundRobin {
	return &roundRobin{next: make(map[string]int)}
}

func (r *roundRobin) Choose(_ context.Context, serviceID string, candidates []Instance) (Instance, error) {
	if len(candidates) == 0 {
		return Instance{}, ErrNoInstances
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	index := r.next[serviceID] % len(candidates)
	r.next[serviceID]++
	return candidates[index], nil
}
