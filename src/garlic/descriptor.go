package garlic

// Signed service descriptors (Part 3 of the hardening task): the
// authenticated binding between a GID and the introduction points a
// client should trust for it. A Rendezvous implementation is untrusted
// storage/relay - it can withhold, reorder, or serve a stale copy, but
// it cannot forge a descriptor for a GID it doesn't hold the signing
// key for, because the GID is derived from the signing public key
// (self-certifying, ComputeGID) and the descriptor is Ed25519-signed by
// that same key. See docs/superpowers/specs/
// 2026-08-09-garlic-crypto-hardening-design.md section D for the full
// rationale, in particular what is and isn't part of the signed
// payload - no rendezvous-added metadata is ever signed.

import (
	"crypto/ed25519"
	"encoding/binary"
	"errors"
)

const (
	maxServiceIDSize = 64
	// MaxDescriptorLifetime bounds ExpiresAt-PublishedAt (seconds) so a
	// service can't mint a descriptor "valid" for an unreasonable span.
	MaxDescriptorLifetime = 7 * 24 * 60 * 60
)

const ServiceDescriptorVersion1 uint8 = 1

var (
	ErrServiceIDTooLarge            = errors.New("garlic: service ID exceeds maximum size")
	ErrUnsupportedDescriptorVersion = errors.New("garlic: unsupported service descriptor version")
	ErrInvalidSigningKeySize        = errors.New("garlic: invalid signing public key size")
	ErrDescriptorLifetimeTooLong    = errors.New("garlic: service descriptor lifetime exceeds maximum")
	ErrInvalidDescriptorSignature   = errors.New("garlic: service descriptor signature invalid")
	ErrDescriptorGIDMismatch        = errors.New("garlic: service descriptor does not match requested GID")
	ErrDescriptorExpired            = errors.New("garlic: service descriptor expired")
)

// ServiceDescriptor is the signed, self-certifying binding between a
// service's GID and its current introduction points.
type ServiceDescriptor struct {
	Version          uint8
	ServicePublicKey ed25519.PublicKey // GID = ComputeGID(ServicePublicKey, ServiceID)
	ServiceID        []byte
	IntroPoints      []IntroPoint
	PublishedAt      uint64
	ExpiresAt        uint64
	Signature        []byte // ed25519, over signedBytes()
}

// signedBytes returns the descriptor's canonical encoding with
// Signature omitted - exactly what SignServiceDescriptor signs and what
// VerifyServiceDescriptor re-derives from a received descriptor to
// check the signature against. No field the rendezvous itself might add
// (receipt timestamps, sequence numbers, storage hints) is ever part of
// this encoding.
func (d *ServiceDescriptor) signedBytes() ([]byte, error) {
	if len(d.ServicePublicKey) != ed25519.PublicKeySize {
		return nil, ErrInvalidSigningKeySize
	}
	if len(d.ServiceID) > maxServiceIDSize {
		return nil, ErrServiceIDTooLarge
	}
	if len(d.IntroPoints) > MaxIntroPoints {
		return nil, ErrTooManyIntroPoints
	}

	buf := []byte{d.Version}
	buf = append(buf, d.ServicePublicKey...)
	buf = binary.BigEndian.AppendUint32(buf, uint32(len(d.ServiceID)))
	buf = append(buf, d.ServiceID...)
	buf = binary.BigEndian.AppendUint32(buf, uint32(len(d.IntroPoints)))
	for _, p := range d.IntroPoints {
		if len(p.NodeKey) > maxCapabilityKeyLen {
			return nil, ErrCapabilityKeyTooLong
		}
		buf = append(buf, byte(len(p.NodeKey)))
		buf = append(buf, p.NodeKey...)
	}
	buf = binary.BigEndian.AppendUint64(buf, d.PublishedAt)
	buf = binary.BigEndian.AppendUint64(buf, d.ExpiresAt)
	return buf, nil
}

// SignServiceDescriptor builds and signs a ServiceDescriptor for
// serviceID/introPoints, valid from publishedAt to expiresAt (span
// capped at MaxDescriptorLifetime), using signingPrivateKey.
func SignServiceDescriptor(signingPublicKey ed25519.PublicKey, signingPrivateKey ed25519.PrivateKey, serviceID []byte, introPoints []IntroPoint, publishedAt, expiresAt uint64) (*ServiceDescriptor, error) {
	if expiresAt < publishedAt || expiresAt-publishedAt > MaxDescriptorLifetime {
		return nil, ErrDescriptorLifetimeTooLong
	}
	d := &ServiceDescriptor{
		Version:          ServiceDescriptorVersion1,
		ServicePublicKey: signingPublicKey,
		ServiceID:        serviceID,
		IntroPoints:      introPoints,
		PublishedAt:      publishedAt,
		ExpiresAt:        expiresAt,
	}
	toSign, err := d.signedBytes()
	if err != nil {
		return nil, err
	}
	d.Signature = ed25519.Sign(signingPrivateKey, toSign)
	return d, nil
}

// VerifyServiceDescriptor checks that d is a validly-signed descriptor
// for gid, not expired as of now. This is the client-side trust
// boundary: Rendezvous.Lookup returns d unverified (the rendezvous is
// untrusted), and every caller of Lookup must run the result through
// this before trusting d.IntroPoints.
func VerifyServiceDescriptor(d *ServiceDescriptor, gid GID, now uint64) error {
	if d.Version != ServiceDescriptorVersion1 {
		return ErrUnsupportedDescriptorVersion
	}
	if ComputeGID(d.ServicePublicKey, d.ServiceID) != gid {
		return ErrDescriptorGIDMismatch
	}
	toVerify, err := d.signedBytes()
	if err != nil {
		return err
	}
	if !ed25519.Verify(d.ServicePublicKey, toVerify, d.Signature) {
		return ErrInvalidDescriptorSignature
	}
	if now > d.ExpiresAt {
		return ErrDescriptorExpired
	}
	return nil
}
