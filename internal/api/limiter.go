package api

import (
	"sync"
	"time"
)

type bucket struct {
	tokens  float64
	updated time.Time
}
type limiter struct {
	mu     sync.Mutex
	global bucket
	peers  map[string]bucket
	now    func() time.Time
}

func newLimiter() *limiter {
	now := time.Now()
	return &limiter{global: bucket{20, now}, peers: make(map[string]bucket), now: time.Now}
}
func refill(b bucket, now time.Time, capacity, perSecond float64) bucket {
	b.tokens = min(capacity, b.tokens+max(0, now.Sub(b.updated).Seconds())*perSecond)
	b.updated = now
	return b
}
func (l *limiter) allow(peer string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	b, ok := l.peers[peer]
	if !ok {
		if len(l.peers) >= 4096 {
			for ip, v := range l.peers {
				if now.Sub(v.updated) > 10*time.Minute {
					delete(l.peers, ip)
				}
			}
		}
		if len(l.peers) >= 4096 {
			return false
		}
		b = bucket{10, now}
	}
	b = refill(b, now, 10, 10.0/60)
	l.global = refill(l.global, now, 20, 1)
	allowed := b.tokens >= 1 && l.global.tokens >= 1
	if allowed {
		b.tokens--
		l.global.tokens--
	}
	l.peers[peer] = b
	return allowed
}
