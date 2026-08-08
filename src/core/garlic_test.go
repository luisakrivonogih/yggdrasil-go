package core

import (
	"bytes"
	"crypto/ed25519"
	"testing"
	"time"
)

// pumpReadFrom continuously calls ReadFrom on n so that its internal
// type-tag switch runs (this is what actually dispatches typeSessionGarlic
// packets to a registered GarlicHandler; typeSessionTraffic packets read
// this way are simply discarded). It returns once ReadFrom starts
// erroring, which happens once the node is stopped.
//
// Both ends of a pair need something driving their ReadFrom loop for the
// underlying encrypted session between them to be serviced at all - not
// just the receiver of application data. CreateEchoListener's tests get
// this for free because they block reading a reply; a one-directional
// send with no reply (as with WriteGarlic) needs an explicit pump on both
// sides.
func pumpReadFrom(n *Core) {
	buf := make([]byte, 65535)
	for {
		if _, _, err := n.ReadFrom(buf); err != nil {
			return
		}
	}
}

func TestCore_GarlicHandler_ReceivesTaggedPacket(t *testing.T) {
	nodeA, nodeB := CreateAndConnectTwo(t, false)
	defer nodeA.Stop()
	defer nodeB.Stop()

	type received struct {
		from ed25519.PublicKey
		data []byte
	}
	ch := make(chan received, 1)
	nodeA.SetGarlicHandler(func(from ed25519.PublicKey, data []byte) {
		ch <- received{from, data}
	})
	go pumpReadFrom(nodeA)
	go pumpReadFrom(nodeB)

	if !WaitConnected(nodeA, nodeB) {
		t.Fatal("nodes did not connect")
	}

	// The underlying transport is an unreliable datagram service (like
	// ordinary Yggdrasil traffic), so - as with any UDP-like send - a
	// single packet isn't guaranteed to arrive; retry sending until the
	// handler observes one, rather than asserting on exactly one send.
	payload := []byte("garlic payload")
	retry := time.NewTicker(200 * time.Millisecond)
	defer retry.Stop()
	deadline := time.After(5 * time.Second)
	if _, err := nodeB.WriteGarlic(payload, nodeA.LocalAddr()); err != nil {
		t.Fatal(err)
	}
	for {
		select {
		case r := <-ch:
			if !bytes.Equal(r.data, payload) {
				t.Fatalf("data = %q, want %q", r.data, payload)
			}
			if !bytes.Equal(r.from, nodeB.PublicKey()) {
				t.Fatalf("from = %x, want %x", r.from, nodeB.PublicKey())
			}
			return
		case <-retry.C:
			if _, err := nodeB.WriteGarlic(payload, nodeA.LocalAddr()); err != nil {
				t.Fatal(err)
			}
		case <-deadline:
			t.Fatal("timed out waiting for garlic handler to be called")
		}
	}
}

// TestCore_GarlicHandler_UnregisteredHandlerDropsSilently is the
// legacy-node compatibility guarantee at the unit level: a node that never
// calls SetGarlicHandler (i.e. Garlic disabled/unsupported, exactly like
// every node before this feature existed) must silently discard
// typeSessionGarlic packets and keep serving ordinary traffic normally,
// with no error surfaced anywhere.
func TestCore_GarlicHandler_UnregisteredHandlerDropsSilently(t *testing.T) {
	nodeA, nodeB := CreateAndConnectTwo(t, false)
	defer nodeA.Stop()
	defer nodeB.Stop()

	if !WaitConnected(nodeA, nodeB) {
		t.Fatal("nodes did not connect")
	}

	if _, err := nodeB.WriteGarlic([]byte("garlic payload"), nodeA.LocalAddr()); err != nil {
		t.Fatal(err)
	}

	// Ordinary traffic must still work: the un-handled garlic packet must
	// not disrupt nodeA's normal ReadFrom loop or leave it in a bad state.
	// nodeB's blocking ReadFrom below (waiting for the echo) is what
	// services its side of the session; see pumpReadFrom's doc comment.
	msgLen := 1500
	done := CreateEchoListener(t, nodeA, msgLen, 1)
	msg := make([]byte, msgLen)
	msg[0] = 0x60
	copy(msg[8:24], nodeB.Address())
	copy(msg[24:40], nodeA.Address())
	if _, err := nodeB.WriteTo(msg, nodeA.LocalAddr()); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, msgLen)
	if _, _, err := nodeB.ReadFrom(buf); err != nil {
		t.Fatal(err)
	}
	<-done
}
