package garlic

import (
	"testing"
	"time"
)

func testManagerConfig() CircuitManagerConfig {
	return CircuitManagerConfig{
		MaxCircuits:        1024,
		MaxCircuitsPerPeer: 1024,
	}
}

func TestCircuitManagerAddAndGet(t *testing.T) {
	m := NewCircuitManager(testManagerConfig())
	c, err := m.Add(testHops(2), time.Minute, 100, 100000)
	if err != nil {
		t.Fatalf("Add returned error: %v", err)
	}
	got, ok := m.Get(c.ID)
	if !ok {
		t.Fatal("Get() ok = false, want true")
	}
	if got != c {
		t.Error("Get() returned a different circuit than Add()")
	}
}

func TestCircuitManagerGetMissingReturnsFalse(t *testing.T) {
	m := NewCircuitManager(testManagerConfig())
	if _, ok := m.Get(CircuitID(12345)); ok {
		t.Error("Get() on unknown ID ok = true, want false")
	}
}

func TestCircuitManagerEnforcesMaxCircuits(t *testing.T) {
	cfg := testManagerConfig()
	cfg.MaxCircuits = 2
	m := NewCircuitManager(cfg)

	if _, err := m.Add(testHops(1), time.Minute, 100, 100000); err != nil {
		t.Fatalf("Add #1 returned error: %v", err)
	}
	if _, err := m.Add(testHops(1), time.Minute, 100, 100000); err != nil {
		t.Fatalf("Add #2 returned error: %v", err)
	}
	if _, err := m.Add(testHops(1), time.Minute, 100, 100000); err == nil {
		t.Fatal("Add #3 succeeded, want error (MaxCircuits exceeded)")
	}
}

func TestCircuitManagerEnforcesMaxCircuitsPerPeer(t *testing.T) {
	cfg := testManagerConfig()
	cfg.MaxCircuitsPerPeer = 1
	m := NewCircuitManager(cfg)

	sameFirstHop := testHops(1)
	if _, err := m.Add(sameFirstHop, time.Minute, 100, 100000); err != nil {
		t.Fatalf("Add #1 returned error: %v", err)
	}
	if _, err := m.Add(sameFirstHop, time.Minute, 100, 100000); err == nil {
		t.Fatal("Add #2 with the same first hop succeeded, want error (MaxCircuitsPerPeer exceeded)")
	}

	// A circuit through a *different* first hop must still be allowed.
	otherHops := testHops(1)
	otherHops[0].NodeKey = []byte("a-completely-different-node")
	if _, err := m.Add(otherHops, time.Minute, 100, 100000); err != nil {
		t.Fatalf("Add with a different first hop returned error: %v", err)
	}
}

func TestCircuitManagerCloseRemovesCircuit(t *testing.T) {
	m := NewCircuitManager(testManagerConfig())
	c, err := m.Add(testHops(1), time.Minute, 100, 100000)
	if err != nil {
		t.Fatalf("Add returned error: %v", err)
	}
	m.Close(c.ID)

	if _, ok := m.Get(c.ID); ok {
		t.Fatal("Get() after Close() ok = true, want false")
	}
	if _, _, _, err := c.Seal([]byte("payload")); err == nil {
		t.Fatal("Seal() on a manager-closed circuit succeeded, want error")
	}
}

func TestCircuitManagerCloseFreesPerPeerSlot(t *testing.T) {
	cfg := testManagerConfig()
	cfg.MaxCircuitsPerPeer = 1
	m := NewCircuitManager(cfg)

	hops := testHops(1)
	c, err := m.Add(hops, time.Minute, 100, 100000)
	if err != nil {
		t.Fatalf("Add #1 returned error: %v", err)
	}
	m.Close(c.ID)

	if _, err := m.Add(hops, time.Minute, 100, 100000); err != nil {
		t.Fatalf("Add after Close returned error: %v, want success (slot freed)", err)
	}
}

func TestCircuitManagerExpireStaleRemovesExpiredCircuits(t *testing.T) {
	m := NewCircuitManager(testManagerConfig())
	c, err := m.Add(testHops(1), time.Millisecond, 100, 100000)
	if err != nil {
		t.Fatalf("Add returned error: %v", err)
	}
	time.Sleep(5 * time.Millisecond)

	if n := m.ExpireStale(); n != 1 {
		t.Fatalf("ExpireStale() = %d, want 1", n)
	}
	if _, ok := m.Get(c.ID); ok {
		t.Fatal("Get() after ExpireStale() ok = true, want false")
	}
}

func TestCircuitManagerCount(t *testing.T) {
	m := NewCircuitManager(testManagerConfig())
	if m.Count() != 0 {
		t.Fatalf("Count() = %d, want 0", m.Count())
	}
	c, err := m.Add(testHops(1), time.Minute, 100, 100000)
	if err != nil {
		t.Fatalf("Add returned error: %v", err)
	}
	if m.Count() != 1 {
		t.Fatalf("Count() = %d, want 1", m.Count())
	}
	m.Close(c.ID)
	if m.Count() != 0 {
		t.Fatalf("Count() after Close = %d, want 0", m.Count())
	}
}

func TestCircuitManagerExpireStaleLeavesFreshCircuits(t *testing.T) {
	m := NewCircuitManager(testManagerConfig())
	c, err := m.Add(testHops(1), time.Minute, 100, 100000)
	if err != nil {
		t.Fatalf("Add returned error: %v", err)
	}

	if n := m.ExpireStale(); n != 0 {
		t.Fatalf("ExpireStale() = %d, want 0", n)
	}
	if _, ok := m.Get(c.ID); !ok {
		t.Fatal("Get() after ExpireStale() ok = false, want true (circuit still fresh)")
	}
}

func TestCircuitManagerListReturnsAllTrackedCircuits(t *testing.T) {
	m := NewCircuitManager(CircuitManagerConfig{MaxCircuits: 10, MaxCircuitsPerPeer: 10})
	c1, err := m.Add([]Hop{{NodeKey: []byte("peer-a")}}, time.Minute, 100, 100000)
	if err != nil {
		t.Fatalf("Add returned error: %v", err)
	}
	c2, err := m.Add([]Hop{{NodeKey: []byte("peer-b")}}, time.Minute, 100, 100000)
	if err != nil {
		t.Fatalf("Add returned error: %v", err)
	}

	list := m.List()
	if len(list) != 2 {
		t.Fatalf("List() returned %d circuits, want 2", len(list))
	}
	found := map[CircuitID]bool{}
	for _, c := range list {
		found[c.ID] = true
	}
	if !found[c1.ID] || !found[c2.ID] {
		t.Fatalf("List() = %+v, want to include %d and %d", list, c1.ID, c2.ID)
	}
}

func TestCircuitManagerListEmptyWhenNoCircuits(t *testing.T) {
	m := NewCircuitManager(CircuitManagerConfig{MaxCircuits: 10, MaxCircuitsPerPeer: 10})
	if list := m.List(); len(list) != 0 {
		t.Fatalf("List() = %+v, want empty", list)
	}
}
