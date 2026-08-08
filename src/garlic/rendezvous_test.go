package garlic

import (
	"bytes"
	"testing"
	"time"
)

func TestStaticRendezvousPublishThenLookup(t *testing.T) {
	r := NewStaticRendezvous()
	gid := ComputeGID([]byte("pub"), []byte("svc"))
	points := []IntroPoint{{NodeKey: []byte("intro-1")}, {NodeKey: []byte("intro-2")}}

	if err := r.Publish(gid, points, time.Minute); err != nil {
		t.Fatalf("Publish returned error: %v", err)
	}
	got, err := r.Lookup(gid)
	if err != nil {
		t.Fatalf("Lookup returned error: %v", err)
	}
	if len(got) != len(points) {
		t.Fatalf("Lookup returned %d intro points, want %d", len(got), len(points))
	}
	for i := range points {
		if !bytes.Equal(got[i].NodeKey, points[i].NodeKey) {
			t.Errorf("intro point %d = %q, want %q", i, got[i].NodeKey, points[i].NodeKey)
		}
	}
}

func TestStaticRendezvousLookupUnpublishedReturnsError(t *testing.T) {
	r := NewStaticRendezvous()
	gid := ComputeGID([]byte("pub"), []byte("svc"))
	if _, err := r.Lookup(gid); err == nil {
		t.Fatal("expected error looking up an unpublished GID, got nil")
	}
}

func TestStaticRendezvousLookupExpiredReturnsError(t *testing.T) {
	r := NewStaticRendezvous()
	gid := ComputeGID([]byte("pub"), []byte("svc"))
	if err := r.Publish(gid, []IntroPoint{{NodeKey: []byte("intro-1")}}, time.Millisecond); err != nil {
		t.Fatalf("Publish returned error: %v", err)
	}
	time.Sleep(5 * time.Millisecond)

	if _, err := r.Lookup(gid); err == nil {
		t.Fatal("expected error looking up an expired publication, got nil")
	}
}

func TestStaticRendezvousPublishOverwritesPreviousEntry(t *testing.T) {
	r := NewStaticRendezvous()
	gid := ComputeGID([]byte("pub"), []byte("svc"))
	if err := r.Publish(gid, []IntroPoint{{NodeKey: []byte("old")}}, time.Minute); err != nil {
		t.Fatalf("first Publish returned error: %v", err)
	}
	if err := r.Publish(gid, []IntroPoint{{NodeKey: []byte("new")}}, time.Minute); err != nil {
		t.Fatalf("second Publish returned error: %v", err)
	}
	got, err := r.Lookup(gid)
	if err != nil {
		t.Fatalf("Lookup returned error: %v", err)
	}
	if len(got) != 1 || !bytes.Equal(got[0].NodeKey, []byte("new")) {
		t.Fatalf("Lookup = %v, want a single intro point %q", got, "new")
	}
}

func TestStaticRendezvousPublishRejectsTooManyIntroPoints(t *testing.T) {
	r := NewStaticRendezvous()
	gid := ComputeGID([]byte("pub"), []byte("svc"))
	points := make([]IntroPoint, MaxIntroPoints+1)
	for i := range points {
		points[i] = IntroPoint{NodeKey: []byte{byte(i)}}
	}
	if err := r.Publish(gid, points, time.Minute); err == nil {
		t.Fatal("expected error publishing more than MaxIntroPoints, got nil")
	}
}

// Rendezvous is implemented by StaticRendezvous; this is a compile-time
// check that the interface and implementation stay in sync.
var _ Rendezvous = (*StaticRendezvous)(nil)
