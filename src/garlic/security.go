package garlic

import "sync/atomic"

type atomicUint64 = atomic.Uint64

// SecurityCounters tracks local-only counts of why this node dropped an
// incoming Garlic circuit-data message, for operator visibility (e.g.
// via the dashboard). These are never transmitted to peers - the wire
// protocol still returns the same undifferentiated actionDrop behavior
// in every case (see protocol.go's processCircuitData doc comment on
// not leaking which check failed); only this node's own admin socket,
// reachable by the same locally-trusted audience that can already run
// yggdrasilctl, exposes the category breakdown. Cumulative since
// process start. The zero value is ready to use.
type SecurityCounters struct {
	replayDrops      atomicUint64
	malformedPackets atomicUint64
	expiredPackets   atomicUint64
	authFailures     atomicUint64
	relayTableFull   atomicUint64
}

// SecurityCounterSnapshot is a point-in-time copy of SecurityCounters,
// safe to serialize (used directly in the getGarlicStats admin
// response).
type SecurityCounterSnapshot struct {
	ReplayDrops      uint64
	MalformedPackets uint64
	ExpiredPackets   uint64
	AuthFailures     uint64
	RelayTableFull   uint64
}

func (s *SecurityCounters) snapshot() SecurityCounterSnapshot {
	return SecurityCounterSnapshot{
		ReplayDrops:      s.replayDrops.Load(),
		MalformedPackets: s.malformedPackets.Load(),
		ExpiredPackets:   s.expiredPackets.Load(),
		AuthFailures:     s.authFailures.Load(),
		RelayTableFull:   s.relayTableFull.Load(),
	}
}
