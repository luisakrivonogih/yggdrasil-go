package garlic

// Garlic bundling (Phase 11 of the roadmap, see
// docs/garlic-architecture.md §3.7): several independently-encrypted
// messages carried inside one garlic packet's body. This type only
// concatenates/splits already-opaque message blobs - it never inspects
// or decrypts them, which is what keeps an intermediate relay unable to
// tell which bundled messages share a real-world sender or correlate
// their plaintext. Each Messages[i] is expected to already be an
// AEAD ciphertext (e.g. produced by EncryptLayer) before it goes into a
// Bundle.

import (
	"crypto/rand"
	"encoding/binary"
	"errors"
)

// MaxBundleMessages bounds how many messages a single bundle may carry,
// and MaxBundleMessageSize bounds each one - both exist so a declared
// count/length is rejected before it drives an allocation, not merely
// once it exceeds the buffer, matching Envelope's and LayerPlaintext's
// parsing discipline.
const (
	MaxBundleMessages    = 32
	MaxBundleMessageSize = MaxBodySize
)

var (
	ErrTooManyBundleMessages = errors.New("garlic: too many bundle messages")
	ErrBundleMessageTooLarge = errors.New("garlic: bundle message exceeds maximum size")
	ErrBundleTruncated       = errors.New("garlic: bundle truncated")
	ErrBundleFull            = errors.New("garlic: bundle is full")
)

// Bundle is a set of independently-encrypted messages carried together.
type Bundle struct {
	Messages [][]byte
}

// Marshal encodes the bundle as count(4) followed by, for each message,
// len(4) and its bytes.
func (b *Bundle) Marshal() ([]byte, error) {
	if len(b.Messages) > MaxBundleMessages {
		return nil, ErrTooManyBundleMessages
	}
	size := 4
	for _, m := range b.Messages {
		if len(m) > MaxBundleMessageSize {
			return nil, ErrBundleMessageTooLarge
		}
		size += 4 + len(m)
	}
	buf := make([]byte, 0, size)
	buf = binary.BigEndian.AppendUint32(buf, uint32(len(b.Messages)))
	for _, m := range b.Messages {
		buf = binary.BigEndian.AppendUint32(buf, uint32(len(m)))
		buf = append(buf, m...)
	}
	return buf, nil
}

// UnmarshalBundle decodes a bundle produced by Marshal, never trusting a
// declared count or length before validating it against both the
// configured maximum and the bytes actually remaining.
func UnmarshalBundle(data []byte) (*Bundle, error) {
	if len(data) < 4 {
		return nil, ErrBundleTruncated
	}
	count := binary.BigEndian.Uint32(data[:4])
	if count > MaxBundleMessages {
		return nil, ErrTooManyBundleMessages
	}
	rest := data[4:]

	messages := make([][]byte, 0, count)
	for range count {
		if len(rest) < 4 {
			return nil, ErrBundleTruncated
		}
		msgLen := binary.BigEndian.Uint32(rest[:4])
		rest = rest[4:]
		if msgLen > MaxBundleMessageSize {
			return nil, ErrBundleMessageTooLarge
		}
		if uint64(msgLen) > uint64(len(rest)) {
			return nil, ErrBundleTruncated
		}
		msg := append([]byte(nil), rest[:msgLen]...)
		rest = rest[msgLen:]
		messages = append(messages, msg)
	}
	return &Bundle{Messages: messages}, nil
}

// AddCoverMessage appends a message of size random bytes: shaped exactly
// like a real bundled message, but carrying no real content. An
// intermediate relay that can't decrypt any bundled message has no way
// to distinguish this from a genuine one (see docs/garlic-architecture.md
// §13 on traffic analysis resistance - this is the hook batching/mixing
// can build on later, not a mixnet by itself).
func (b *Bundle) AddCoverMessage(size int) error {
	if len(b.Messages) >= MaxBundleMessages {
		return ErrBundleFull
	}
	cover := make([]byte, size)
	if _, err := rand.Read(cover); err != nil {
		return err
	}
	b.Messages = append(b.Messages, cover)
	return nil
}
