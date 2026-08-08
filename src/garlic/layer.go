package garlic

// Layered (onion) encryption on top of the single-layer AEAD primitives in
// crypto.go (Phase 4 of the roadmap). A hop can only recover its own
// LayerPlaintext - it never sees the plaintext of any layer further in,
// and it never sees which hops came before it. This file does not decide
// how per-hop keys are established for a real circuit (that's circuit
// construction, Phase 5) - it takes already-derived per-hop keys as input.

import (
	"encoding/binary"
	"errors"
)

// MaxNextHopSize and MaxLayerInnerSize bound LayerPlaintext's variable
// fields, for the same reason Envelope bounds Body/Padding: a declared
// length must be rejected before it drives an allocation, not merely once
// it exceeds the buffer. MaxLayerInnerSize matches MaxBodySize, since Inner
// is either an application payload or another layer's ciphertext, and both
// ultimately travel inside an Envelope.Body.
const (
	MaxNextHopSize    = 256
	MaxLayerInnerSize = MaxBodySize
)

var (
	ErrEmptyPath          = errors.New("garlic: onion path must have at least one hop")
	ErrLayerTooShort      = errors.New("garlic: layer plaintext shorter than fixed header")
	ErrLayerTruncated     = errors.New("garlic: layer plaintext truncated")
	ErrNextHopTooLarge    = errors.New("garlic: next-hop field exceeds maximum size")
	ErrLayerInnerTooLarge = errors.New("garlic: layer inner field exceeds maximum size")
)

// Hop is one hop of a path used to build an onion (see BuildOnion). Key
// and Counter must follow Seal's nonce-reuse rules: Key must be unique to
// this hop within this circuit, and Counter must never repeat under that
// Key.
type Hop struct {
	NodeKey []byte // this hop's Yggdrasil public key (routing address)
	Key     []byte // per-hop symmetric key, already derived (e.g. via ECDH + DeriveKey)
	Counter uint64 // nonce/replay counter for this hop's layer
}

// LayerPlaintext is what a hop recovers after decrypting its layer: either
// forwarding instructions (NextHop set, Inner is the ciphertext to forward
// there) or, for the final hop, the delivered payload (NextHop empty,
// Inner is the payload itself). A real NodeKey is never zero-length, so an
// empty NextHop unambiguously marks the terminal hop.
type LayerPlaintext struct {
	NextHop []byte
	Inner   []byte
}

func (l *LayerPlaintext) marshal() ([]byte, error) {
	if len(l.NextHop) > MaxNextHopSize {
		return nil, ErrNextHopTooLarge
	}
	if len(l.Inner) > MaxLayerInnerSize {
		return nil, ErrLayerInnerTooLarge
	}
	buf := make([]byte, 0, 4+len(l.NextHop)+4+len(l.Inner))
	buf = binary.BigEndian.AppendUint32(buf, uint32(len(l.NextHop)))
	buf = append(buf, l.NextHop...)
	buf = binary.BigEndian.AppendUint32(buf, uint32(len(l.Inner)))
	buf = append(buf, l.Inner...)
	return buf, nil
}

func unmarshalLayerPlaintext(data []byte) (*LayerPlaintext, error) {
	if len(data) < 4 {
		return nil, ErrLayerTooShort
	}
	nextHopLen := binary.BigEndian.Uint32(data[:4])
	rest := data[4:]
	if nextHopLen > MaxNextHopSize {
		return nil, ErrNextHopTooLarge
	}
	if uint64(nextHopLen) > uint64(len(rest)) {
		return nil, ErrLayerTruncated
	}
	l := &LayerPlaintext{}
	if nextHopLen > 0 {
		l.NextHop = append([]byte(nil), rest[:nextHopLen]...)
	}
	rest = rest[nextHopLen:]

	if len(rest) < 4 {
		return nil, ErrLayerTruncated
	}
	innerLen := binary.BigEndian.Uint32(rest[:4])
	rest = rest[4:]
	if innerLen > MaxLayerInnerSize {
		return nil, ErrLayerInnerTooLarge
	}
	if uint64(innerLen) > uint64(len(rest)) {
		return nil, ErrLayerTruncated
	}
	if innerLen > 0 {
		l.Inner = append([]byte(nil), rest[:innerLen]...)
	}
	return l, nil
}

// EncryptLayer encodes and encrypts layer under key/counter, producing the
// ciphertext a hop receives for this layer. See Seal for the nonce-reuse
// requirement on (key, counter).
func EncryptLayer(key []byte, counter uint64, layer *LayerPlaintext) ([]byte, error) {
	pt, err := layer.marshal()
	if err != nil {
		return nil, err
	}
	return Seal(key, counter, pt, nil)
}

// DecryptLayer decrypts and parses a layer ciphertext produced by
// EncryptLayer with the same key and counter.
func DecryptLayer(key []byte, counter uint64, ciphertext []byte) (*LayerPlaintext, error) {
	pt, err := Open(key, counter, ciphertext, nil)
	if err != nil {
		return nil, err
	}
	return unmarshalLayerPlaintext(pt)
}

// BuildOnion constructs a layered-encrypted onion for path hops, with
// payload as the innermost content. hops[0] is the first hop the sender
// transmits the returned ciphertext to; hops[len(hops)-1] is the final hop,
// which recovers payload with an empty NextHop. Each intermediate hop i
// recovers NextHop == hops[i+1].NodeKey and a still-encrypted Inner to
// forward there unchanged.
func BuildOnion(hops []Hop, payload []byte) ([]byte, error) {
	if len(hops) == 0 {
		return nil, ErrEmptyPath
	}

	inner := payload
	for i := len(hops) - 1; i >= 0; i-- {
		var nextHop []byte
		if i+1 < len(hops) {
			nextHop = hops[i+1].NodeKey
		}
		ct, err := EncryptLayer(hops[i].Key, hops[i].Counter, &LayerPlaintext{
			NextHop: nextHop,
			Inner:   inner,
		})
		if err != nil {
			return nil, err
		}
		inner = ct
	}
	return inner, nil
}
