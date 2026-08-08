package garlic

import (
	"bytes"
	"testing"
	"time"
)

func testHops(n int) []Hop {
	hops := make([]Hop, n)
	for i := range hops {
		key, _ := DeriveKey([]byte{byte(i)}, nil, LabelLayerKey)
		hops[i] = Hop{
			NodeKey: []byte{byte('A' + i)},
			Key:     key,
			Counter: 0,
		}
	}
	return hops
}

func TestNewCircuitGeneratesRandomID(t *testing.T) {
	c1, err := NewCircuit(testHops(2), time.Minute, 100, 100000)
	if err != nil {
		t.Fatalf("NewCircuit returned error: %v", err)
	}
	c2, err := NewCircuit(testHops(2), time.Minute, 100, 100000)
	if err != nil {
		t.Fatalf("NewCircuit returned error: %v", err)
	}
	if c1.ID == c2.ID {
		t.Error("two circuits got the same ID")
	}
}

func TestNewCircuitRejectsEmptyPath(t *testing.T) {
	if _, err := NewCircuit(nil, time.Minute, 100, 100000); err == nil {
		t.Fatal("expected error for empty path, got nil")
	}
}

func TestNewCircuitRejectsPathExceedingMaxLength(t *testing.T) {
	if _, err := NewCircuit(testHops(MaxPathLength+1), time.Minute, 100, 100000); err == nil {
		t.Fatal("expected error for path exceeding MaxPathLength, got nil")
	}
}

func TestCircuitSealProducesPeelableOnion(t *testing.T) {
	hops := testHops(2)
	c, err := NewCircuit(hops, time.Minute, 100, 100000)
	if err != nil {
		t.Fatalf("NewCircuit returned error: %v", err)
	}
	payload := []byte("hello bob")

	onion, firstHop, err := c.Seal(payload)
	if err != nil {
		t.Fatalf("Seal returned error: %v", err)
	}
	if !bytes.Equal(firstHop, hops[0].NodeKey) {
		t.Fatalf("firstHop = %q, want %q", firstHop, hops[0].NodeKey)
	}

	atHop0, err := DecryptLayer(hops[0].Key, 0, onion)
	if err != nil {
		t.Fatalf("DecryptLayer at hop0 (counter 0) returned error: %v", err)
	}
	if !bytes.Equal(atHop0.NextHop, hops[1].NodeKey) {
		t.Fatalf("hop0 NextHop = %q, want %q", atHop0.NextHop, hops[1].NodeKey)
	}
	atHop1, err := DecryptLayer(hops[1].Key, 0, atHop0.Inner)
	if err != nil {
		t.Fatalf("DecryptLayer at hop1 (counter 0) returned error: %v", err)
	}
	if !bytes.Equal(atHop1.Inner, payload) {
		t.Fatalf("hop1 Inner = %q, want %q", atHop1.Inner, payload)
	}
}

func TestCircuitSealIncrementsPerHopCounters(t *testing.T) {
	hops := testHops(1)
	c, err := NewCircuit(hops, time.Minute, 100, 100000)
	if err != nil {
		t.Fatalf("NewCircuit returned error: %v", err)
	}

	onion1, _, err := c.Seal([]byte("first"))
	if err != nil {
		t.Fatalf("first Seal returned error: %v", err)
	}
	onion2, _, err := c.Seal([]byte("second"))
	if err != nil {
		t.Fatalf("second Seal returned error: %v", err)
	}

	if _, err := DecryptLayer(hops[0].Key, 0, onion1); err != nil {
		t.Fatalf("expected onion1 decryptable at counter 0: %v", err)
	}
	if _, err := DecryptLayer(hops[0].Key, 1, onion2); err != nil {
		t.Fatalf("expected onion2 decryptable at counter 1: %v", err)
	}
	// The counter must actually have moved on - onion2 must not also be
	// decryptable at counter 0 (that would mean a nonce got reused).
	if _, err := DecryptLayer(hops[0].Key, 0, onion2); err == nil {
		t.Fatal("onion2 decrypted at counter 0, want failure (would indicate nonce reuse)")
	}
}

func TestCircuitSealRejectsAfterExpiry(t *testing.T) {
	c, err := NewCircuit(testHops(1), time.Millisecond, 100, 100000)
	if err != nil {
		t.Fatalf("NewCircuit returned error: %v", err)
	}
	time.Sleep(5 * time.Millisecond)

	if _, _, err := c.Seal([]byte("payload")); err == nil {
		t.Fatal("expected error sealing on an expired circuit, got nil")
	}
}

func TestCircuitSealRejectsAfterMaxPackets(t *testing.T) {
	c, err := NewCircuit(testHops(1), time.Minute, 1, 100000)
	if err != nil {
		t.Fatalf("NewCircuit returned error: %v", err)
	}
	if _, _, err := c.Seal([]byte("first")); err != nil {
		t.Fatalf("first Seal returned error: %v", err)
	}
	if _, _, err := c.Seal([]byte("second")); err == nil {
		t.Fatal("expected error exceeding MaxPackets, got nil")
	}
}

func TestCircuitSealRejectsAfterMaxBytes(t *testing.T) {
	c, err := NewCircuit(testHops(1), time.Minute, 100, 5)
	if err != nil {
		t.Fatalf("NewCircuit returned error: %v", err)
	}
	if _, _, err := c.Seal([]byte("123456789")); err == nil {
		t.Fatal("expected error exceeding MaxBytes, got nil")
	}
}

func TestCircuitSealRejectsAfterClose(t *testing.T) {
	c, err := NewCircuit(testHops(1), time.Minute, 100, 100000)
	if err != nil {
		t.Fatalf("NewCircuit returned error: %v", err)
	}
	c.Close()
	if _, _, err := c.Seal([]byte("payload")); err == nil {
		t.Fatal("expected error sealing a closed circuit, got nil")
	}
}
