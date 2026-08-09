package garlic

import (
	"bytes"
	"testing"
)

func TestNewIdentityProducesDistinctKeypairs(t *testing.T) {
	id1, err := NewIdentity()
	if err != nil {
		t.Fatalf("NewIdentity returned error: %v", err)
	}
	id2, err := NewIdentity()
	if err != nil {
		t.Fatalf("NewIdentity returned error: %v", err)
	}
	if bytes.Equal(id1.PublicKey, id2.PublicKey) {
		t.Error("two identities got the same X25519 public key")
	}
	if bytes.Equal(id1.PrivateKey, id2.PrivateKey) {
		t.Error("two identities got the same X25519 private key")
	}
	if bytes.Equal(id1.SigningPublicKey, id2.SigningPublicKey) {
		t.Error("two identities got the same Ed25519 signing public key")
	}
}

func TestNewIdentitySigningKeyIsIndependentOfEncryptionKey(t *testing.T) {
	id, err := NewIdentity()
	if err != nil {
		t.Fatalf("NewIdentity returned error: %v", err)
	}
	// The two keypairs must not be trivially related - in particular,
	// the signing public key must not equal the X25519 public key (they
	// are different key types generated independently, never one
	// derived from the other).
	if bytes.Equal(id.PublicKey, id.SigningPublicKey) {
		t.Error("SigningPublicKey equals the X25519 PublicKey - keys are not independent")
	}
}

func TestLoadIdentityRoundTrip(t *testing.T) {
	id, err := NewIdentity()
	if err != nil {
		t.Fatalf("NewIdentity returned error: %v", err)
	}
	loaded, err := LoadIdentity(id.PublicKey, id.PrivateKey, id.SigningPublicKey, id.SigningPrivateKey.Seed())
	if err != nil {
		t.Fatalf("LoadIdentity returned error: %v", err)
	}
	if !bytes.Equal(loaded.PublicKey, id.PublicKey) {
		t.Errorf("PublicKey = %x, want %x", loaded.PublicKey, id.PublicKey)
	}
	if !bytes.Equal(loaded.PrivateKey, id.PrivateKey) {
		t.Errorf("PrivateKey = %x, want %x", loaded.PrivateKey, id.PrivateKey)
	}
	if !bytes.Equal(loaded.SigningPublicKey, id.SigningPublicKey) {
		t.Errorf("SigningPublicKey = %x, want %x", loaded.SigningPublicKey, id.SigningPublicKey)
	}
	if !bytes.Equal(loaded.SigningPrivateKey, id.SigningPrivateKey) {
		t.Errorf("SigningPrivateKey = %x, want %x", loaded.SigningPrivateKey, id.SigningPrivateKey)
	}
}

func TestLoadIdentityRejectsWrongSizePublicKey(t *testing.T) {
	id, _ := NewIdentity()
	if _, err := LoadIdentity(id.PublicKey[:16], id.PrivateKey, id.SigningPublicKey, id.SigningPrivateKey.Seed()); err == nil {
		t.Fatal("expected error for wrong-size public key, got nil")
	}
}

func TestLoadIdentityRejectsWrongSizePrivateKey(t *testing.T) {
	id, _ := NewIdentity()
	if _, err := LoadIdentity(id.PublicKey, id.PrivateKey[:16], id.SigningPublicKey, id.SigningPrivateKey.Seed()); err == nil {
		t.Fatal("expected error for wrong-size private key, got nil")
	}
}

func TestLoadIdentityRejectsWrongSizeSigningKey(t *testing.T) {
	id, _ := NewIdentity()
	if _, err := LoadIdentity(id.PublicKey, id.PrivateKey, id.SigningPublicKey[:16], id.SigningPrivateKey.Seed()); err == nil {
		t.Fatal("expected error for wrong-size signing public key, got nil")
	}
	if _, err := LoadIdentity(id.PublicKey, id.PrivateKey, id.SigningPublicKey, id.SigningPrivateKey.Seed()[:16]); err == nil {
		t.Fatal("expected error for wrong-size signing private key seed, got nil")
	}
}

func TestLoadIdentityFromPrivateKeysDerivesMatchingPublicKeys(t *testing.T) {
	id, err := NewIdentity()
	if err != nil {
		t.Fatalf("NewIdentity returned error: %v", err)
	}
	loaded, err := LoadIdentityFromPrivateKeys(id.PrivateKey, id.SigningPrivateKey.Seed())
	if err != nil {
		t.Fatalf("LoadIdentityFromPrivateKeys returned error: %v", err)
	}
	if !bytes.Equal(loaded.PublicKey, id.PublicKey) {
		t.Errorf("derived X25519 PublicKey = %x, want %x", loaded.PublicKey, id.PublicKey)
	}
	if !bytes.Equal(loaded.SigningPublicKey, id.SigningPublicKey) {
		t.Errorf("derived SigningPublicKey = %x, want %x", loaded.SigningPublicKey, id.SigningPublicKey)
	}
}

func TestLoadIdentityFromPrivateKeysRejectsWrongSize(t *testing.T) {
	id, _ := NewIdentity()
	if _, err := LoadIdentityFromPrivateKeys(make([]byte, 16), id.SigningPrivateKey.Seed()); err == nil {
		t.Fatal("expected error for wrong-size X25519 private key, got nil")
	}
	if _, err := LoadIdentityFromPrivateKeys(id.PrivateKey, make([]byte, 16)); err == nil {
		t.Fatal("expected error for wrong-size signing private key seed, got nil")
	}
}

func TestLoadIdentityFromPrivateKeysNeverDerivesX25519FromEd25519OrViceVersa(t *testing.T) {
	// The two private keys are independently generated - loading from
	// one must not somehow determine the other. Build an identity from
	// two *unrelated* keys and confirm both halves come out exactly as
	// given, not cross-derived.
	x25519ID, err := NewIdentity()
	if err != nil {
		t.Fatalf("NewIdentity returned error: %v", err)
	}
	ed25519ID, err := NewIdentity()
	if err != nil {
		t.Fatalf("NewIdentity returned error: %v", err)
	}
	loaded, err := LoadIdentityFromPrivateKeys(x25519ID.PrivateKey, ed25519ID.SigningPrivateKey.Seed())
	if err != nil {
		t.Fatalf("LoadIdentityFromPrivateKeys returned error: %v", err)
	}
	if !bytes.Equal(loaded.PublicKey, x25519ID.PublicKey) {
		t.Error("X25519 public key does not match the X25519 identity it was loaded from")
	}
	if !bytes.Equal(loaded.SigningPublicKey, ed25519ID.SigningPublicKey) {
		t.Error("Ed25519 signing public key does not match the Ed25519 identity it was loaded from")
	}
}
