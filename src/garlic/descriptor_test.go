package garlic

import (
	"bytes"
	"testing"
)

func testDescriptorIdentity(t *testing.T) *Identity {
	t.Helper()
	id, err := NewIdentity()
	if err != nil {
		t.Fatalf("NewIdentity returned error: %v", err)
	}
	return id
}

func TestSignAndVerifyServiceDescriptorRoundTrip(t *testing.T) {
	id := testDescriptorIdentity(t)
	serviceID := []byte("my-service")
	points := []IntroPoint{{NodeKey: []byte("intro-1")}, {NodeKey: []byte("intro-2")}}

	d, err := SignServiceDescriptor(id.SigningPublicKey, id.SigningPrivateKey, serviceID, points, 1000, 2000)
	if err != nil {
		t.Fatalf("SignServiceDescriptor returned error: %v", err)
	}
	gid := ComputeGID(id.SigningPublicKey, serviceID)

	if err := VerifyServiceDescriptor(d, gid, 1500); err != nil {
		t.Fatalf("VerifyServiceDescriptor returned error: %v", err)
	}
}

func TestVerifyServiceDescriptorRejectsWrongServiceKey(t *testing.T) {
	realID := testDescriptorIdentity(t)
	attackerID := testDescriptorIdentity(t)
	serviceID := []byte("victim-service")
	points := []IntroPoint{{NodeKey: []byte("attacker-controlled-intro")}}

	// The attacker signs a descriptor with their own key, but claims to
	// be publishing under the victim's GID by computing the GID from
	// their own key/serviceID pair - which necessarily produces a
	// *different* GID (self-certifying), not the victim's.
	forged, err := SignServiceDescriptor(attackerID.SigningPublicKey, attackerID.SigningPrivateKey, serviceID, points, 1000, 2000)
	if err != nil {
		t.Fatalf("SignServiceDescriptor returned error: %v", err)
	}
	victimGID := ComputeGID(realID.SigningPublicKey, serviceID)

	if err := VerifyServiceDescriptor(forged, victimGID, 1500); err == nil {
		t.Fatal("expected error verifying an attacker-signed descriptor against the victim's GID, got nil")
	}
}

func TestVerifyServiceDescriptorRejectsForgedSignature(t *testing.T) {
	id := testDescriptorIdentity(t)
	serviceID := []byte("svc")
	points := []IntroPoint{{NodeKey: []byte("intro-1")}}

	d, err := SignServiceDescriptor(id.SigningPublicKey, id.SigningPrivateKey, serviceID, points, 1000, 2000)
	if err != nil {
		t.Fatalf("SignServiceDescriptor returned error: %v", err)
	}
	// Tamper with an intro point after signing - a bogus rendezvous
	// substituting its own introduction point must be caught here.
	d.IntroPoints[0].NodeKey = []byte("attacker-substituted-intro")
	gid := ComputeGID(id.SigningPublicKey, serviceID)

	if err := VerifyServiceDescriptor(d, gid, 1500); err == nil {
		t.Fatal("expected error verifying a descriptor with a tampered intro point, got nil")
	}
}

func TestVerifyServiceDescriptorRejectsModifiedSignatureBytes(t *testing.T) {
	id := testDescriptorIdentity(t)
	serviceID := []byte("svc")
	d, err := SignServiceDescriptor(id.SigningPublicKey, id.SigningPrivateKey, serviceID, nil, 1000, 2000)
	if err != nil {
		t.Fatalf("SignServiceDescriptor returned error: %v", err)
	}
	d.Signature[0] ^= 0xFF
	gid := ComputeGID(id.SigningPublicKey, serviceID)

	if err := VerifyServiceDescriptor(d, gid, 1500); err == nil {
		t.Fatal("expected error verifying a descriptor with corrupted signature bytes, got nil")
	}
}

func TestVerifyServiceDescriptorRejectsExpired(t *testing.T) {
	id := testDescriptorIdentity(t)
	serviceID := []byte("svc")
	d, err := SignServiceDescriptor(id.SigningPublicKey, id.SigningPrivateKey, serviceID, nil, 1000, 2000)
	if err != nil {
		t.Fatalf("SignServiceDescriptor returned error: %v", err)
	}
	gid := ComputeGID(id.SigningPublicKey, serviceID)

	if err := VerifyServiceDescriptor(d, gid, 2001); err == nil {
		t.Fatal("expected error verifying an expired descriptor, got nil")
	}
}

func TestVerifyServiceDescriptorRejectsWrongGID(t *testing.T) {
	id := testDescriptorIdentity(t)
	d, err := SignServiceDescriptor(id.SigningPublicKey, id.SigningPrivateKey, []byte("svc-a"), nil, 1000, 2000)
	if err != nil {
		t.Fatalf("SignServiceDescriptor returned error: %v", err)
	}
	wrongGID := ComputeGID(id.SigningPublicKey, []byte("svc-b"))

	if err := VerifyServiceDescriptor(d, wrongGID, 1500); err == nil {
		t.Fatal("expected error verifying a valid descriptor against an unrelated GID, got nil")
	}
}

func TestSignServiceDescriptorRejectsExcessiveLifetime(t *testing.T) {
	id := testDescriptorIdentity(t)
	if _, err := SignServiceDescriptor(id.SigningPublicKey, id.SigningPrivateKey, []byte("svc"), nil, 1000, 1000+MaxDescriptorLifetime+1); err == nil {
		t.Fatal("expected error for a descriptor lifetime exceeding MaxDescriptorLifetime, got nil")
	}
}

func TestSignServiceDescriptorRejectsTooManyIntroPoints(t *testing.T) {
	id := testDescriptorIdentity(t)
	points := make([]IntroPoint, MaxIntroPoints+1)
	for i := range points {
		points[i] = IntroPoint{NodeKey: []byte{byte(i)}}
	}
	if _, err := SignServiceDescriptor(id.SigningPublicKey, id.SigningPrivateKey, []byte("svc"), points, 1000, 2000); err == nil {
		t.Fatal("expected error for too many introduction points, got nil")
	}
}

func TestSignedBytesExcludeSignatureField(t *testing.T) {
	id := testDescriptorIdentity(t)
	d, err := SignServiceDescriptor(id.SigningPublicKey, id.SigningPrivateKey, []byte("svc"), nil, 1000, 2000)
	if err != nil {
		t.Fatalf("SignServiceDescriptor returned error: %v", err)
	}
	signed, err := d.signedBytes()
	if err != nil {
		t.Fatalf("signedBytes returned error: %v", err)
	}
	if bytes.Contains(signed, d.Signature) {
		t.Error("signedBytes includes the Signature field itself - the signature would cover its own bytes")
	}
}
