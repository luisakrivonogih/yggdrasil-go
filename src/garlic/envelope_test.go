package garlic

import (
	"bytes"
	"encoding/binary"
	"testing"
)

func TestEnvelopeMarshalUnmarshalRoundTrip(t *testing.T) {
	env := &Envelope{
		Version:       EnvelopeVersion1,
		CircuitID:     0x0102030405060708,
		PacketCounter: 42,
		Expiration:    1234567890,
		Body:          []byte("hello garlic"),
		Padding:       []byte{0, 0, 0, 0},
	}

	data, err := env.Marshal()
	if err != nil {
		t.Fatalf("Marshal returned error: %v", err)
	}

	got, err := Unmarshal(data)
	if err != nil {
		t.Fatalf("Unmarshal returned error: %v", err)
	}
	if got.Version != env.Version {
		t.Errorf("Version = %d, want %d", got.Version, env.Version)
	}
	if got.CircuitID != env.CircuitID {
		t.Errorf("CircuitID = %#x, want %#x", got.CircuitID, env.CircuitID)
	}
	if got.PacketCounter != env.PacketCounter {
		t.Errorf("PacketCounter = %d, want %d", got.PacketCounter, env.PacketCounter)
	}
	if got.Expiration != env.Expiration {
		t.Errorf("Expiration = %d, want %d", got.Expiration, env.Expiration)
	}
	if !bytes.Equal(got.Body, env.Body) {
		t.Errorf("Body = %q, want %q", got.Body, env.Body)
	}
	if !bytes.Equal(got.Padding, env.Padding) {
		t.Errorf("Padding = %q, want %q", got.Padding, env.Padding)
	}
}

func TestEnvelopeMarshalUnmarshalRoundTripEmptyBodyAndPadding(t *testing.T) {
	env := &Envelope{Version: EnvelopeVersion1, CircuitID: 1, PacketCounter: 1, Expiration: 1}

	data, err := env.Marshal()
	if err != nil {
		t.Fatalf("Marshal returned error: %v", err)
	}

	got, err := Unmarshal(data)
	if err != nil {
		t.Fatalf("Unmarshal returned error: %v", err)
	}
	if len(got.Body) != 0 {
		t.Errorf("Body = %q, want empty", got.Body)
	}
	if len(got.Padding) != 0 {
		t.Errorf("Padding = %q, want empty", got.Padding)
	}
}

func TestUnmarshalRejectsEmptyInput(t *testing.T) {
	if _, err := Unmarshal(nil); err == nil {
		t.Fatal("expected error for empty input, got nil")
	}
}

func TestUnmarshalRejectsTruncatedHeader(t *testing.T) {
	data := make([]byte, envelopeFixedHeaderSize-1)
	if _, err := Unmarshal(data); err == nil {
		t.Fatal("expected error for truncated header, got nil")
	}
}

func TestUnmarshalRejectsBodyLengthExceedingBuffer(t *testing.T) {
	var data []byte
	data = append(data, EnvelopeVersion1)
	data = binary.BigEndian.AppendUint64(data, 1)     // circuit id
	data = binary.BigEndian.AppendUint64(data, 1)     // packet counter
	data = binary.BigEndian.AppendUint64(data, 1)     // expiration
	data = binary.BigEndian.AppendUint32(data, 1<<20) // claims a huge body that isn't actually there

	if _, err := Unmarshal(data); err == nil {
		t.Fatal("expected error for body length exceeding buffer, got nil")
	}
}

func TestUnmarshalRejectsBodyLengthAtMaxUint32(t *testing.T) {
	// Regression guard: a declared length near the uint32 max must not be used
	// to drive an allocation before it's validated against the actual buffer.
	var data []byte
	data = append(data, EnvelopeVersion1)
	data = binary.BigEndian.AppendUint64(data, 1)
	data = binary.BigEndian.AppendUint64(data, 1)
	data = binary.BigEndian.AppendUint64(data, 1)
	data = binary.BigEndian.AppendUint32(data, 0xFFFFFFFF)

	if _, err := Unmarshal(data); err == nil {
		t.Fatal("expected error for body length at uint32 max, got nil")
	}
}

func TestUnmarshalRejectsPaddingLengthExceedingBuffer(t *testing.T) {
	body := []byte("hi")
	var data []byte
	data = append(data, EnvelopeVersion1)
	data = binary.BigEndian.AppendUint64(data, 1)
	data = binary.BigEndian.AppendUint64(data, 1)
	data = binary.BigEndian.AppendUint64(data, 1)
	data = binary.BigEndian.AppendUint32(data, uint32(len(body)))
	data = append(data, body...)
	data = binary.BigEndian.AppendUint32(data, 1<<20) // claims huge padding that isn't actually there

	if _, err := Unmarshal(data); err == nil {
		t.Fatal("expected error for padding length exceeding buffer, got nil")
	}
}

func TestUnmarshalRejectsUnsupportedVersion(t *testing.T) {
	env := &Envelope{Version: 99, CircuitID: 1, PacketCounter: 1, Expiration: 1}
	data, err := env.Marshal()
	if err != nil {
		t.Fatalf("Marshal returned error: %v", err)
	}

	if _, err := Unmarshal(data); err == nil {
		t.Fatal("expected error for unsupported version, got nil")
	}
}

func TestMarshalRejectsBodyExceedingMaxSize(t *testing.T) {
	env := &Envelope{Version: EnvelopeVersion1, Body: make([]byte, MaxBodySize+1)}
	if _, err := env.Marshal(); err == nil {
		t.Fatal("expected error for body exceeding MaxBodySize, got nil")
	}
}

func TestMarshalRejectsPaddingExceedingMaxSize(t *testing.T) {
	env := &Envelope{Version: EnvelopeVersion1, Padding: make([]byte, MaxPaddingSize+1)}
	if _, err := env.Marshal(); err == nil {
		t.Fatal("expected error for padding exceeding MaxPaddingSize, got nil")
	}
}

func TestUnmarshalDoesNotAliasInputBuffer(t *testing.T) {
	env := &Envelope{Version: EnvelopeVersion1, Body: []byte("original")}
	data, err := env.Marshal()
	if err != nil {
		t.Fatalf("Marshal returned error: %v", err)
	}

	got, err := Unmarshal(data)
	if err != nil {
		t.Fatalf("Unmarshal returned error: %v", err)
	}

	for i := range data {
		data[i] = 0xFF
	}

	if !bytes.Equal(got.Body, []byte("original")) {
		t.Errorf("Body = %q after mutating input buffer, want unaffected copy %q", got.Body, "original")
	}
}

func TestEnvelopePadToProducesExactCellSize(t *testing.T) {
	env := &Envelope{Version: EnvelopeVersion1, Body: []byte("a short body")}
	if err := env.PadTo(1200); err != nil {
		t.Fatalf("PadTo returned error: %v", err)
	}
	data, err := env.Marshal()
	if err != nil {
		t.Fatalf("Marshal returned error: %v", err)
	}
	if len(data) != 1200 {
		t.Errorf("len(data) = %d, want 1200", len(data))
	}
}

func TestEnvelopePadToZeroPaddingNeeded(t *testing.T) {
	env := &Envelope{Version: EnvelopeVersion1}
	unpadded, err := env.Marshal()
	if err != nil {
		t.Fatalf("Marshal returned error: %v", err)
	}
	if err := env.PadTo(len(unpadded)); err != nil {
		t.Fatalf("PadTo returned error: %v", err)
	}
	if len(env.Padding) != 0 {
		t.Errorf("Padding = %d bytes, want 0", len(env.Padding))
	}
}

func TestEnvelopePadToRejectsCellSizeTooSmall(t *testing.T) {
	env := &Envelope{Version: EnvelopeVersion1, Body: make([]byte, 100)}
	if err := env.PadTo(10); err == nil {
		t.Fatal("expected error for a cell size smaller than the unpadded envelope, got nil")
	}
}

func TestEnvelopePadToIsIdempotentAcrossRepeatedCalls(t *testing.T) {
	env := &Envelope{Version: EnvelopeVersion1, Body: []byte("a short body")}
	if err := env.PadTo(1200); err != nil {
		t.Fatalf("first PadTo returned error: %v", err)
	}
	if err := env.PadTo(1200); err != nil {
		t.Fatalf("second PadTo returned error: %v", err)
	}
	data, err := env.Marshal()
	if err != nil {
		t.Fatalf("Marshal returned error: %v", err)
	}
	if len(data) != 1200 {
		t.Errorf("len(data) = %d, want 1200 (padding must not accumulate across calls)", len(data))
	}
}

func TestEnvelopePadToRejectsExceedingMaxPaddingSize(t *testing.T) {
	env := &Envelope{Version: EnvelopeVersion1}
	if err := env.PadTo(envelopeFixedHeaderSize + 4 + MaxPaddingSize + 1); err == nil {
		t.Fatal("expected error when the needed padding exceeds MaxPaddingSize, got nil")
	}
}

func TestEnvelopePadToRandomRangeStaysWithinBounds(t *testing.T) {
	for range 50 {
		env := &Envelope{Version: EnvelopeVersion1, Body: []byte("a short body")}
		if err := env.PadToRandomRange(1000, 1400); err != nil {
			t.Fatalf("PadToRandomRange returned error: %v", err)
		}
		data, err := env.Marshal()
		if err != nil {
			t.Fatalf("Marshal returned error: %v", err)
		}
		if len(data) < 1000 || len(data) > 1400 {
			t.Fatalf("len(data) = %d, want in [1000, 1400]", len(data))
		}
	}
}

func TestEnvelopePadToRandomRangeProducesVariety(t *testing.T) {
	sizes := map[int]bool{}
	for range 50 {
		env := &Envelope{Version: EnvelopeVersion1, Body: []byte("x")}
		if err := env.PadToRandomRange(500, 2000); err != nil {
			t.Fatalf("PadToRandomRange returned error: %v", err)
		}
		data, err := env.Marshal()
		if err != nil {
			t.Fatalf("Marshal returned error: %v", err)
		}
		sizes[len(data)] = true
	}
	if len(sizes) < 2 {
		t.Fatalf("PadToRandomRange produced only %d distinct size(s) across 50 calls, want variety", len(sizes))
	}
}

func TestEnvelopePadToRandomRangeRaisesLowerBoundForLargeBody(t *testing.T) {
	env := &Envelope{Version: EnvelopeVersion1, Body: make([]byte, 1200)}
	if err := env.PadToRandomRange(10, 2000); err != nil {
		t.Fatalf("PadToRandomRange returned error: %v", err)
	}
	data, err := env.Marshal()
	if err != nil {
		t.Fatalf("Marshal returned error: %v", err)
	}
	if len(data) < envelopeFixedHeaderSize+4+1200 {
		t.Fatalf("len(data) = %d, want at least the unpadded envelope size even though minSize was smaller", len(data))
	}
}

func TestEnvelopePadToRandomRangeRejectsWhenUnpaddedExceedsMax(t *testing.T) {
	env := &Envelope{Version: EnvelopeVersion1, Body: make([]byte, 2000)}
	if err := env.PadToRandomRange(10, 100); err == nil {
		t.Fatal("expected error when the unpadded envelope already exceeds maxSize, got nil")
	}
}

func TestEnvelopePadToRandomRangeRejectsInvertedRange(t *testing.T) {
	env := &Envelope{Version: EnvelopeVersion1}
	if err := env.PadToRandomRange(2000, 1000); err == nil {
		t.Fatal("expected error for maxSize < minSize, got nil")
	}
}
