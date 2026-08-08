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
		t.Error("two identities got the same public key")
	}
	if bytes.Equal(id1.PrivateKey, id2.PrivateKey) {
		t.Error("two identities got the same private key")
	}
}

func TestLoadIdentityRoundTrip(t *testing.T) {
	id, err := NewIdentity()
	if err != nil {
		t.Fatalf("NewIdentity returned error: %v", err)
	}
	loaded, err := LoadIdentity(id.PublicKey, id.PrivateKey)
	if err != nil {
		t.Fatalf("LoadIdentity returned error: %v", err)
	}
	if !bytes.Equal(loaded.PublicKey, id.PublicKey) {
		t.Errorf("PublicKey = %x, want %x", loaded.PublicKey, id.PublicKey)
	}
	if !bytes.Equal(loaded.PrivateKey, id.PrivateKey) {
		t.Errorf("PrivateKey = %x, want %x", loaded.PrivateKey, id.PrivateKey)
	}
}

func TestLoadIdentityRejectsWrongSizePublicKey(t *testing.T) {
	id, _ := NewIdentity()
	if _, err := LoadIdentity(id.PublicKey[:16], id.PrivateKey); err == nil {
		t.Fatal("expected error for wrong-size public key, got nil")
	}
}

func TestLoadIdentityRejectsWrongSizePrivateKey(t *testing.T) {
	id, _ := NewIdentity()
	if _, err := LoadIdentity(id.PublicKey, id.PrivateKey[:16]); err == nil {
		t.Fatal("expected error for wrong-size private key, got nil")
	}
}
