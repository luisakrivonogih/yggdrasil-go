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
	if !capR.SupportsGarlicV1() {
		t.Fatal("R's capability response does not advertise garlic-v1")
	}
	if !bytes.Equal(capR.PublicKey, idR.PublicKey) {
		t.Fatalf("R's advertised public key = %x, want %x", capR.PublicKey, idR.PublicKey)
	}
	capB := waitForCapability(t, gA, nodeB.PublicKey(), 180*time.Second)
	if !capB.SupportsGarlicV1() {
		t.Fatal("B's capability response does not advertise garlic-v1")
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
	if statsB.RelayedCircuits != 1 {
		t.Errorf("B's RelayedCircuits = %d, want 1 (B still runs relay-side replay bookkeeping as the terminal hop)", statsB.RelayedCircuits)
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
