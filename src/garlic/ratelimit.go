package garlic

// Per-peer rate limiting (Phase 12 of the roadmap): a token-bucket per
// remote node key, bounding handshakes/packets per second so a single
// peer can't exhaust CPU by flooding capability requests or garlic
// packets. The number of tracked peers is itself bounded - once at
// capacity, a brand-new peer is denied (fails closed) rather than
// growing the bucket map without limit; Cleanup reclaims buckets for
// peers that have gone quiet.

import (
	"encoding/hex"
	"sync"
	"time"
)

type bucket struct {
	tokens     float64
	lastRefill time.Time
	lastSeen   time.Time
}

// RateLimiter is a per-peer token bucket rate limiter. It is safe for
// concurrent use.
type RateLimiter struct {
	mu              sync.Mutex
	ratePerSec      float64
	burst           float64
	maxTrackedPeers int
	buckets         map[string]*bucket
}

// NewRateLimiter returns a RateLimiter allowing burst requests
// immediately per peer, refilling at ratePerSec tokens/second, and
// tracking at most maxTrackedPeers distinct peers at once.
func NewRateLimiter(ratePerSec, burst float64, maxTrackedPeers int) *RateLimiter {
	return &RateLimiter{
		ratePerSec:      ratePerSec,
		burst:           burst,
		maxTrackedPeers: maxTrackedPeers,
		buckets:         make(map[string]*bucket),
	}
}

// Allow reports whether a request from peerKey should proceed right now,
// consuming one token if so.
func (r *RateLimiter) Allow(peerKey []byte) bool {
	key := hex.EncodeToString(peerKey)
	now := time.Now()

	r.mu.Lock()
	defer r.mu.Unlock()

	b, ok := r.buckets[key]
	if !ok {
		if len(r.buckets) >= r.maxTrackedPeers {
			return false
		}
		b = &bucket{tokens: r.burst, lastRefill: now}
		r.buckets[key] = b
	}

	elapsed := now.Sub(b.lastRefill).Seconds()
	b.tokens += elapsed * r.ratePerSec
	if b.tokens > r.burst {
		b.tokens = r.burst
	}
	b.lastRefill = now
	b.lastSeen = now

	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// Cleanup removes buckets for peers not seen within maxAge, returning how
// many were removed. Call periodically to bound memory for a long-running
// node that has talked to many peers over time.
func (r *RateLimiter) Cleanup(maxAge time.Duration) int {
	cutoff := time.Now().Add(-maxAge)

	r.mu.Lock()
	defer r.mu.Unlock()

	var stale []string
	for key, b := range r.buckets {
		if b.lastSeen.Before(cutoff) {
			stale = append(stale, key)
		}
	}
	for _, key := range stale {
		delete(r.buckets, key)
	}
	return len(stale)
}
