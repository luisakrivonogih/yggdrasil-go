package garlic

import "testing"

func TestCircuitPoolNextCircuitRoundRobin(t *testing.T) {
	p := newCircuitPool([]CircuitID{1, 2, 3})
	want := []CircuitID{1, 2, 3, 1, 2}
	for i, w := range want {
		got, ok := p.nextCircuit()
		if !ok {
			t.Fatalf("call %d: ok = false, want true", i)
		}
		if got != w {
			t.Fatalf("call %d: got %d, want %d", i, got, w)
		}
	}
}

func TestCircuitPoolNextCircuitEmptyPoolReturnsFalse(t *testing.T) {
	p := newCircuitPool(nil)
	if _, ok := p.nextCircuit(); ok {
		t.Fatal("ok = true for an empty pool, want false")
	}
}

func TestCircuitPoolAllReturnsEveryCircuit(t *testing.T) {
	p := newCircuitPool([]CircuitID{5, 6, 7})
	all := p.all()
	if len(all) != 3 {
		t.Fatalf("all() returned %d circuits, want 3", len(all))
	}
}

func TestCircuitPoolAllReturnsDefensiveCopy(t *testing.T) {
	p := newCircuitPool([]CircuitID{1, 2})
	all := p.all()
	all[0] = 999
	again := p.all()
	if again[0] == 999 {
		t.Fatal("mutating all()'s result affected the pool's internal state")
	}
}
