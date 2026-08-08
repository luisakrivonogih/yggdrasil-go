package garlic

// Cryptographic primitives for the Garlic Envelope (Phase 3 of the
// roadmap). This file provides the building blocks - key derivation,
// authenticated encryption of a single layer, and X25519 key
// agreement - that Phase 4's layered (onion) construction composes into
// multi-hop circuits. It does not implement onion peeling, next-hop
// routing instructions, or circuits itself.
//
// Primitive choices (see docs/garlic-architecture.md §3.6 and §15):
//   - Key agreement:    X25519 (golang.org/x/crypto/curve25519)
//   - Key derivation:   HKDF-SHA256, with an explicit domain-separation
//     label per key purpose, so two keys derived from the same secret for
//     different purposes are cryptographically independent.
//   - Authentication/encryption: XChaCha20-Poly1305 AEAD (24-byte nonce),
//     never a bare hash or a custom construction.
//   - Nonce generation: deterministic from the caller-supplied counter.
//     This is safe only because callers are required to never reuse a
//     counter value under the same key - see Seal's doc comment.

import (
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"io"

	"golang.org/x/crypto/chacha20poly1305"
	"golang.org/x/crypto/curve25519"
	"golang.org/x/crypto/hkdf"
)

// KeySize is the size, in bytes, of symmetric keys used by Seal/Open and
// produced by DeriveKey.
const KeySize = chacha20poly1305.KeySize

// Domain-separation labels for HKDF-derived keys. Each distinct key
// purpose must use a distinct label, so that keys derived from the same
// underlying secret (e.g. the same ECDH output) for different purposes
// remain cryptographically independent.
const (
	LabelLayerKey   = "yggdrasil-garlic-v1-layer-key"
	LabelCircuitKey = "yggdrasil-garlic-v1-circuit-key"
)

var (
	ErrInvalidKeySize   = errors.New("garlic: invalid key size")
	ErrDecryptionFailed = errors.New("garlic: decryption failed")
)

// DeriveKey derives a KeySize-byte key from secret using HKDF-SHA256. salt
// may be nil. label provides explicit domain separation between different
// key purposes derived from the same secret (see the Label* constants) and
// must never be empty.
func DeriveKey(secret, salt []byte, label string) ([]byte, error) {
	kdf := hkdf.New(sha256.New, secret, salt, []byte(label))
	key := make([]byte, KeySize)
	if _, err := io.ReadFull(kdf, key); err != nil {
		return nil, err
	}
	return key, nil
}

// Seal encrypts and authenticates plaintext under key using
// XChaCha20-Poly1305, returning ciphertext||tag. aad is authenticated but
// not encrypted, and may be nil.
//
// The nonce is derived deterministically from counter. Callers MUST NOT
// call Seal twice with the same (key, counter) pair, since that would
// reuse a nonce and break the AEAD's confidentiality and authenticity
// guarantees. In practice this means: key must be unique per session/hop
// (e.g. produced by DeriveKey from a fresh ECDH), and counter must be a
// strictly monotonic, never-reused value scoped to that key (e.g.
// Envelope.PacketCounter).
func Seal(key []byte, counter uint64, plaintext, aad []byte) ([]byte, error) {
	aead, err := newAEAD(key)
	if err != nil {
		return nil, err
	}
	nonce := nonceFromCounter(counter)
	return aead.Seal(nil, nonce[:], plaintext, aad), nil
}

// Open decrypts and authenticates ciphertext produced by Seal with the
// same key, counter, and aad. Any failure - wrong key, wrong counter,
// tampered ciphertext, or mismatched aad - is reported as the single
// generic ErrDecryptionFailed, so a remote peer cannot learn which check
// failed.
func Open(key []byte, counter uint64, ciphertext, aad []byte) ([]byte, error) {
	aead, err := newAEAD(key)
	if err != nil {
		return nil, ErrDecryptionFailed
	}
	nonce := nonceFromCounter(counter)
	plaintext, err := aead.Open(nil, nonce[:], ciphertext, aad)
	if err != nil {
		return nil, ErrDecryptionFailed
	}
	return plaintext, nil
}

func newAEAD(key []byte) (cipher.AEAD, error) {
	if len(key) != KeySize {
		return nil, ErrInvalidKeySize
	}
	return chacha20poly1305.NewX(key)
}

func nonceFromCounter(counter uint64) [chacha20poly1305.NonceSizeX]byte {
	var nonce [chacha20poly1305.NonceSizeX]byte
	binary.BigEndian.PutUint64(nonce[len(nonce)-8:], counter)
	return nonce
}

// GenerateKeypair generates a new X25519 keypair, suitable for use as
// either a long-term Garlic identity key or an ephemeral per-circuit key
// (see docs/garlic-architecture.md §3.9).
func GenerateKeypair() (public, private []byte, err error) {
	private = make([]byte, curve25519.ScalarSize)
	if _, err := rand.Read(private); err != nil {
		return nil, nil, err
	}
	public, err = curve25519.X25519(private, curve25519.Basepoint)
	if err != nil {
		return nil, nil, err
	}
	return public, private, nil
}

// ECDH computes the X25519 shared secret between a local private key and a
// remote public key. The result is raw Diffie-Hellman output and must not
// be used directly as a symmetric key - pass it through DeriveKey with an
// appropriate domain-separation label first.
func ECDH(privateKey, publicKey []byte) ([]byte, error) {
	return curve25519.X25519(privateKey, publicKey)
}
