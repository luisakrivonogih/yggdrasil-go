package garlic

// Jitter: a bounded, delayed-send scheduler used to add random delay
// before actually transmitting a circuitData packet (origin send or
// relay forward), so an observer can't line up exact send timestamps
// across hops - a defense against the timing half of "traffic
// correlation" (docs/garlic-threat-model.md), complementing the
// per-packet size randomization in envelope.go/protocol.go.
//
// It must never block its caller: Garlic.handleIncoming's forwarding
// path calls enqueue synchronously from within core.Core.ReadFrom's read
// loop (see core.GarlicHandler's doc comment on why that must not
// block), so this is a fixed-size worker pool pulling from a bounded
// channel - enqueue either succeeds immediately or fails immediately
// (queue full), never waits.

import (
	"crypto/rand"
	"errors"
	"math/big"
	"net"
	"time"
)

var ErrInvalidJitterRange = errors.New("garlic: invalid jitter delay range")

type jitterJob struct {
	data   []byte
	addr   net.Addr
	sendAt time.Time
}

// jitterScheduler delays calls to send by a caller-specified duration,
// bounded by a fixed-capacity queue and a fixed worker pool - so a burst
// of enqueues can never grow memory or goroutine count without limit.
type jitterScheduler struct {
	send func(data []byte, addr net.Addr) error
	jobs chan jitterJob
	stop chan struct{}
}

// newJitterScheduler starts a scheduler backed by workers goroutines
// pulling from a queue of capacity queueSize. workers may be 0 (nothing
// is ever sent; useful for testing enqueue's bounded-capacity behavior
// in isolation).
func newJitterScheduler(send func(data []byte, addr net.Addr) error, queueSize, workers int) *jitterScheduler {
	s := &jitterScheduler{
		send: send,
		jobs: make(chan jitterJob, queueSize),
		stop: make(chan struct{}),
	}
	for range workers {
		go s.worker()
	}
	return s
}

func (s *jitterScheduler) worker() {
	for {
		select {
		case job := <-s.jobs:
			if d := time.Until(job.sendAt); d > 0 {
				select {
				case <-time.After(d):
				case <-s.stop:
					return
				}
			}
			_ = s.send(job.data, job.addr)
		case <-s.stop:
			return
		}
	}
}

// enqueue schedules data to be sent to addr after delay and returns
// immediately. It returns false, without sending, if the queue is at
// capacity or Stop has already been called - never blocks and never
// grows the queue unboundedly.
//
// Checking s.stop here (in addition to workers doing the same) closes a
// race that would otherwise exist purely from closing the channel:
// Go's select picks pseudo-randomly among ready cases, so a worker whose
// select happens to run after both the queue has a job *and* s.stop has
// been closed could still pick the job case. Rejecting new enqueues once
// Stop has been observed here avoids that for the common sequential
// pattern (Stop, then no further enqueue calls from that goroutine).
func (s *jitterScheduler) enqueue(data []byte, addr net.Addr, delay time.Duration) bool {
	select {
	case <-s.stop:
		return false
	default:
	}
	select {
	case s.jobs <- jitterJob{data: data, addr: addr, sendAt: time.Now().Add(delay)}:
		return true
	default:
		return false
	}
}

// Stop halts all workers. Jobs still queued are dropped, not sent.
func (s *jitterScheduler) Stop() {
	close(s.stop)
}

// randomJitter returns a uniformly random duration in [minDelay,
// maxDelay].
func randomJitter(minDelay, maxDelay time.Duration) (time.Duration, error) {
	if maxDelay < minDelay {
		return 0, ErrInvalidJitterRange
	}
	if maxDelay == minDelay {
		return minDelay, nil
	}
	span := big.NewInt(int64(maxDelay-minDelay) + 1)
	n, err := rand.Int(rand.Reader, span)
	if err != nil {
		return 0, err
	}
	return minDelay + time.Duration(n.Int64()), nil
}
