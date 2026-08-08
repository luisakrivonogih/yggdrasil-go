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
)

// EnvelopeVersion1 is the only Garlic Envelope wire version defined so far.
const EnvelopeVersion1 uint8 = 1

// MaxBodySize and MaxPaddingSize bound the envelope's variable-length
// fields. They match the underlying core.Core.MTU() cap (65535 bytes) and
// exist so a maliciously large length prefix is rejected before any
// allocation is attempted, not merely once it exceeds the buffer.
const (
	MaxBodySize    = 65535
	MaxPaddingSize = 65535
)

// envelopeFixedHeaderSize is the size, in bytes, of the fixed-length
// portion of the wire format: version(1) + circuit_id(8) + packet_counter(8)
// + expiration(8) + body_len(4).
const envelopeFixedHeaderSize = 1 + 8 + 8 + 8 + 4

var (
	ErrEnvelopeTooShort   = errors.New("garlic: envelope shorter than fixed header")
	ErrEnvelopeTruncated  = errors.New("garlic: envelope truncated")
	ErrUnsupportedVersion = errors.New("garlic: unsupported envelope version")
	ErrBodyTooLarge       = errors.New("garlic: envelope body exceeds maximum size")
	ErrPaddingTooLarge    = errors.New("garlic: envelope padding exceeds maximum size")
	ErrCellSizeTooSmall   = errors.New("garlic: cell size too small for envelope")
)

// Envelope is the Garlic Envelope: the outermost structure carried as the
// payload of every Garlic-tagged packet on the mesh. Body is opaque at this
// layer - in later phases it holds an AEAD ciphertext - and Padding is
// carried and round-tripped but never interpreted.
type Envelope struct {
	Version       uint8
	CircuitID     uint64
	PacketCounter uint64
	Expiration    uint64
	Body          []byte
	Padding       []byte
}

// Marshal encodes the envelope into its wire format:
//
//	version(1) circuit_id(8) packet_counter(8) expiration(8)
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
	buf = binary.BigEndian.AppendUint64(buf, e.CircuitID)
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

// Unmarshal decodes a Garlic Envelope from its wire format. It never trusts
// a declared length before validating it against both the configured
// maximum and the bytes actually remaining in data, so malformed or
// adversarial input returns an error rather than panicking or driving an
// oversized allocation.
func Unmarshal(data []byte) (*Envelope, error) {
	if len(data) < envelopeFixedHeaderSize {
		return nil, ErrEnvelopeTooShort
	}

	e := &Envelope{
		Version:       data[0],
		CircuitID:     binary.BigEndian.Uint64(data[1:9]),
		PacketCounter: binary.BigEndian.Uint64(data[9:17]),
		Expiration:    binary.BigEndian.Uint64(data[17:25]),
	}
	if e.Version != EnvelopeVersion1 {
		return nil, ErrUnsupportedVersion
	}

	rest := data[envelopeFixedHeaderSize:]
	bodyLen := binary.BigEndian.Uint32(data[25:29])
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
