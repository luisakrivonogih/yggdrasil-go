package garlic

import (
	"bytes"
	"testing"
)

func testDescriptor(t *testing.T, id *Identity, serviceID string, points []IntroPoint, publishedAt, expiresAt uint64) (*ServiceDescriptor, GID) {
	t.Helper()
	d, err := SignServiceDescriptor(id.SigningPublicKey, id.SigningPrivateKey, []byte(serviceID), points, publishedAt, expiresAt)
	if err != nil {
		t.Fatalf("SignServiceDescriptor returned error: %v", err)
	}
	return d, ComputeGID(id.SigningPublicKey, []byte(serviceID))
}

func TestStaticRendezvousPublishThenLookup(t *testing.T) {
	id, err := NewIdentity()
	if err != nil {
		t.Fatalf("NewIdentity returned error: %v", err)
	}
	r := NewStaticRendezvous()
	points := []IntroPoint{{NodeKey: []byte("intro-1")}, {NodeKey: []byte("intro-2")}}
	d, gid := testDescriptor(t, id, "svc", points, 1000, 2000)

	if err := r.Publish(gid, d); err != nil {
		t.Fatalf("Publish returned error: %v", err)
	}
	got, err := r.Lookup(gid)
	if err != nil {
		t.Fatalf("Lookup returned error: %v", err)
	}
	if len(got.IntroPoints) != len(points) {
		t.Fatalf("Lookup returned %d intro points, want %d", len(got.IntroPoints), len(points))
	}
	for i := range points {
		if !bytes.Equal(got.IntroPoints[i].NodeKey, points[i].NodeKey) {
			t.Errorf("intro point %d = %q, want %q", i, got.IntroPoints[i].NodeKey, points[i].NodeKey)
		}
	}
	if err := VerifyServiceDescriptor(got, gid, 1500); err != nil {
		t.Errorf("VerifyServiceDescriptor on the round-tripped descriptor returned error: %v", err)
	}
}

func TestStaticRendezvousLookupUnpublishedReturnsError(t *testing.T) {
	r := NewStaticRendezvous()
	id, _ := NewIdentity()
	gid := ComputeGID(id.SigningPublicKey, []byte("svc"))
	if _, err := r.Lookup(gid); err == nil {
		t.Fatal("expected error looking up an unpublished GID, got nil")
	}
}

func TestStaticRendezvousPublishOverwritesPreviousEntry(t *testing.T) {
	id, _ := NewIdentity()
	r := NewStaticRendezvous()
	old, gid := testDescriptor(t, id, "svc", []IntroPoint{{NodeKey: []byte("old")}}, 1000, 2000)
	if err := r.Publish(gid, old); err != nil {
		t.Fatalf("first Publish returned error: %v", err)
	}
	fresh, _ := testDescriptor(t, id, "svc", []IntroPoint{{NodeKey: []byte("new")}}, 1500, 2500)
	if err := r.Publish(gid, fresh); err != nil {
		t.Fatalf("second Publish returned error: %v", err)
	}
	got, err := r.Lookup(gid)
	if err != nil {
		t.Fatalf("Lookup returned error: %v", err)
	}
	if len(got.IntroPoints) != 1 || !bytes.Equal(got.IntroPoints[0].NodeKey, []byte("new")) {
		t.Fatalf("Lookup = %+v, want a single intro point %q", got.IntroPoints, "new")
	}
}

func TestStaticRendezvousPublishRejectsTooManyIntroPoints(t *testing.T) {
	id, _ := NewIdentity()
	r := NewStaticRendezvous()
	points := make([]IntroPoint, MaxIntroPoints+1)
	for i := range points {
		points[i] = IntroPoint{NodeKey: []byte{byte(i)}}
	}
	d := &ServiceDescriptor{ServicePublicKey: id.SigningPublicKey, ServiceID: []byte("svc"), IntroPoints: points}
	gid := ComputeGID(id.SigningPublicKey, []byte("svc"))
	if err := r.Publish(gid, d); err == nil {
		t.Fatal("expected error publishing more than MaxIntroPoints, got nil")
	}
}

// TestStaticRendezvousServesStaleDescriptorUncritically documents the
// deliberate trust boundary: StaticRendezvous is untrusted storage, so
// it hands back exactly what was published even after ExpiresAt has
// passed - enforcement of freshness is the *client's* job
// (VerifyServiceDescriptor), not the rendezvous's. This is what makes
// the "malicious/buggy rendezvous serves a stale descriptor" scenario
// (Part 3 of the hardening task) actually testable end to end.
func TestStaticRendezvousServesStaleDescriptorUncritically(t *testing.T) {
	id, _ := NewIdentity()
	r := NewStaticRendezvous()
	d, gid := testDescriptor(t, id, "svc", []IntroPoint{{NodeKey: []byte("intro")}}, 1000, 2000)
	if err := r.Publish(gid, d); err != nil {
		t.Fatalf("Publish returned error: %v", err)
	}

	got, err := r.Lookup(gid)
	if err != nil {
		t.Fatalf("Lookup on a stale-but-present entry returned error: %v, want the entry returned uncritically", err)
	}
	if err := VerifyServiceDescriptor(got, gid, 9999); err == nil {
		t.Fatal("expected the client's own VerifyServiceDescriptor to reject the now-expired descriptor, got nil")
	}
}

// Rendezvous is implemented by StaticRendezvous; this is a compile-time
// check that the interface and implementation stay in sync.
var _ Rendezvous = (*StaticRendezvous)(nil)
