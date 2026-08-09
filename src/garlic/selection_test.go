package garlic

import "testing"

func TestSelectDiversePathPrefersFartherHops(t *testing.T) {
	pool := []HopCandidate{
		{NodeKey: []byte("near"), HopCount: 1},
		{NodeKey: []byte("mid"), HopCount: 5},
		{NodeKey: []byte("far"), HopCount: 10},
	}
	selected, err := SelectDiversePath(pool, 2, 0)
	if err != nil {
		t.Fatalf("SelectDiversePath returned error: %v", err)
	}
	if len(selected) != 2 {
		t.Fatalf("got %d candidates, want 2", len(selected))
	}
	if string(selected[0].NodeKey) != "far" || string(selected[1].NodeKey) != "mid" {
		t.Fatalf("selected = %q, %q; want \"far\" then \"mid\" (farthest first)", selected[0].NodeKey, selected[1].NodeKey)
	}
}

func TestSelectDiversePathAvoidsSharedParent(t *testing.T) {
	pool := []HopCandidate{
		{NodeKey: []byte("sibling-1"), HopCount: 10, TreeParent: []byte("parent-x")},
		{NodeKey: []byte("sibling-2"), HopCount: 9, TreeParent: []byte("parent-x")},
		{NodeKey: []byte("other"), HopCount: 8, TreeParent: []byte("parent-y")},
	}
	selected, err := SelectDiversePath(pool, 2, 0)
	if err != nil {
		t.Fatalf("SelectDiversePath returned error: %v", err)
	}
	if len(selected) != 2 {
		t.Fatalf("got %d candidates, want 2", len(selected))
	}
	parents := map[string]bool{}
	for _, c := range selected {
		if parents[string(c.TreeParent)] {
			t.Fatalf("two selected hops share TreeParent %q, want at most one per parent", c.TreeParent)
		}
		parents[string(c.TreeParent)] = true
	}
	// The highest-hop-count sibling should win over its sibling, and the
	// "other" candidate (different parent) must be the second pick.
	if string(selected[0].NodeKey) != "sibling-1" {
		t.Fatalf("selected[0] = %q, want %q (highest hop count)", selected[0].NodeKey, "sibling-1")
	}
	if string(selected[1].NodeKey) != "other" {
		t.Fatalf("selected[1] = %q, want %q (sibling-2 excluded as a same-parent duplicate)", selected[1].NodeKey, "other")
	}
}

func TestSelectDiversePathFiltersByMinHopCount(t *testing.T) {
	pool := []HopCandidate{
		{NodeKey: []byte("too-close"), HopCount: 1},
		{NodeKey: []byte("far-enough"), HopCount: 5},
	}
	selected, err := SelectDiversePath(pool, 1, 3)
	if err != nil {
		t.Fatalf("SelectDiversePath returned error: %v", err)
	}
	if len(selected) != 1 || string(selected[0].NodeKey) != "far-enough" {
		t.Fatalf("selected = %+v, want only %q (below minHopCount excluded)", selected, "far-enough")
	}
}

func TestSelectDiversePathErrorsWhenNotEnoughCandidates(t *testing.T) {
	pool := []HopCandidate{
		{NodeKey: []byte("only-one"), HopCount: 5},
	}
	if _, err := SelectDiversePath(pool, 3, 0); err == nil {
		t.Fatal("expected error when the pool has fewer candidates than requested, got nil")
	}
}

func TestSelectDiversePathUnknownParentsDoNotConflict(t *testing.T) {
	pool := []HopCandidate{
		{NodeKey: []byte("a"), HopCount: 10}, // TreeParent unset for both
		{NodeKey: []byte("b"), HopCount: 9},
	}
	selected, err := SelectDiversePath(pool, 2, 0)
	if err != nil {
		t.Fatalf("SelectDiversePath returned error: %v (unknown parents should not be treated as a conflict)", err)
	}
	if len(selected) != 2 {
		t.Fatalf("got %d candidates, want 2", len(selected))
	}
}
