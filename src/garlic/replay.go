package garlic

// Replay protection (Phase 5/16 of the roadmap): a fixed-size sliding
// bitmap window keyed by Envelope.PacketCounter, the standard IPsec/
// WireGuard-style anti-replay construction. Memory use is bounded
// regardless of how far or how erratically an attacker drives the
// counter - there is no per-counter map entry that could be grown
// without bound.

import "sync"

// replayWindowBits is the width of the sliding window: a counter more
// than this far behind the highest one accepted so far is rejected
// outright as too old, rather than remembered.
const replayWindowBits = 2048

const replayWindowBytes = replayWindowBits / 8

// maxReplayWindowMemoryBytes bounds a single ReplayWindow's footprint,
// used by tests to confirm the implementation never grows unbounded.
const maxReplayWindowMemoryBytes = replayWindowBytes + 64

// ReplayWindow rejects a previously-seen or too-old packet counter. It is
// safe for concurrent use.
type ReplayWindow struct {
	mu          sync.Mutex
	initialized bool
	highest     uint64
	bitmap      [replayWindowBytes]byte
}

// NewReplayWindow returns an empty replay window.
func NewReplayWindow() *ReplayWindow {
	return &ReplayWindow{}
}

// CheckAndSet reports whether counter is fresh (neither a replay of an
// already-seen counter nor older than the sliding window), and if so,
// marks it seen. It returns false for both a replay and a too-old value,
// deliberately not distinguishing the two - see docs/garlic-architecture.md
// §17 on not leaking which check failed.
func (w *ReplayWindow) CheckAndSet(counter uint64) bool {
	w.mu.Lock()
	defer w.mu.Unlock()

	if !w.initialized {
		w.initialized = true
		w.highest = counter
		w.setBit(0)
		return true
	}

	if counter > w.highest {
		w.shift(counter - w.highest)
		w.highest = counter
		w.setBit(0)
		return true
	}

	diff := w.highest - counter
	if diff >= replayWindowBits {
		return false
	}
	if w.testBit(diff) {
		return false
	}
	w.setBit(diff)
	return true
}

// shift advances the window by n positions (a new higher counter was
// seen), dropping bits that fall out of the window and clearing the
// newly-in-range low bits.
func (w *ReplayWindow) shift(n uint64) {
	if n >= replayWindowBits {
		w.bitmap = [replayWindowBytes]byte{}
		return
	}
	byteShift := n / 8
	bitShift := n % 8

	if byteShift > 0 {
		copy(w.bitmap[byteShift:], w.bitmap[:replayWindowBytes-byteShift])
		for i := range byteShift {
			w.bitmap[i] = 0
		}
	}
	if bitShift > 0 {
		var carry byte
		for i := int(byteShift); i < replayWindowBytes; i++ {
			b := w.bitmap[i]
			w.bitmap[i] = (b << bitShift) | carry
			carry = b >> (8 - bitShift)
		}
	}
}

func (w *ReplayWindow) setBit(pos uint64) {
	w.bitmap[pos/8] |= 1 << (pos % 8)
}

func (w *ReplayWindow) testBit(pos uint64) bool {
	return w.bitmap[pos/8]&(1<<(pos%8)) != 0
}

// memoryBytes reports the window's fixed memory footprint, for tests.
func (w *ReplayWindow) memoryBytes() int {
	return len(w.bitmap)
}
