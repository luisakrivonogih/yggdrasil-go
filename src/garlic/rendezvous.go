package garlic

// Rendezvous abstraction (Phase 9 of the roadmap, see
// docs/garlic-architecture.md §3.9): endpoint discovery decoupled from
// circuit construction, so circuits can be built and tested against a
// StaticRendezvous without any distributed directory. A DHT-backed
// implementation is future work behind the same interface.

import (
	"errors"
	"sync"
	"time"
)

// MaxIntroPoints bounds how many introduction points a single
// publication may list, so a remote publisher can't make a Rendezvous
// implementation store unbounded per-GID state.
const MaxIntroPoints = 16

var (
	ErrGIDNotFound        = errors.New("garlic: GID not found")
	ErrTooManyIntroPoints = errors.New("garlic: too many introduction points")
)

// IntroPoint is one introduction point for a Garlic service: a
// Garlic-capable node willing to forward circuit-extension requests to
// the service on its behalf, without itself being the service's
// Yggdrasil address.
type IntroPoint struct {
	NodeKey []byte
}

// Rendezvous maps Garlic Service IDs (GID) to their current introduction
// points.
type Rendezvous interface {
	// Publish advertises points as the introduction points for gid, valid
	// for ttl. A later Publish for the same gid replaces the previous
	// publication.
	Publish(gid GID, points []IntroPoint, ttl time.Duration) error
	// Lookup returns the currently-published introduction points for gid,
	// or an error if none are published or the publication has expired.
	Lookup(gid GID) ([]IntroPoint, error)
}

type staticEntry struct {
	points    []IntroPoint
	expiresAt time.Time
}

// StaticRendezvous is an in-memory Rendezvous implementation, suitable
// for local testing and small statically-configured deployments
// independent of any distributed directory. It is safe for concurrent
// use.
type StaticRendezvous struct {
	mu      sync.Mutex
	entries map[GID]staticEntry
}

// NewStaticRendezvous returns an empty StaticRendezvous.
func NewStaticRendezvous() *StaticRendezvous {
	return &StaticRendezvous{entries: make(map[GID]staticEntry)}
}

func (s *StaticRendezvous) Publish(gid GID, points []IntroPoint, ttl time.Duration) error {
	if len(points) > MaxIntroPoints {
		return ErrTooManyIntroPoints
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries[gid] = staticEntry{
		points:    append([]IntroPoint(nil), points...),
		expiresAt: time.Now().Add(ttl),
	}
	return nil
}

func (s *StaticRendezvous) Lookup(gid GID) ([]IntroPoint, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.entries[gid]
	if !ok || time.Now().After(e.expiresAt) {
		return nil, ErrGIDNotFound
	}
	return append([]IntroPoint(nil), e.points...), nil
}
