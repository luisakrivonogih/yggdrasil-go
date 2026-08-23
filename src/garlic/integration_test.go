package garlic_test

// Integration test (Phase 13 of the roadmap): a real multi-node
// Yggdrasil mesh, in-process, proving the whole pipeline end-to-end -
// not just each piece in isolation. Topology:
//
//	A (garlic origin) -- L1 (legacy) -- L2 (legacy) -- R (garlic relay) -- B (garlic destination)
//
// A and R are never directly peered; every packet between them - the
// capability negotiation, and every hop of the onion circuit - must
// transit L1 and L2, which never call garlic.New and have no idea
// Garlic exists. This is the direct, running proof of the core claim in
// docs/garlic-architecture.md §4: legacy nodes transparently carry
// Garlic traffic as ordinary encrypted mesh frames, requiring no
// changes and gaining no visibility into it.

import (
	"bytes"
	"crypto/ed25519"
	"encoding/hex"
	"errors"
	"io"
	"net/url"
	"testing"
	"time"

	"github.com/gologme/log"

	"github.com/yggdrasil-network/yggdrasil-go/src/config"
	"github.com/yggdrasil-network/yggdrasil-go/src/core"
	"github.com/yggdrasil-network/yggdrasil-go/src/garlic"
)

func newLinkedTestNode(t *testing.T) *core.Core {
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

// connectChain peers nodes[i] <- nodes[i+1] for consecutive pairs only,
// so any two non-adjacent nodes can only reach each other by transiting
// the ones between them.
func connectChain(t *testing.T, nodes []*core.Core) {
	t.Helper()
	for i := 0; i < len(nodes)-1; i++ {
		listenURL, err := url.Parse("tcp://localhost:0")
		if err != nil {
			t.Fatal(err)
		}
		listener, err := nodes[i].Listen(listenURL, "")
		if err != nil {
			t.Fatalf("Listen returned error: %v", err)
		}
		peerURL, err := url.Parse("tcp://" + listener.Addr().String())
		if err != nil {
			t.Fatal(err)
		}
		if err := nodes[i+1].CallPeer(peerURL, ""); err != nil {
			t.Fatalf("CallPeer returned error: %v", err)
		}
	}
}

// pumpAll drives every node's ReadFrom loop, exactly as tun.queue()/
// tun.read() does in the real daemon (see docs/garlic-architecture.md
// §3.3) - without this, none of these nodes' underlying encrypted
// sessions with each other would ever be serviced, Garlic or otherwise.
func pumpAll(nodes []*core.Core) {
	for _, n := range nodes {
		go func(n *core.Core) {
			buf := make([]byte, 65535)
			for {
				if _, _, err := n.ReadFrom(buf); err != nil {
					return
				}
			}
		}(n)
	}
}

func waitForCapability(t *testing.T, g *garlic.Garlic, peer ed25519.PublicKey, maxWait time.Duration) *garlic.CapabilityMessage {
	t.Helper()
	deadline := time.Now().Add(maxWait)
	var lastErr error
	for time.Now().Before(deadline) {
		msg, err := g.QueryCapability(peer)
		if err == nil {
			return msg
		}
		lastErr = err
	}
	t.Fatalf("capability query never succeeded within %s: %v", maxWait, lastErr)
	return nil
}

func TestIntegrationSendGarlicThroughLegacyRelay(t *testing.T) {
	nodeA := newLinkedTestNode(t)
	nodeL1 := newLinkedTestNode(t)
	nodeL2 := newLinkedTestNode(t)
	nodeR := newLinkedTestNode(t)
	nodeB := newLinkedTestNode(t)
	all := []*core.Core{nodeA, nodeL1, nodeL2, nodeR, nodeB}
	for _, n := range all {
		defer n.Stop()
	}

	connectChain(t, all)
	pumpAll(all)

	idA, err := garlic.NewIdentity()
	if err != nil {
		t.Fatalf("NewIdentity (A) returned error: %v", err)
	}
	idR, err := garlic.NewIdentity()
	if err != nil {
		t.Fatalf("NewIdentity (R) returned error: %v", err)
	}
	idB, err := garlic.NewIdentity()
	if err != nil {
		t.Fatalf("NewIdentity (B) returned error: %v", err)
	}

	cfg := garlic.DefaultConfig()
	cfg.CapabilityTimeout = 2 * time.Second

	gA := garlic.New(nodeA, idA, cfg, garlic.NewStaticRendezvous())
	defer gA.Close()
	// nodeL1 and nodeL2 deliberately get no garlic.New call: they are
	// plain, unmodified Yggdrasil nodes.
	gR := garlic.New(nodeR, idR, cfg, garlic.NewStaticRendezvous())
	defer gR.Close()
	gB := garlic.New(nodeB, idB, cfg, garlic.NewStaticRendezvous())
	defer gB.Close()

	// A malformed garlic packet, sent before anything real, must not
	// crash or wedge the receiving node - confirmed by everything below
	// still working.
	if _, err := nodeA.WriteGarlic([]byte{0xFF, 0x00, 0x01, 0x02}, nodeR.LocalAddr()); err != nil {
		t.Fatalf("WriteGarlic (malformed) returned error: %v", err)
	}

	// A legacy node must never answer a capability request: it has no
	// handler registered, so core.Core silently drops the request, and
	// the querying side must see this as an ordinary timeout - the same
	// signal it gets for any other non-Garlic node.
	if _, err := gA.QueryCapability(nodeL1.PublicKey()); err == nil {
		t.Fatal("QueryCapability against a legacy node succeeded, want a timeout")
	}

	capR := waitForCapability(t, gA, nodeR.PublicKey(), 180*time.Second)
	if !capR.SupportsGarlicV2() {
		t.Fatal("R's capability response does not advertise garlic-v2")
	}
	if !bytes.Equal(capR.PublicKey, idR.PublicKey) {
		t.Fatalf("R's advertised public key = %x, want %x", capR.PublicKey, idR.PublicKey)
	}
	capB := waitForCapability(t, gA, nodeB.PublicKey(), 180*time.Second)
	if !capB.SupportsGarlicV2() {
		t.Fatal("B's capability response does not advertise garlic-v2")
	}

	circuitID, err := gA.CreateCircuit(
		[]garlic.CapabilityMessage{*capR, *capB},
		[][]byte{nodeR.PublicKey(), nodeB.PublicKey()},
	)
	if err != nil {
		t.Fatalf("CreateCircuit returned error: %v", err)
	}

	payload := []byte("hello bob, from alice, via garlic, through two legacy hops")
	if err := gA.SendGarlic(circuitID, payload); err != nil {
		t.Fatalf("SendGarlic returned error: %v", err)
	}

	delivered, err := gB.RecvGarlic(20 * time.Second)
	if err != nil {
		t.Fatalf("RecvGarlic returned error: %v", err)
	}
	if !bytes.Equal(delivered.Payload, payload) {
		t.Fatalf("delivered payload = %q, want %q", delivered.Payload, payload)
	}

	statsA := gA.GetStats()
	if statsA.OriginatedCircuits != 1 {
		t.Errorf("A's OriginatedCircuits = %d, want 1", statsA.OriginatedCircuits)
	}
	statsR := gR.GetStats()
	if statsR.RelayedCircuits != 1 {
		t.Errorf("R's RelayedCircuits = %d, want 1", statsR.RelayedCircuits)
	}
	statsB := gB.GetStats()
	// B is the terminal hop: it still runs relay-side replay bookkeeping
	// (its relay-table entry exists and still occupies a capacity slot),
	// but it never forwards a packet onward, so recordForward is never
	// called for it and it has no real previous/next hop to report.
	// relayCircuitState.snapshot deliberately excludes such entries
	// rather than surfacing them as phantom relays with zero traffic, so
	// the destination node reports no relayed circuits.
	if statsB.RelayedCircuits != 0 {
		t.Errorf("B's RelayedCircuits = %d, want 0 (B is the destination, not a relay - it never forwards)", statsB.RelayedCircuits)
	}
}

// TestIntegrationGossipDiscoversUnknownPeer proves discovery propagation
// end to end against a real mesh: A only ever talks directly to B, and B
// only ever talks directly to C - A never queries C's capability itself
// - yet after B gossips its known peers to A, A learns about C purely
// from that announce message.
func TestIntegrationGossipDiscoversUnknownPeer(t *testing.T) {
	nodeA := newLinkedTestNode(t)
	nodeB := newLinkedTestNode(t)
	nodeC := newLinkedTestNode(t)
	all := []*core.Core{nodeA, nodeB, nodeC}
	for _, n := range all {
		defer n.Stop()
	}

	connectChain(t, all) // A -- B -- C
	pumpAll(all)

	idA, err := garlic.NewIdentity()
	if err != nil {
		t.Fatalf("NewIdentity (A) returned error: %v", err)
	}
	idB, err := garlic.NewIdentity()
	if err != nil {
		t.Fatalf("NewIdentity (B) returned error: %v", err)
	}
	idC, err := garlic.NewIdentity()
	if err != nil {
		t.Fatalf("NewIdentity (C) returned error: %v", err)
	}

	cfg := garlic.DefaultConfig()
	cfg.CapabilityTimeout = 2 * time.Second

	gA := garlic.New(nodeA, idA, cfg, garlic.NewStaticRendezvous())
	defer gA.Close()
	gB := garlic.New(nodeB, idB, cfg, garlic.NewStaticRendezvous())
	defer gB.Close()
	gC := garlic.New(nodeC, idC, cfg, garlic.NewStaticRendezvous())
	defer gC.Close()

	// A learns about B directly; B learns about C directly. A never
	// queries C.
	waitForCapability(t, gA, nodeB.PublicKey(), 60*time.Second)
	waitForCapability(t, gB, nodeC.PublicKey(), 60*time.Second)

	for _, p := range gA.KnownPeers() {
		if bytes.Equal(p.NodeKey, nodeC.PublicKey()) {
			t.Fatal("A already knows about C before any gossip happened - test setup is invalid")
		}
	}

	if err := gB.GossipAnnounce(nodeA.PublicKey()); err != nil {
		t.Fatalf("GossipAnnounce returned error: %v", err)
	}

	deadline := time.Now().Add(10 * time.Second)
	for {
		found := false
		for _, p := range gA.KnownPeers() {
			if bytes.Equal(p.NodeKey, nodeC.PublicKey()) && bytes.Equal(p.GarlicPublicKey, idC.PublicKey) {
				found = true
			}
		}
		if found {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("A never learned about C via gossip within the deadline; known peers: %+v", gA.KnownPeers())
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestIntegrationAnnounceRequestTriggersImmediateGossipAnnounce(t *testing.T) {
	nodeA := newLinkedTestNode(t)
	nodeB := newLinkedTestNode(t)
	nodeC := newLinkedTestNode(t)
	all := []*core.Core{nodeA, nodeB, nodeC}
	for _, n := range all {
		defer n.Stop()
	}

	connectChain(t, all) // A -- B -- C
	pumpAll(all)

	idA, err := garlic.NewIdentity()
	if err != nil {
		t.Fatalf("NewIdentity (A) returned error: %v", err)
	}
	idB, err := garlic.NewIdentity()
	if err != nil {
		t.Fatalf("NewIdentity (B) returned error: %v", err)
	}
	idC, err := garlic.NewIdentity()
	if err != nil {
		t.Fatalf("NewIdentity (C) returned error: %v", err)
	}

	cfg := garlic.DefaultConfig()
	cfg.CapabilityTimeout = 2 * time.Second

	gA := garlic.New(nodeA, idA, cfg, garlic.NewStaticRendezvous())
	defer gA.Close()
	gB := garlic.New(nodeB, idB, cfg, garlic.NewStaticRendezvous())
	defer gB.Close()
	gC := garlic.New(nodeC, idC, cfg, garlic.NewStaticRendezvous())
	defer gC.Close()

	// B learns about C directly. A learns about B directly, but never
	// queries C, and critically: B never queries A either, so (unlike
	// TestIntegrationGossipDiscoversUnknownPeer) A is NOT in B's
	// capabilityCache and B's periodic gossipTick would never target A.
	waitForCapability(t, gA, nodeB.PublicKey(), 60*time.Second)
	waitForCapability(t, gB, nodeC.PublicKey(), 60*time.Second)

	for _, p := range gA.KnownPeers() {
		if bytes.Equal(p.NodeKey, nodeC.PublicKey()) {
			t.Fatal("A already knows about C before any gossip happened - test setup is invalid")
		}
	}

	if err := gA.RequestGossip(nodeB.PublicKey()); err != nil {
		t.Fatalf("RequestGossip returned error: %v", err)
	}

	deadline := time.Now().Add(10 * time.Second)
	for {
		found := false
		for _, p := range gA.KnownPeers() {
			if bytes.Equal(p.NodeKey, nodeC.PublicKey()) && bytes.Equal(p.GarlicPublicKey, idC.PublicKey) {
				found = true
			}
		}
		if found {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("A never learned about C via RequestGossip's pull within the deadline; known peers: %+v", gA.KnownPeers())
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// TestIntegrationSelectPathAgainstRealTopology proves SelectPath's
// core.Core.GetTree()/GetPaths() integration works against a real mesh,
// not just SelectDiversePath's already-unit-tested selection algorithm
// in isolation.
func TestIntegrationSelectPathAgainstRealTopology(t *testing.T) {
	nodeA := newLinkedTestNode(t)
	nodeB := newLinkedTestNode(t)
	nodeC := newLinkedTestNode(t)
	all := []*core.Core{nodeA, nodeB, nodeC}
	for _, n := range all {
		defer n.Stop()
	}

	connectChain(t, all) // A -- B -- C
	pumpAll(all)

	idA, err := garlic.NewIdentity()
	if err != nil {
		t.Fatalf("NewIdentity (A) returned error: %v", err)
	}
	idB, err := garlic.NewIdentity()
	if err != nil {
		t.Fatalf("NewIdentity (B) returned error: %v", err)
	}
	idC, err := garlic.NewIdentity()
	if err != nil {
		t.Fatalf("NewIdentity (C) returned error: %v", err)
	}

	cfg := garlic.DefaultConfig()
	cfg.CapabilityTimeout = 2 * time.Second
	cfg.MinHopCount = 0 // this test's tiny topology has no room for a real distance filter

	gA := garlic.New(nodeA, idA, cfg, garlic.NewStaticRendezvous())
	defer gA.Close()
	gB := garlic.New(nodeB, idB, cfg, garlic.NewStaticRendezvous())
	defer gB.Close()
	gC := garlic.New(nodeC, idC, cfg, garlic.NewStaticRendezvous())
	defer gC.Close()

	// A needs a resolved mesh path to a candidate (not just knowledge of
	// its key) before SelectPath can score it - direct contact resolves
	// one as a side effect, same as real usage would after discovering
	// candidates via gossip.
	waitForCapability(t, gA, nodeB.PublicKey(), 60*time.Second)
	waitForCapability(t, gA, nodeC.PublicKey(), 60*time.Second)

	// This deliberately does not assert a *specific* outcome (which of
	// B/C gets picked at n=1, or whether both pass the shared-tree-parent
	// check at n=2): both depend on this tiny topology's exact,
	// non-deterministic-across-runs tree shape, which isn't something
	// this test controls or should reverse-engineer. The sorting and
	// diversity-filtering *behavior* is already deterministically proven
	// against controlled inputs in selection_test.go. What this test
	// exists to prove is narrower and topology-independent: that
	// candidatePool pulls real core.Core.GetTree()/GetPaths() data end
	// to end and SelectPath returns a legitimate, known candidate from
	// it, not a stub or an empty pool.
	selected, err := gA.SelectPath(1)
	if err != nil {
		t.Fatalf("SelectPath returned error: %v", err)
	}
	if len(selected) != 1 {
		t.Fatalf("SelectPath returned %d hops, want 1", len(selected))
	}
	hop := selected[0]
	if !bytes.Equal(hop.NodeKey, nodeB.PublicKey()) && !bytes.Equal(hop.NodeKey, nodeC.PublicKey()) {
		t.Fatalf("selected hop %x is neither B nor C", hop.NodeKey)
	}
	if len(hop.GarlicPublicKey) == 0 {
		t.Fatal("selected hop has no GarlicPublicKey - candidatePool didn't carry real discovery data through")
	}
}

func TestIntegrationCandidatePoolCarriesSelfVerifiedThrough(t *testing.T) {
	nodeA := newLinkedTestNode(t)
	nodeB := newLinkedTestNode(t)
	all := []*core.Core{nodeA, nodeB}
	for _, n := range all {
		defer n.Stop()
	}

	connectChain(t, all) // A -- B
	pumpAll(all)

	idA, err := garlic.NewIdentity()
	if err != nil {
		t.Fatalf("NewIdentity (A) returned error: %v", err)
	}
	idB, err := garlic.NewIdentity()
	if err != nil {
		t.Fatalf("NewIdentity (B) returned error: %v", err)
	}

	cfg := garlic.DefaultConfig()
	cfg.CapabilityTimeout = 2 * time.Second
	cfg.MinHopCount = 0 // A and B are direct peers here (hop count 1), below the default distance filter

	gA := garlic.New(nodeA, idA, cfg, garlic.NewStaticRendezvous())
	defer gA.Close()
	gB := garlic.New(nodeB, idB, cfg, garlic.NewStaticRendezvous())
	defer gB.Close()

	// A directly capability-queries B, which resolves a real mesh path
	// AND records B as self-verified (Task 1/2's handleCapabilityResponse
	// change).
	waitForCapability(t, gA, nodeB.PublicKey(), 60*time.Second)

	selected, err := gA.SelectPath(1)
	if err != nil {
		t.Fatalf("SelectPath returned error: %v", err)
	}
	if len(selected) != 1 || !selected[0].SelfVerified {
		t.Fatalf("SelectPath(1) = %+v, want one self-verified candidate (B, directly queried by A)", selected)
	}
}

// TestIntegrationMultipathSpreadsTraffic proves SendGarlicMultipath
// actually delivers over two independent paths against a real mesh, not
// just that circuitPool's round-robin index advances correctly in
// isolation (already covered by multipath_test.go).
func TestIntegrationMultipathSpreadsTraffic(t *testing.T) {
	nodeA := newLinkedTestNode(t)
	nodeB := newLinkedTestNode(t)
	nodeC := newLinkedTestNode(t)
	all := []*core.Core{nodeA, nodeB, nodeC}
	for _, n := range all {
		defer n.Stop()
	}

	connectChain(t, all) // A -- B -- C (C reachable from A transparently through B)
	pumpAll(all)

	idA, err := garlic.NewIdentity()
	if err != nil {
		t.Fatalf("NewIdentity (A) returned error: %v", err)
	}
	idB, err := garlic.NewIdentity()
	if err != nil {
		t.Fatalf("NewIdentity (B) returned error: %v", err)
	}
	idC, err := garlic.NewIdentity()
	if err != nil {
		t.Fatalf("NewIdentity (C) returned error: %v", err)
	}

	cfg := garlic.DefaultConfig()
	cfg.CapabilityTimeout = 2 * time.Second

	gA := garlic.New(nodeA, idA, cfg, garlic.NewStaticRendezvous())
	defer gA.Close()
	gB := garlic.New(nodeB, idB, cfg, garlic.NewStaticRendezvous())
	defer gB.Close()
	gC := garlic.New(nodeC, idC, cfg, garlic.NewStaticRendezvous())
	defer gC.Close()

	capB := waitForCapability(t, gA, nodeB.PublicKey(), 60*time.Second)
	capC := waitForCapability(t, gA, nodeC.PublicKey(), 60*time.Second)

	pool, err := gA.CreateCircuitPool(
		[][]garlic.CapabilityMessage{{*capB}, {*capC}},
		[][][]byte{{nodeB.PublicKey()}, {nodeC.PublicKey()}},
	)
	if err != nil {
		t.Fatalf("CreateCircuitPool returned error: %v", err)
	}
	defer gA.ClosePool(pool)

	const messagesPerDest = 3
	for i := range 2 * messagesPerDest {
		payload := []byte{byte(i)}
		if err := gA.SendGarlicMultipath(pool, payload); err != nil {
			t.Fatalf("SendGarlicMultipath call %d returned error: %v", i, err)
		}
	}

	countB := countDelivered(t, gB, messagesPerDest, 20*time.Second)
	countC := countDelivered(t, gC, messagesPerDest, 20*time.Second)
	if countB != messagesPerDest {
		t.Errorf("B received %d messages, want %d (round-robin should split evenly)", countB, messagesPerDest)
	}
	if countC != messagesPerDest {
		t.Errorf("C received %d messages, want %d (round-robin should split evenly)", countC, messagesPerDest)
	}
}

// TestIntegrationSendGarlicBundledDeliversAmongCover proves
// SendGarlicBundled's real entry survives a real mesh trip - decrypt,
// replay-window, and forward logic all still fire correctly - while
// mixed in with cover entries that the receiving hop must (and does)
// silently fail to decrypt and discard, exactly once, with no
// duplicate/garbage deliveries.
func TestIntegrationSendGarlicBundledDeliversAmongCover(t *testing.T) {
	nodeA := newLinkedTestNode(t)
	nodeB := newLinkedTestNode(t)
	all := []*core.Core{nodeA, nodeB}
	for _, n := range all {
		defer n.Stop()
	}

	connectChain(t, all)
	pumpAll(all)

	idA, err := garlic.NewIdentity()
	if err != nil {
		t.Fatalf("NewIdentity (A) returned error: %v", err)
	}
	idB, err := garlic.NewIdentity()
	if err != nil {
		t.Fatalf("NewIdentity (B) returned error: %v", err)
	}

	cfg := garlic.DefaultConfig()
	cfg.CapabilityTimeout = 2 * time.Second

	gA := garlic.New(nodeA, idA, cfg, garlic.NewStaticRendezvous())
	defer gA.Close()
	gB := garlic.New(nodeB, idB, cfg, garlic.NewStaticRendezvous())
	defer gB.Close()

	capB := waitForCapability(t, gA, nodeB.PublicKey(), 60*time.Second)

	circuitID, err := gA.CreateCircuit([]garlic.CapabilityMessage{*capB}, [][]byte{nodeB.PublicKey()})
	if err != nil {
		t.Fatalf("CreateCircuit returned error: %v", err)
	}

	payload := []byte("hello bob, hidden among cover traffic")
	if err := gA.SendGarlicBundled(circuitID, payload, 5); err != nil {
		t.Fatalf("SendGarlicBundled returned error: %v", err)
	}

	delivered, err := gB.RecvGarlic(20 * time.Second)
	if err != nil {
		t.Fatalf("RecvGarlic returned error: %v", err)
	}
	if !bytes.Equal(delivered.Payload, payload) {
		t.Fatalf("delivered payload = %q, want %q", delivered.Payload, payload)
	}

	// Nothing else should ever arrive: the 5 cover entries must never
	// decrypt into a delivery.
	if extra, err := gB.RecvGarlic(500 * time.Millisecond); err == nil {
		t.Fatalf("unexpected second delivery: %+v", extra)
	}
}

// TestIntegrationSendGarlicAutoThenRecvGarlicAutoRoundTrips proves the
// tagged auto-pool delivery path (SendGarlicAuto -> msgTypeCircuitDataV3
// -> deliverTagged -> g.autoDelivered -> RecvGarlicAuto) round-trips a
// real payload over a real mesh, and - just as importantly - that this
// traffic never surfaces on B's plain RecvGarlic/g.delivered channel,
// which the existing SendGarlic/RecvGarlic path uses instead.
func TestIntegrationSendGarlicAutoThenRecvGarlicAutoRoundTrips(t *testing.T) {
	nodeA := newLinkedTestNode(t)
	nodeB := newLinkedTestNode(t)
	all := []*core.Core{nodeA, nodeB}
	for _, n := range all {
		defer n.Stop()
	}
	connectChain(t, all)
	pumpAll(all)

	idA, err := garlic.NewIdentity()
	if err != nil {
		t.Fatalf("NewIdentity (A) returned error: %v", err)
	}
	idB, err := garlic.NewIdentity()
	if err != nil {
		t.Fatalf("NewIdentity (B) returned error: %v", err)
	}

	cfg := garlic.DefaultConfig()
	cfg.CapabilityTimeout = 2 * time.Second

	gA := garlic.New(nodeA, idA, cfg, garlic.NewStaticRendezvous())
	defer gA.Close()
	gB := garlic.New(nodeB, idB, cfg, garlic.NewStaticRendezvous())
	defer gB.Close()

	capB := waitForCapability(t, gA, nodeB.PublicKey(), 60*time.Second)
	if !capB.SupportsAutoCircuit() {
		t.Fatal("B's capability response does not advertise CapabilityAutoCircuit")
	}

	circuitID, err := gA.CreateCircuit([]garlic.CapabilityMessage{*capB}, [][]byte{nodeB.PublicKey()})
	if err != nil {
		t.Fatalf("CreateCircuit returned error: %v", err)
	}

	if err := gA.SendGarlicAuto(circuitID, []byte("auto-hello")); err != nil {
		t.Fatalf("SendGarlicAuto returned error: %v", err)
	}
	msg, err := gB.RecvGarlicAuto(10 * time.Second)
	if err != nil {
		t.Fatalf("RecvGarlicAuto returned error: %v", err)
	}
	if string(msg.Payload) != "auto-hello" {
		t.Fatalf("Payload = %q, want %q", msg.Payload, "auto-hello")
	}
	if msg.CircuitID != circuitID {
		t.Fatalf("CircuitID = %x, want %x", msg.CircuitID, circuitID)
	}

	// Nothing sent via SendGarlicAuto should ever surface on B's plain
	// RecvGarlic channel.
	if _, err := gB.RecvGarlic(200 * time.Millisecond); !errors.Is(err, garlic.ErrRecvTimeout) {
		t.Fatalf("RecvGarlic err = %v, want ErrRecvTimeout (auto-pool traffic must stay off the manual delivery channel)", err)
	}
}

func TestIntegrationBootstrapPeersRecordedAsSelfVerified(t *testing.T) {
	nodeA := newLinkedTestNode(t)
	nodeB := newLinkedTestNode(t)
	all := []*core.Core{nodeA, nodeB}
	for _, n := range all {
		defer n.Stop()
	}
	connectChain(t, all)
	pumpAll(all)

	idA, err := garlic.NewIdentity()
	if err != nil {
		t.Fatalf("NewIdentity (A) returned error: %v", err)
	}
	idB, err := garlic.NewIdentity()
	if err != nil {
		t.Fatalf("NewIdentity (B) returned error: %v", err)
	}

	// B must exist and be Garlic-capable before A starts, since A's
	// bootstrap step (launched from New, best-effort) queries it once.
	cfgB := garlic.DefaultConfig()
	cfgB.CapabilityTimeout = 2 * time.Second
	gB := garlic.New(nodeB, idB, cfgB, garlic.NewStaticRendezvous())
	defer gB.Close()

	cfgA := garlic.DefaultConfig()
	cfgA.CapabilityTimeout = 2 * time.Second
	cfgA.BootstrapPeers = []string{hex.EncodeToString(nodeB.PublicKey())}
	gA := garlic.New(nodeA, idA, cfgA, garlic.NewStaticRendezvous())
	defer gA.Close()

	deadline := time.Now().Add(10 * time.Second)
	for {
		found := false
		for _, p := range gA.KnownPeers() {
			if bytes.Equal(p.NodeKey, nodeB.PublicKey()) && p.SelfVerified {
				found = true
			}
		}
		if found {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("A never recorded its configured BootstrapPeers entry as self-verified; known peers: %+v", gA.KnownPeers())
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestIntegrationAutoCreateCircuitUsesSelfVerifiedGuard(t *testing.T) {
	nodeA := newLinkedTestNode(t)
	nodeB := newLinkedTestNode(t)
	all := []*core.Core{nodeA, nodeB}
	for _, n := range all {
		defer n.Stop()
	}
	connectChain(t, all)
	pumpAll(all)

	idA, err := garlic.NewIdentity()
	if err != nil {
		t.Fatalf("NewIdentity (A) returned error: %v", err)
	}
	idB, err := garlic.NewIdentity()
	if err != nil {
		t.Fatalf("NewIdentity (B) returned error: %v", err)
	}

	cfg := garlic.DefaultConfig()
	cfg.CapabilityTimeout = 2 * time.Second
	cfg.MinHopCount = 0 // this tiny topology has no room for a real distance filter

	gA := garlic.New(nodeA, idA, cfg, garlic.NewStaticRendezvous())
	defer gA.Close()
	gB := garlic.New(nodeB, idB, cfg, garlic.NewStaticRendezvous())
	defer gB.Close()

	waitForCapability(t, gA, nodeB.PublicKey(), 60*time.Second)

	id, err := gA.AutoCreateCircuit(1)
	if err != nil {
		t.Fatalf("AutoCreateCircuit returned error: %v", err)
	}

	var found *garlic.Circuit
	for _, c := range gA.OriginatedCircuits() {
		if c.ID == id {
			found = c
		}
	}
	if found == nil {
		t.Fatal("AutoCreateCircuit's returned ID is not in OriginatedCircuits()")
	}
	hops := found.HopKeys()
	if len(hops) != 1 || !bytes.Equal(hops[0], nodeB.PublicKey()) {
		t.Fatalf("hops = %x, want [%x] (B, the only self-verified candidate)", hops, nodeB.PublicKey())
	}
}

func TestIntegrationAutoCreateCircuitFailsWithoutSelfVerifiedCandidate(t *testing.T) {
	nodeA := newLinkedTestNode(t) // deliberately unpeered - candidatePool() will be empty
	defer nodeA.Stop()

	idA, err := garlic.NewIdentity()
	if err != nil {
		t.Fatalf("NewIdentity (A) returned error: %v", err)
	}
	gA := garlic.New(nodeA, idA, garlic.DefaultConfig(), garlic.NewStaticRendezvous())
	defer gA.Close()

	if _, err := gA.AutoCreateCircuit(1); !errors.Is(err, garlic.ErrNoSelfVerifiedCandidates) {
		t.Fatalf("err = %v, want ErrNoSelfVerifiedCandidates", err)
	}
}

func countDelivered(t *testing.T, g *garlic.Garlic, want int, maxWait time.Duration) int {
	t.Helper()
	deadline := time.Now().Add(maxWait)
	count := 0
	for count < want && time.Now().Before(deadline) {
		if _, err := g.RecvGarlic(1 * time.Second); err == nil {
			count++
		}
	}
	return count
}
