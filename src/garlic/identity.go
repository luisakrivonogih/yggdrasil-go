package garlic

// Long-term Garlic identity (Phase 8 of the roadmap): an X25519 keypair
// independent of the node's Yggdrasil ed25519 identity (see
// docs/garlic-architecture.md §1.1/§3.9), so compromise of one never
// implicates the other. Ephemeral per-circuit keys are generated
// separately, per circuit, via GenerateKeypair/ECDH - an Identity is only
// ever the stable, long-term key a Garlic service is known by.

import "errors"

var ErrInvalidIdentityKeySize = errors.New("garlic: identity key has invalid size")

// Identity is a long-term Garlic X25519 keypair.
type Identity struct {
	PublicKey  []byte
	PrivateKey []byte
}

// NewIdentity generates a fresh long-term Garlic identity.
func NewIdentity() (*Identity, error) {
	pub, priv, err := GenerateKeypair()
	if err != nil {
		return nil, err
	}
	return &Identity{PublicKey: pub, PrivateKey: priv}, nil
}

// LoadIdentity reconstructs an Identity from previously-persisted key
// material (e.g. from config), validating key sizes.
func LoadIdentity(publicKey, privateKey []byte) (*Identity, error) {
	if len(publicKey) != KeySize || len(privateKey) != KeySize {
		return nil, ErrInvalidIdentityKeySize
	}
	return &Identity{
		PublicKey:  append([]byte(nil), publicKey...),
		PrivateKey: append([]byte(nil), privateKey...),
	}, nil
}
