package garlic

// Capability negotiation message format (Phase 6 of the roadmap, see
// docs/garlic-architecture.md §3.4): an in-band request/response,
// structurally mirroring how src/core's own NodeInfo protocol works,
// reaching any node by key regardless of hop count. A node that never
// responds (or responds without CapabilityGarlicV1) is treated as
// legacy and never selected as a circuit hop or rendezvous point - see
// (*Garlic) in manager.go for the request/response exchange itself; this
// file is only the wire message the two sides exchange.

import "errors"

// CapabilityGarlicV1 is the capability string a Garlic-v1-capable node
// advertises.
const CapabilityGarlicV1 = "garlic-v1"

const (
	maxCapabilityVersions   = 16
	maxCapabilityVersionLen = 32
	maxCapabilityKeyLen     = 64
)

var (
	ErrTooManyCapabilityVersions  = errors.New("garlic: too many capability versions")
	ErrCapabilityVersionTooLong   = errors.New("garlic: capability version string too long")
	ErrCapabilityKeyTooLong       = errors.New("garlic: capability public key too long")
	ErrCapabilityMessageTruncated = errors.New("garlic: capability message truncated")
)

// CapabilityMessage is what a node advertises about itself: which
// protocol versions it supports, and (if any) its Garlic identity public
// key, so a peer that decides to use it as a circuit hop already has the
// key it needs for per-hop ECDH.
type CapabilityMessage struct {
	Versions  []string
	PublicKey []byte
}

// SupportsGarlicV1 reports whether the message advertises
// CapabilityGarlicV1.
func (m *CapabilityMessage) SupportsGarlicV1() bool {
	for _, v := range m.Versions {
		if v == CapabilityGarlicV1 {
			return true
		}
	}
	return false
}

// Marshal encodes the message as: version_count(1), then per version
// len(1)+bytes, then key_len(1)+bytes.
func (m *CapabilityMessage) Marshal() ([]byte, error) {
	if len(m.Versions) > maxCapabilityVersions {
		return nil, ErrTooManyCapabilityVersions
	}
	if len(m.PublicKey) > maxCapabilityKeyLen {
		return nil, ErrCapabilityKeyTooLong
	}
	var buf []byte
	buf = append(buf, byte(len(m.Versions)))
	for _, v := range m.Versions {
		if len(v) > maxCapabilityVersionLen {
			return nil, ErrCapabilityVersionTooLong
		}
		buf = append(buf, byte(len(v)))
		buf = append(buf, v...)
	}
	buf = append(buf, byte(len(m.PublicKey)))
	buf = append(buf, m.PublicKey...)
	return buf, nil
}

// UnmarshalCapabilityMessage decodes a message produced by Marshal, never
// trusting a declared count or length before validating it against both
// the configured maximum and the bytes actually remaining.
func UnmarshalCapabilityMessage(data []byte) (*CapabilityMessage, error) {
	if len(data) < 1 {
		return nil, ErrCapabilityMessageTruncated
	}
	n := int(data[0])
	if n > maxCapabilityVersions {
		return nil, ErrTooManyCapabilityVersions
	}
	rest := data[1:]

	versions := make([]string, 0, n)
	for range n {
		if len(rest) < 1 {
			return nil, ErrCapabilityMessageTruncated
		}
		vlen := int(rest[0])
		rest = rest[1:]
		if vlen > maxCapabilityVersionLen {
			return nil, ErrCapabilityVersionTooLong
		}
		if vlen > len(rest) {
			return nil, ErrCapabilityMessageTruncated
		}
		versions = append(versions, string(rest[:vlen]))
		rest = rest[vlen:]
	}

	if len(rest) < 1 {
		return nil, ErrCapabilityMessageTruncated
	}
	klen := int(rest[0])
	rest = rest[1:]
	if klen > maxCapabilityKeyLen {
		return nil, ErrCapabilityKeyTooLong
	}
	if klen > len(rest) {
		return nil, ErrCapabilityMessageTruncated
	}
	pub := append([]byte(nil), rest[:klen]...)

	return &CapabilityMessage{Versions: versions, PublicKey: pub}, nil
}
