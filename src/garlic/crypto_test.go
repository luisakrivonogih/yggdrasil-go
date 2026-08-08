package garlic

import (
	"bytes"
	"testing"
)

func TestDeriveKeyIsDeterministic(t *testing.T) {
	secret := []byte("shared secret material")
	salt := []byte("salt")

	k1, err := DeriveKey(secret, salt, LabelLayerKey)
	if err != nil {
		t.Fatalf("DeriveKey returned error: %v", err)
	}
	k2, err := DeriveKey(secret, salt, LabelLayerKey)
	if err != nil {
		t.Fatalf("DeriveKey returned error: %v", err)
	}
	if !bytes.Equal(k1, k2) {
		t.Errorf("DeriveKey produced different keys for identical inputs: %x != %x", k1, k2)
	}
}

func TestDeriveKeyProducesKeySizeBytes(t *testing.T) {
	key, err := DeriveKey([]byte("secret"), nil, LabelLayerKey)
	if err != nil {
		t.Fatalf("DeriveKey returned error: %v", err)
	}
	if len(key) != KeySize {
		t.Errorf("len(key) = %d, want %d", len(key), KeySize)
	}
}

func TestDeriveKeyDiffersByLabel(t *testing.T) {
	secret := []byte("shared secret material")

	k1, err := DeriveKey(secret, nil, LabelLayerKey)
	if err != nil {
		t.Fatalf("DeriveKey returned error: %v", err)
	}
	k2, err := DeriveKey(secret, nil, LabelCircuitKey)
	if err != nil {
		t.Fatalf("DeriveKey returned error: %v", err)
	}
	if bytes.Equal(k1, k2) {
		t.Error("DeriveKey produced the same key for two different domain-separation labels")
	}
}

func TestDeriveKeyDiffersBySecret(t *testing.T) {
	k1, err := DeriveKey([]byte("secret A"), nil, LabelLayerKey)
	if err != nil {
		t.Fatalf("DeriveKey returned error: %v", err)
	}
	k2, err := DeriveKey([]byte("secret B"), nil, LabelLayerKey)
	if err != nil {
		t.Fatalf("DeriveKey returned error: %v", err)
	}
	if bytes.Equal(k1, k2) {
		t.Error("DeriveKey produced the same key for two different secrets")
	}
}

func TestSealOpenRoundTrip(t *testing.T) {
	key, err := DeriveKey([]byte("secret"), nil, LabelLayerKey)
	if err != nil {
		t.Fatalf("DeriveKey returned error: %v", err)
	}
	plaintext := []byte("attack at dawn")
	aad := []byte("header context")

	ciphertext, err := Seal(key, 1, plaintext, aad)
	if err != nil {
		t.Fatalf("Seal returned error: %v", err)
	}
	got, err := Open(key, 1, ciphertext, aad)
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	if !bytes.Equal(got, plaintext) {
		t.Errorf("Open() = %q, want %q", got, plaintext)
	}
}

func TestOpenRejectsWrongKey(t *testing.T) {
	key1, _ := DeriveKey([]byte("secret A"), nil, LabelLayerKey)
	key2, _ := DeriveKey([]byte("secret B"), nil, LabelLayerKey)

	ciphertext, err := Seal(key1, 1, []byte("plaintext"), nil)
	if err != nil {
		t.Fatalf("Seal returned error: %v", err)
	}
	if _, err := Open(key2, 1, ciphertext, nil); err == nil {
		t.Fatal("expected error opening with the wrong key, got nil")
	}
}

func TestOpenRejectsWrongCounter(t *testing.T) {
	key, _ := DeriveKey([]byte("secret"), nil, LabelLayerKey)

	ciphertext, err := Seal(key, 1, []byte("plaintext"), nil)
	if err != nil {
		t.Fatalf("Seal returned error: %v", err)
	}
	if _, err := Open(key, 2, ciphertext, nil); err == nil {
		t.Fatal("expected error opening with the wrong counter, got nil")
	}
}

func TestOpenRejectsTamperedCiphertext(t *testing.T) {
	key, _ := DeriveKey([]byte("secret"), nil, LabelLayerKey)

	ciphertext, err := Seal(key, 1, []byte("plaintext"), nil)
	if err != nil {
		t.Fatalf("Seal returned error: %v", err)
	}
	ciphertext[0] ^= 0xFF

	if _, err := Open(key, 1, ciphertext, nil); err == nil {
		t.Fatal("expected error opening tampered ciphertext, got nil")
	}
}

func TestOpenRejectsMismatchedAAD(t *testing.T) {
	key, _ := DeriveKey([]byte("secret"), nil, LabelLayerKey)

	ciphertext, err := Seal(key, 1, []byte("plaintext"), []byte("aad A"))
	if err != nil {
		t.Fatalf("Seal returned error: %v", err)
	}
	if _, err := Open(key, 1, ciphertext, []byte("aad B")); err == nil {
		t.Fatal("expected error opening with mismatched aad, got nil")
	}
}

func TestSealRejectsInvalidKeySize(t *testing.T) {
	if _, err := Seal([]byte("too short"), 1, []byte("plaintext"), nil); err == nil {
		t.Fatal("expected error for invalid key size, got nil")
	}
}

func TestOpenRejectsInvalidKeySize(t *testing.T) {
	if _, err := Open([]byte("too short"), 1, []byte("ciphertext"), nil); err == nil {
		t.Fatal("expected error for invalid key size, got nil")
	}
}

func TestSealProducesDifferentCiphertextForDifferentCounters(t *testing.T) {
	key, _ := DeriveKey([]byte("secret"), nil, LabelLayerKey)
	plaintext := []byte("attack at dawn")

	c1, err := Seal(key, 1, plaintext, nil)
	if err != nil {
		t.Fatalf("Seal returned error: %v", err)
	}
	c2, err := Seal(key, 2, plaintext, nil)
	if err != nil {
		t.Fatalf("Seal returned error: %v", err)
	}
	if bytes.Equal(c1, c2) {
		t.Error("Seal produced identical ciphertext for two different counters")
	}
}

func TestGenerateKeypairProducesDistinctKeys(t *testing.T) {
	pub1, priv1, err := GenerateKeypair()
	if err != nil {
		t.Fatalf("GenerateKeypair returned error: %v", err)
	}
	pub2, priv2, err := GenerateKeypair()
	if err != nil {
		t.Fatalf("GenerateKeypair returned error: %v", err)
	}
	if bytes.Equal(pub1, pub2) {
		t.Error("GenerateKeypair produced the same public key twice")
	}
	if bytes.Equal(priv1, priv2) {
		t.Error("GenerateKeypair produced the same private key twice")
	}
}

func TestECDHIsSymmetric(t *testing.T) {
	alicePub, alicePriv, err := GenerateKeypair()
	if err != nil {
		t.Fatalf("GenerateKeypair returned error: %v", err)
	}
	bobPub, bobPriv, err := GenerateKeypair()
	if err != nil {
		t.Fatalf("GenerateKeypair returned error: %v", err)
	}

	aliceShared, err := ECDH(alicePriv, bobPub)
	if err != nil {
		t.Fatalf("ECDH returned error: %v", err)
	}
	bobShared, err := ECDH(bobPriv, alicePub)
	if err != nil {
		t.Fatalf("ECDH returned error: %v", err)
	}
	if !bytes.Equal(aliceShared, bobShared) {
		t.Errorf("ECDH shared secrets differ: alice=%x bob=%x", aliceShared, bobShared)
	}
}

func TestECDHOutputUsableWithDeriveKeyAndSeal(t *testing.T) {
	alicePub, alicePriv, err := GenerateKeypair()
	if err != nil {
		t.Fatalf("GenerateKeypair returned error: %v", err)
	}
	bobPub, bobPriv, err := GenerateKeypair()
	if err != nil {
		t.Fatalf("GenerateKeypair returned error: %v", err)
	}

	aliceShared, err := ECDH(alicePriv, bobPub)
	if err != nil {
		t.Fatalf("ECDH returned error: %v", err)
	}
	bobShared, err := ECDH(bobPriv, alicePub)
	if err != nil {
		t.Fatalf("ECDH returned error: %v", err)
	}

	aliceKey, err := DeriveKey(aliceShared, nil, LabelLayerKey)
	if err != nil {
		t.Fatalf("DeriveKey returned error: %v", err)
	}
	bobKey, err := DeriveKey(bobShared, nil, LabelLayerKey)
	if err != nil {
		t.Fatalf("DeriveKey returned error: %v", err)
	}

	plaintext := []byte("hello bob")
	ciphertext, err := Seal(aliceKey, 1, plaintext, nil)
	if err != nil {
		t.Fatalf("Seal returned error: %v", err)
	}
	got, err := Open(bobKey, 1, ciphertext, nil)
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	if !bytes.Equal(got, plaintext) {
		t.Errorf("Open() = %q, want %q", got, plaintext)
	}
}
