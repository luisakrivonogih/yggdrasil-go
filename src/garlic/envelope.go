// Package garlic implements the experimental Garlic Routing Overlay: an
// optional, privacy-enhanced routing layer built on top of the existing
// Yggdrasil mesh. See docs/garlic-architecture.md for the design.
//
// This file implements the Garlic Envelope wire format only (Phase 2 of the
// roadmap): versioned header fields, replay/expiration metadata, an opaque
// encrypted body, and optional padding. It does not implement the
// cryptography that produces/consumes the body (Phase 3), layered
// encryption (Phase 4), or circuits (Phase 5).
package garlic

import (
	"crypto/rand"
	"encoding/binary"
	"errors"
	"math/big"
	"time"
)

// EnvelopeVersion1 is the original Garlic Envelope wire version: CircuitID,
// PacketCounter, and Expiration are chosen once by the circuit originator
// and copied unchanged at every relay hop. Kept only so this node can
// still correctly relay another, not-yet-upgraded peer's legacy circuit -
// this node itself never originates EnvelopeVersion1 traffic once
// EnvelopeVersion2 is available (see manager.go's CreateCircuit).
const EnvelopeVersion1 uint8 = 1

// EnvelopeVersion2 is the hop-local envelope format: CircuitID,
// PacketCounter, and Expiration are independent per hop-to-hop leg,
// carried forward via LayerPlaintext's NextLocalCircuitID/NextLocalCounter/
// NextLocalExpiration fields (layer.go) rather than copied verbatim. See
// docs/superpowers/specs/2026-08-24-garlic-hop-local-metadata-design.md.
const EnvelopeVersion2 uint8 = 2

// MaxBodySize and MaxPaddingSize bound the envelope's variable-length
// fields. They match the underlying core.Core.MTU() cap (65535 bytes) and
// exist so a maliciously large length prefix is rejected before any
// allocation is attempted, not merely once it exceeds the buffer.
const (
	MaxBodySize    = 65535
	MaxPaddingSize = 65535
)

// envelopeFixedHeaderSize is the size, in bytes, of the fixed-length
// portion of the wire format: version(1) + circuit_id(16) + packet_counter(8)
// + expiration(8) + body_len(4).
const envelopeFixedHeaderSize = 1 + 16 + 8 + 8 + 4

var (
	ErrEnvelopeTooShort    = errors.New("garlic: envelope shorter than fixed header")
	ErrEnvelopeTruncated   = errors.New("garlic: envelope truncated")
	ErrUnsupportedVersion  = errors.New("garlic: unsupported envelope version")
	ErrBodyTooLarge        = errors.New("garlic: envelope body exceeds maximum size")
	ErrPaddingTooLarge     = errors.New("garlic: envelope padding exceeds maximum size")
	ErrCellSizeTooSmall    = errors.New("garlic: cell size too small for envelope")
	ErrInvalidPaddingRange = errors.New("garlic: invalid padding size range")
)

// Envelope is the Garlic Envelope: the outermost structure carried as the
// payload of every Garlic-tagged packet on the mesh. Body is opaque at this
// layer - in later phases it holds an AEAD ciphertext - and Padding is
// carried and round-tripped but never interpreted.
type Envelope struct {
	Version       uint8
	CircuitID     CircuitID
	PacketCounter uint64
	Expiration    uint64
	Body          []byte
	Padding       []byte
}

// Marshal encodes the envelope into its wire format:
//
//	version(1) circuit_id(16) packet_counter(8) expiration(8)
//	body_len(4) body(body_len) padding_len(4) padding(padding_len)
//
// all integers big-endian.
func (e *Envelope) Marshal() ([]byte, error) {
	if len(e.Body) > MaxBodySize {
		return nil, ErrBodyTooLarge
	}
	if len(e.Padding) > MaxPaddingSize {
		return nil, ErrPaddingTooLarge
	}

	buf := make([]byte, 0, envelopeFixedHeaderSize+len(e.Body)+4+len(e.Padding))
	buf = append(buf, e.Version)
	buf = append(buf, e.CircuitID[:]...)
	buf = binary.BigEndian.AppendUint64(buf, e.PacketCounter)
	buf = binary.BigEndian.AppendUint64(buf, e.Expiration)
	buf = binary.BigEndian.AppendUint32(buf, uint32(len(e.Body)))
	buf = append(buf, e.Body...)
	buf = binary.BigEndian.AppendUint32(buf, uint32(len(e.Padding)))
	buf = append(buf, e.Padding...)
	return buf, nil
}

// PadTo sets e.Padding so that e.Marshal's output is exactly cellSize
// bytes long, for fixed-size packet normalization (see
// docs/garlic-architecture.md §13). It resets any existing padding first,
// so calling it repeatedly does not accumulate padding. It fails with
// ErrCellSizeTooSmall if the envelope without padding already exceeds
// cellSize, and with ErrPaddingTooLarge if the padding needed would
// exceed MaxPaddingSize.
func (e *Envelope) PadTo(cellSize int) error {
	e.Padding = nil
	unpadded, err := e.Marshal()
	if err != nil {
		return err
	}
	if len(unpadded) > cellSize {
		return ErrCellSizeTooSmall
	}
	needed := cellSize - len(unpadded)
	if needed > MaxPaddingSize {
		return ErrPaddingTooLarge
	}
	padding := make([]byte, needed)
	if _, err := rand.Read(padding); err != nil {
		return err
	}
	e.Padding = padding
	return nil
}

// PadToRandomRange pads e to a uniformly random size in [minSize, maxSize]
// (raising the effective lower bound to the envelope's own unpadded size
// if that's already larger than minSize). Unlike PadTo's single fixed
// target, calling this independently at every hop - both at the
// originator and again at each relay when it rebuilds the forwarded
// envelope - means the wire size changes at every hop, so an observer
// comparing sizes seen near the two ends of a hop-to-hop link gets no
// consistent size fingerprint to correlate on. See
// docs/garlic-security.md's traffic-correlation discussion for why this
// is deliberately independent per hop rather than a single value chosen
// once by the originator.
func (e *Envelope) PadToRandomRange(minSize, maxSize int) error {
	if maxSize < minSize {
		return ErrInvalidPaddingRange
	}
	e.Padding = nil
	unpadded, err := e.Marshal()
	if err != nil {
		return err
	}
	lower := max(minSize, len(unpadded))
	if lower > maxSize {
		return ErrCellSizeTooSmall
	}
	target, err := randomIntInRange(lower, maxSize)
	if err != nil {
		return err
	}
	return e.PadTo(target)
}

// randomIntInRange returns a cryptographically random integer in [lo, hi]
// (inclusive on both ends).
func randomIntInRange(lo, hi int) (int, error) {
	if lo == hi {
		return lo, nil
	}
	span := big.NewInt(int64(hi-lo) + 1)
	n, err := rand.Int(rand.Reader, span)
	if err != nil {
		return 0, err
	}
	return lo + int(n.Int64()), nil
}

// jitteredExpiration returns a Unix-seconds Expiration value for
// now+ttl, independently jittered by up to +-10% of ttl (in whole
// seconds, floor of 1 second either way) - the same per-hop-independent
// jitter principle as PadToRandomRange's padding size, applied to
// expiration instead: two legs of the same packet must not carry a
// bit-identical wire Expiration (see docs/garlic-threat-model.md). Jitter
// is computed in whole seconds, not ttl's native nanosecond resolution,
// so the bound passed to randomIntInRange never risks overflowing a
// 32-bit int on a 32-bit build target.
func jitteredExpiration(ttl time.Duration) (uint64, error) {
	base := time.Now().Add(ttl)
	ttlSeconds := int(ttl / time.Second)
	if ttlSeconds <= 0 {
		return uint64(base.Unix()), nil
	}
	span := max(ttlSeconds/10, 1)
	offsetSeconds, err := randomIntInRange(-span, span)
	if err != nil {
		return 0, err
	}
	return uint64(base.Add(time.Duration(offsetSeconds) * time.Second).Unix()), nil
}

// Unmarshal decodes a Garlic Envelope from its wire format. It never trusts
// a declared length before validating it against both the configured
// maximum and the bytes actually remaining in data, so malformed or
// adversarial input returns an error rather than panicking or driving an
// oversized allocation.
func Unmarshal(data []byte) (*Envelope, error) {
	if len(data) < envelopeFixedHeaderSize {
		return nil, ErrEnvelopeTooShort
	}

	e := &Envelope{Version: data[0]}
	copy(e.CircuitID[:], data[1:17])
	e.PacketCounter = binary.BigEndian.Uint64(data[17:25])
	e.Expiration = binary.BigEndian.Uint64(data[25:33])
	if e.Version != EnvelopeVersion1 && e.Version != EnvelopeVersion2 {
		return nil, ErrUnsupportedVersion
	}

	rest := data[envelopeFixedHeaderSize:]
	bodyLen := binary.BigEndian.Uint32(data[33:37])
	if bodyLen > MaxBodySize {
		return nil, ErrBodyTooLarge
	}
	if uint64(bodyLen) > uint64(len(rest)) {
		return nil, ErrEnvelopeTruncated
	}
	if bodyLen > 0 {
		e.Body = append([]byte(nil), rest[:bodyLen]...)
	}
	rest = rest[bodyLen:]

	if len(rest) < 4 {
		return nil, ErrEnvelopeTruncated
	}
	paddingLen := binary.BigEndian.Uint32(rest[:4])
	rest = rest[4:]
	if paddingLen > MaxPaddingSize {
		return nil, ErrPaddingTooLarge
	}
	if uint64(paddingLen) > uint64(len(rest)) {
		return nil, ErrEnvelopeTruncated
	}
	if paddingLen > 0 {
		e.Padding = append([]byte(nil), rest[:paddingLen]...)
	}

	return e, nil
}
