package garlic

// Regression test for the final-review finding that AutoCreateCircuit's
// selection-time verification loop checked SupportsAutoCircuit() but not
// SupportsGarlicV3(), so a mixed mesh containing a peer that only
// advertises garlic-v2 (not yet upgraded to the hop-local envelope
// format) would sail past that loop, burn a QueryCapability round trip,
// and only then fail one layer deeper inside CreateCircuit's own gate -
// with fillAutoPool (the auto-pool background loop) silently discarding
// the resulting error. See manager.go's AutoCreateCircuit doc comment
// and ErrHopMissingGarlicV3Support.
//
// This needs a real, linked two-node mesh: AutoCreateCircuit's own
// candidatePool()/HopCount() read core.Core.GetTree()/GetPaths(), which
// only a running ironwood instance populates - a bare *Garlic fixture
// (as relay_logic_test.go's newTestGarlic uses for pure decision-logic
// tests) has no core.Core at all and would panic. This is the same
// fixture shape as integration_test.go's
// TestIntegrationAutoCreateCircuitUsesSelfVerifiedGuard - duplicated
// here (rather than reused directly) because this test needs to reach
// into the unexported capabilityCache to simulate a peer whose already-
// cached capability answer predates a garlic-v3 upgrade, which is only
// possible from inside package garlic (integration_test.go is
// package garlic_test).

import (
	"encoding/hex"
	"errors"
	"io"
	"net/url"
	"testing"
	"time"

	"github.com/gologme/log"

	"github.com/yggdrasil-network/yggdrasil-go/src/config"
	"github.com/yggdrasil-network/yggdrasil-go/src/core"
)

func newLinkedCoreForAutoCircuitTest(t *testing.T) *core.Core {
	t.Helper()
	cfg := config.GenerateConfig()
	if err := cfg.GenerateSelfSignedCertificate(); err != nil {
		t.Fatalf("GenerateSelfSignedCertificate returned error: %v", err)
	}
	logger := log.New(io.Discard, "", 0)
	c, err := core.New(cfg.Certificate, logger)
	if err != nil {
		t.Fatalf("core.New returned error: %v", err)
	}
	return c
}

func TestAutoCreateCircuitRejectsCandidateMissingGarlicV3(t *testing.T) {
	origin := newLinkedCoreForAutoCircuitTest(t)
	defer origin.Stop()
	candidate := newLinkedCoreForAutoCircuitTest(t)
	defer candidate.Stop()

	listenURL, err := url.Parse("tcp://localhost:0")
	if err != nil {
		t.Fatal(err)
	}
	listener, err := origin.Listen(listenURL, "")
	if err != nil {
		t.Fatalf("Listen returned error: %v", err)
	}
	peerURL, err := url.Parse("tcp://" + listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	if err := candidate.CallPeer(peerURL, ""); err != nil {
		t.Fatalf("CallPeer returned error: %v", err)
	}

	for _, n := range []*core.Core{origin, candidate} {
		go func(n *core.Core) {
			buf := make([]byte, 65535)
			for {
				if _, _, err := n.ReadFrom(buf); err != nil {
					return
				}
			}
		}(n)
	}

	idOrigin, err := NewIdentity()
	if err != nil {
		t.Fatalf("NewIdentity (origin) returned error: %v", err)
	}
	idCandidate, err := NewIdentity()
	if err != nil {
		t.Fatalf("NewIdentity (candidate) returned error: %v", err)
	}

	cfg := DefaultConfig()
	cfg.CapabilityTimeout = 2 * time.Second
	cfg.MinHopCount = 0 // this tiny two-node topology has no room for a real distance filter

	gOrigin := New(origin, idOrigin, cfg, NewStaticRendezvous())
	defer gOrigin.Close()
	gCandidate := New(candidate, idCandidate, cfg, NewStaticRendezvous())
	defer gCandidate.Close()

	// A real capability round trip both records the candidate as
	// self-verified (required by SelectPathWithGuardPolicy) and resolves
	// the mesh path HopCount()/candidatePool() need - see
	// bootstrapMaxAttempts's doc comment for why the very first query can
	// race path discovery and needs a couple of retries.
	var verified bool
	for attempt := 0; attempt < bootstrapMaxAttempts; attempt++ {
		if _, err := gOrigin.QueryCapability(candidate.PublicKey()); err == nil {
			verified = true
			break
		}
	}
	if !verified {
		t.Fatal("capability query against candidate never succeeded")
	}

	// Now simulate the mixed-mesh scenario: the candidate's cached
	// capability answer advertises CapabilityAutoCircuit (so the
	// pre-existing SupportsAutoCircuit() gate alone would NOT catch this)
	// but not CapabilityGarlicV3 - exactly the "upgraded for auto-circuit
	// but not yet for the hop-local envelope format" gap the new check
	// exists to close.
	key := hex.EncodeToString(candidate.PublicKey())
	gOrigin.mu.Lock()
	gOrigin.capabilityCache[key] = &CapabilityMessage{
		Versions:  []string{CapabilityGarlicV2, CapabilityAutoCircuit},
		PublicKey: idCandidate.PublicKey,
	}
	gOrigin.mu.Unlock()

	before := len(gOrigin.OriginatedCircuits())
	_, err = gOrigin.AutoCreateCircuit(1)
	if !errors.Is(err, ErrHopMissingGarlicV3Support) {
		t.Fatalf("AutoCreateCircuit error = %v, want ErrHopMissingGarlicV3Support", err)
	}
	if after := len(gOrigin.OriginatedCircuits()); after != before {
		t.Fatalf("OriginatedCircuits count = %d, want unchanged %d (a rejected candidate must not leave a circuit behind)", after, before)
	}
}
