package jinx

import (
	"context"
	"net/http"
	"sync"
	"time"
)

type transport func(ctx context.Context, req *http.Request) (*http.Response, error)

type insKey struct {
	secure bool
	host   string
	port   int
}

type cooldownTracker struct {
	mu       sync.Mutex
	until    map[insKey]time.Time
	now      func() time.Time
	duration time.Duration
}

func newCooldownTracker(duration time.Duration) cooldownTracker {
	return cooldownTracker{
		until:    make(map[insKey]time.Time),
		now:      time.Now,
		duration: duration,
	}
}

func (c *cooldownTracker) mark(instance Instance) {
	now := c.now()
	c.mu.Lock()
	defer c.mu.Unlock()
	c.prune(now)
	if c.duration > 0 {
		c.until[keyFor(instance)] = now.Add(c.duration)
	}
}

func (c *cooldownTracker) isCooling(instance Instance) bool {
	now := c.now()
	c.mu.Lock()
	defer c.mu.Unlock()

	key := keyFor(instance)
	until, ok := c.until[key]
	if !ok {
		return false
	}
	if !until.After(now) {
		delete(c.until, key)
		return false
	}
	return true
}

func (c *cooldownTracker) len() int {
	now := c.now()
	c.mu.Lock()
	defer c.mu.Unlock()
	c.prune(now)
	return len(c.until)
}

func (c *cooldownTracker) prune(now time.Time) {
	for key, until := range c.until {
		if !until.After(now) {
			delete(c.until, key)
		}
	}
}

func keyFor(instance Instance) insKey {
	return insKey{
		secure: instance.Secure,
		host:   instance.Host,
		port:   instance.Port,
	}
}
