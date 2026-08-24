package garlic

import (
	"bytes"
	"encoding/binary"
	"testing"
	"time"
)

// testCircuitID builds a distinguishable CircuitID for tests, encoding n
// into the last 8 bytes so distinct small integers remain distinct
// distinguishable IDs (the type itself carries no integer semantics -
// production code only ever compares CircuitID for equality).
func testCircuitID(n uint64) CircuitID {
	var id CircuitID
	binary.BigEndian.PutUint64(id[8:], n)
	return id
}

func testHops(n int) []Hop {
	hops := make([]Hop, n)
	for i := range hops {
		key, _ := DeriveKey([]byte{byte(i)}, nil, LabelCircuitDataSend)
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

	onion, firstHop, counter, err := c.Seal(payload)
	if err != nil {
		t.Fatalf("Seal returned error: %v", err)
	}
	if !bytes.Equal(firstHop, hops[0].NodeKey) {
		t.Fatalf("firstHop = %q, want %q", firstHop, hops[0].NodeKey)
	}
	if counter != 0 {
		t.Fatalf("counter = %d, want 0 (first Seal call)", counter)
	}

	atHop0, err := DecryptLayer(hops[0].Key, counter, onion)
	if err != nil {
		t.Fatalf("DecryptLayer at hop0 (counter 0) returned error: %v", err)
	}
	if !bytes.Equal(atHop0.NextHop, hops[1].NodeKey) {
		t.Fatalf("hop0 NextHop = %q, want %q", atHop0.NextHop, hops[1].NodeKey)
	}
	atHop1, err := DecryptLayer(hops[1].Key, counter, atHop0.Inner)
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

	onion1, _, counter1, err := c.Seal([]byte("first"))
	if err != nil {
		t.Fatalf("first Seal returned error: %v", err)
	}
	onion2, _, counter2, err := c.Seal([]byte("second"))
	if err != nil {
		t.Fatalf("second Seal returned error: %v", err)
	}
	if counter2 != counter1+1 {
		t.Fatalf("counter2 = %d, want counter1+1 = %d", counter2, counter1+1)
	}

	if _, err := DecryptLayer(hops[0].Key, counter1, onion1); err != nil {
		t.Fatalf("expected onion1 decryptable at counter1: %v", err)
	}
	if _, err := DecryptLayer(hops[0].Key, counter2, onion2); err != nil {
		t.Fatalf("expected onion2 decryptable at counter2: %v", err)
	}
	// The counter must actually have moved on - onion2 must not also be
	// decryptable at counter1 (that would mean a nonce got reused).
	if _, err := DecryptLayer(hops[0].Key, counter1, onion2); err == nil {
		t.Fatal("onion2 decrypted at counter1, want failure (would indicate nonce reuse)")
	}
}

func TestCircuitSealRejectsAfterExpiry(t *testing.T) {
	c, err := NewCircuit(testHops(1), time.Millisecond, 100, 100000)
	if err != nil {
		t.Fatalf("NewCircuit returned error: %v", err)
	}
	time.Sleep(5 * time.Millisecond)

	if _, _, _, err := c.Seal([]byte("payload")); err == nil {
		t.Fatal("expected error sealing on an expired circuit, got nil")
	}
}

func TestCircuitSealRejectsAfterMaxPackets(t *testing.T) {
	c, err := NewCircuit(testHops(1), time.Minute, 1, 100000)
	if err != nil {
		t.Fatalf("NewCircuit returned error: %v", err)
	}
	if _, _, _, err := c.Seal([]byte("first")); err != nil {
		t.Fatalf("first Seal returned error: %v", err)
	}
	if _, _, _, err := c.Seal([]byte("second")); err == nil {
		t.Fatal("expected error exceeding MaxPackets, got nil")
	}
}

func TestCircuitSealRejectsAfterMaxBytes(t *testing.T) {
	c, err := NewCircuit(testHops(1), time.Minute, 100, 5)
	if err != nil {
		t.Fatalf("NewCircuit returned error: %v", err)
	}
	if _, _, _, err := c.Seal([]byte("123456789")); err == nil {
		t.Fatal("expected error exceeding MaxBytes, got nil")
	}
}

func TestCircuitSealRejectsAfterClose(t *testing.T) {
	c, err := NewCircuit(testHops(1), time.Minute, 100, 100000)
	if err != nil {
		t.Fatalf("NewCircuit returned error: %v", err)
	}
	c.Close()
	if _, _, _, err := c.Seal([]byte("payload")); err == nil {
		t.Fatal("expected error sealing a closed circuit, got nil")
	}
}

func TestCircuitHopKeysReturnsOrderedNodeKeys(t *testing.T) {
	hops := []Hop{
		{NodeKey: []byte("node-a"), Key: make([]byte, 32)},
		{NodeKey: []byte("node-b"), Key: make([]byte, 32)},
	}
	c, err := NewCircuit(hops, time.Minute, 100, 100000)
	if err != nil {
		t.Fatalf("NewCircuit returned error: %v", err)
	}
	keys := c.HopKeys()
	if len(keys) != 2 {
		t.Fatalf("HopKeys() returned %d keys, want 2", len(keys))
	}
	if string(keys[0]) != "node-a" || string(keys[1]) != "node-b" {
		t.Fatalf("HopKeys() = %q, want [node-a node-b]", keys)
	}
}

func TestCircuitHopKeysIsACopy(t *testing.T) {
	hops := []Hop{{NodeKey: []byte("node-a"), Key: make([]byte, 32)}}
	c, err := NewCircuit(hops, time.Minute, 100, 100000)
	if err != nil {
		t.Fatalf("NewCircuit returned error: %v", err)
	}
	keys := c.HopKeys()
	keys[0][0] = 'X' // mutate the returned slice
	if string(c.HopKeys()[0]) != "node-a" {
		t.Fatal("mutating HopKeys()'s return value affected the circuit's internal hop state")
	}
}

func TestCircuitTrafficStatsTracksSeals(t *testing.T) {
	hops := []Hop{{NodeKey: []byte("node-a"), Key: make([]byte, 32)}}
	c, err := NewCircuit(hops, time.Minute, 100, 100000)
	if err != nil {
		t.Fatalf("NewCircuit returned error: %v", err)
	}
	packets, bytes := c.TrafficStats()
	if packets != 0 || bytes != 0 {
		t.Fatalf("TrafficStats() before any Seal = (%d, %d), want (0, 0)", packets, bytes)
	}
	if _, _, _, err := c.Seal([]byte("hello")); err != nil {
		t.Fatalf("Seal returned error: %v", err)
	}
	packets, bytes = c.TrafficStats()
	if packets != 1 || bytes != 5 {
		t.Fatalf("TrafficStats() after one 5-byte Seal = (%d, %d), want (1, 5)", packets, bytes)
	}
}

func TestCircuitIsClosedReflectsCloseCall(t *testing.T) {
	hops := []Hop{{NodeKey: []byte("node-a"), Key: make([]byte, 32)}}
	c, err := NewCircuit(hops, time.Minute, 100, 100000)
	if err != nil {
		t.Fatalf("NewCircuit returned error: %v", err)
	}
	if c.IsClosed() {
		t.Fatal("IsClosed() = true before Close(), want false")
	}
	c.Close()
	if !c.IsClosed() {
		t.Fatal("IsClosed() = false after Close(), want true")
	}
}

func TestRandomCircuitIDsAreNotDuplicated(t *testing.T) {
	ids := make(map[CircuitID]bool)
	for i := 0; i < 1000; i++ {
		id, err := randomCircuitID()
		if err != nil {
			t.Fatalf("randomCircuitID returned error: %v", err)
		}
		if ids[id] {
			t.Fatalf("randomCircuitID produced a duplicate after %d draws", i)
		}
		ids[id] = true
	}
}

func TestSealHopLocalReturnsFirstLegMetadata(t *testing.T) {
	hops := testHops(2)
	hops[0].LocalCircuitID = CircuitID{9, 9, 9}
	hops[0].Counter = 500
	hops[1].LocalCircuitID = CircuitID{8, 8, 8}
	hops[1].Counter = 900
	c, err := NewCircuit(hops, time.Minute, 100, 100000)
	if err != nil {
		t.Fatalf("NewCircuit returned error: %v", err)
	}

	onion, firstHop, circuitID, counter, expiration, err := c.SealHopLocal([]byte("hi"), 60*time.Second)
	if err != nil {
		t.Fatalf("SealHopLocal returned error: %v", err)
	}
	if circuitID != (CircuitID{9, 9, 9}) {
		t.Fatalf("circuitID = %x, want hops[0].LocalCircuitID", circuitID)
	}
	if counter != 500 {
		t.Fatalf("counter = %d, want 500 (hops[0]'s pre-increment Counter)", counter)
	}
	if expiration == 0 {
		t.Fatal("expiration = 0, want a real Unix timestamp")
	}
	if !bytes.Equal(firstHop, hops[0].NodeKey) {
		t.Fatalf("firstHop = %x, want %x", firstHop, hops[0].NodeKey)
	}
	if len(onion) == 0 {
		t.Fatal("onion is empty")
	}

	// A second call must use the now-incremented counters, still
	// independently per hop (not reset, not resynced across hops).
	_, _, _, counter2, _, err := c.SealHopLocal([]byte("second"), 60*time.Second)
	if err != nil {
		t.Fatalf("SealHopLocal (second call) returned error: %v", err)
	}
	if counter2 != 501 {
		t.Fatalf("counter (second call) = %d, want 501", counter2)
	}
}

func TestRandomCounterOffsetVaries(t *testing.T) {
	a, err := randomCounterOffset()
	if err != nil {
		t.Fatalf("randomCounterOffset returned error: %v", err)
	}
	b, err := randomCounterOffset()
	if err != nil {
		t.Fatalf("randomCounterOffset returned error: %v", err)
	}
	if a == b {
		t.Fatal("randomCounterOffset returned the same value twice in a row - not actually random")
	}
}
