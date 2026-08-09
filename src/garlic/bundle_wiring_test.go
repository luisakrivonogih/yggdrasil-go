package garlic

import (
	"bytes"
	"crypto/rand"
	"testing"
	"time"
)

// randomCoverSubMessage returns size random bytes shaped like a
// circuitData body (ephemeralPub || Envelope): to any relay that hasn't
// decrypted it, indistinguishable in shape from a real one.
func randomCoverSubMessage(t *testing.T, size int) []byte {
	t.Helper()
	b := make([]byte, size)
	if _, err := rand.Read(b); err != nil {
		t.Fatalf("rand.Read returned error: %v", err)
	}
	return b
}

func TestProcessCircuitDataBundleDeliversRealMessageAmongCover(t *testing.T) {
	g := newTestGarlic(t)
	payload := []byte("hello bob, hidden in a bundle")
	real, _ := buildTestCircuitData(t, []*Identity{g.identity}, [][]byte{g.identity.PublicKey}, payload, time.Minute)

	bundle := &Bundle{Messages: [][]byte{
		randomCoverSubMessage(t, len(real)),
		real,
		randomCoverSubMessage(t, len(real)),
	}}
	body, err := bundle.Marshal()
	if err != nil {
		t.Fatalf("Marshal returned error: %v", err)
	}

	actions := g.processCircuitDataBundle(body)
	var delivered int
	for _, a := range actions {
		if a.kind == actionDeliver {
			delivered++
			if !bytes.Equal(a.payload, payload) {
				t.Errorf("delivered payload = %q, want %q", a.payload, payload)
			}
		}
	}
	if delivered != 1 {
		t.Fatalf("got %d actionDeliver results, want exactly 1 (cover messages must not produce any action)", delivered)
	}
}

func TestProcessCircuitDataBundleHandlesMultipleForwards(t *testing.T) {
	g := newTestGarlic(t)
	destID1, err := NewIdentity()
	if err != nil {
		t.Fatalf("NewIdentity returned error: %v", err)
	}
	destID2, err := NewIdentity()
	if err != nil {
		t.Fatalf("NewIdentity returned error: %v", err)
	}
	msg1, _ := buildTestCircuitData(t, []*Identity{g.identity, destID1}, [][]byte{[]byte("relay"), []byte("dest-1")}, []byte("a"), time.Minute)
	msg2, _ := buildTestCircuitData(t, []*Identity{g.identity, destID2}, [][]byte{[]byte("relay"), []byte("dest-2")}, []byte("b"), time.Minute)

	bundle := &Bundle{Messages: [][]byte{msg1, msg2}}
	body, err := bundle.Marshal()
	if err != nil {
		t.Fatalf("Marshal returned error: %v", err)
	}

	actions := g.processCircuitDataBundle(body)
	var forwards int
	destinations := map[string]bool{}
	for _, a := range actions {
		if a.kind == actionForward {
			forwards++
			destinations[string(a.forwardTo)] = true
		}
	}
	if forwards != 2 {
		t.Fatalf("got %d actionForward results, want 2", forwards)
	}
	if !destinations["dest-1"] || !destinations["dest-2"] {
		t.Fatalf("forward destinations = %v, want both dest-1 and dest-2", destinations)
	}
}

func TestProcessCircuitDataBundleAllCoverProducesNoActions(t *testing.T) {
	g := newTestGarlic(t)
	bundle := &Bundle{Messages: [][]byte{
		randomCoverSubMessage(t, 200),
		randomCoverSubMessage(t, 200),
	}}
	body, err := bundle.Marshal()
	if err != nil {
		t.Fatalf("Marshal returned error: %v", err)
	}

	actions := g.processCircuitDataBundle(body)
	if len(actions) != 0 {
		t.Fatalf("got %d actions from an all-cover bundle, want 0", len(actions))
	}
}

func TestProcessCircuitDataBundleIgnoresMalformedBundle(t *testing.T) {
	g := newTestGarlic(t)
	actions := g.processCircuitDataBundle([]byte{0xFF, 0xFF, 0xFF}) // must not panic
	if len(actions) != 0 {
		t.Fatalf("got %d actions from a malformed bundle, want 0", len(actions))
	}
}
