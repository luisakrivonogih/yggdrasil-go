package garlic

// Long-term Garlic identities (Phase 8 of the roadmap, extended by the
// crypto hardening pass): a node's X25519 keypair (circuit-hop ECDH,
// unchanged from before) plus an independently generated Ed25519
// keypair used only to sign service descriptors (Part 3 of the
// hardening task - see docs/superpowers/specs/
// 2026-08-09-garlic-crypto-hardening-design.md section D). The two
// keypairs are always generated/loaded together but never derived one
// from the other - compromise of one type does not implicate the other,
// and there is no ad-hoc X25519-from-Ed25519 (or reverse) conversion
// anywhere in this file.

import (
	"crypto/ed25519"
	"errors"
)

var (
	ErrInvalidIdentityKeySize = errors.New("garlic: identity key has invalid size")
	ErrInvalidSigningKeySeed  = errors.New("garlic: signing private key seed has invalid size")
)

// Identity is a node's long-term Garlic identity: an X25519 keypair for
// circuit-hop ECDH, and an independent Ed25519 keypair for signing
// service descriptors.
type Identity struct {
	PublicKey  []byte // X25519
	PrivateKey []byte // X25519

	SigningPublicKey  ed25519.PublicKey
	SigningPrivateKey ed25519.PrivateKey
}

// NewIdentity generates a fresh long-term Garlic identity: a new X25519
// keypair and a new, independent Ed25519 signing keypair.
func NewIdentity() (*Identity, error) {
	pub, priv, err := GenerateKeypair()
	if err != nil {
		return nil, err
	}
	signingPub, signingPriv, err := ed25519.GenerateKey(nil)
	if err != nil {
		return nil, err
	}
	return &Identity{
		PublicKey:         pub,
		PrivateKey:        priv,
		SigningPublicKey:  signingPub,
		SigningPrivateKey: signingPriv,
	}, nil
}

// LoadIdentity reconstructs an Identity from previously-persisted key
// material, validating every size. signingPrivateKeySeed is the 32-byte
// Ed25519 seed (not the 64-byte expanded private key) - the same
// persisted-secret shape as the X25519 privateKey, for a consistent
// config format.
func LoadIdentity(publicKey, privateKey, signingPublicKey, signingPrivateKeySeed []byte) (*Identity, error) {
	if len(publicKey) != KeySize || len(privateKey) != KeySize {
		return nil, ErrInvalidIdentityKeySize
	}
	if len(signingPublicKey) != ed25519.PublicKeySize {
		return nil, ErrInvalidIdentityKeySize
	}
	if len(signingPrivateKeySeed) != ed25519.SeedSize {
		return nil, ErrInvalidSigningKeySeed
	}
	return &Identity{
		PublicKey:         append([]byte(nil), publicKey...),
		PrivateKey:        append([]byte(nil), privateKey...),
		SigningPublicKey:  append(ed25519.PublicKey(nil), signingPublicKey...),
		SigningPrivateKey: ed25519.NewKeyFromSeed(signingPrivateKeySeed),
	}, nil
}

// LoadIdentityFromPrivateKeys reconstructs an Identity from just the two
// private secrets, deriving both matching public keys. This is what
// lets config persist two 32-byte secrets (the X25519 private scalar
// and the Ed25519 seed) for a stable Garlic identity across restarts,
// the same way the node's main Yggdrasil identity only persists a
// private key. The two secrets are independently generated and loaded
// independently here - neither is ever derived from the other.
func LoadIdentityFromPrivateKeys(privateKey, signingPrivateKeySeed []byte) (*Identity, error) {
	if len(privateKey) != KeySize {
		return nil, ErrInvalidIdentityKeySize
	}
	if len(signingPrivateKeySeed) != ed25519.SeedSize {
		return nil, ErrInvalidSigningKeySeed
	}
	publicKey, err := DerivePublicKey(privateKey)
	if err != nil {
		return nil, err
	}
	signingPrivateKey := ed25519.NewKeyFromSeed(signingPrivateKeySeed)
	return &Identity{
		PublicKey:         publicKey,
		PrivateKey:        append([]byte(nil), privateKey...),
		SigningPublicKey:  signingPrivateKey.Public().(ed25519.PublicKey),
		SigningPrivateKey: signingPrivateKey,
	}, nil
}

// LoadIdentityFromPrivateKey reconstructs just the X25519 half of an
// Identity from a persisted private key (deriving its public key, like
// LoadIdentityFromPrivateKeys does for both halves), and generates a
// fresh, independent Ed25519 signing keypair rather than loading one.
//
// This is the upgrade path for a node that already had a stable
// Garlic.PrivateKey configured before Garlic.SigningPrivateKey existed:
// its X25519 identity carries over unchanged, at the cost of a signing
// identity (and thus GID, for any service it publishes) that is fresh
// every run until Garlic.SigningPrivateKey is also configured.
func LoadIdentityFromPrivateKey(privateKey []byte) (*Identity, error) {
	if len(privateKey) != KeySize {
		return nil, ErrInvalidIdentityKeySize
	}
	publicKey, err := DerivePublicKey(privateKey)
	if err != nil {
		return nil, err
	}
	signingPub, signingPriv, err := ed25519.GenerateKey(nil)
	if err != nil {
		return nil, err
	}
	return &Identity{
		PublicKey:         publicKey,
		PrivateKey:        append([]byte(nil), privateKey...),
		SigningPublicKey:  signingPub,
		SigningPrivateKey: signingPriv,
	}, nil
}
