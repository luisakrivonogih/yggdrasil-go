package garlic

import (
	"bytes"
	"encoding/binary"
	"testing"
)

func TestBundleMarshalUnmarshalRoundTrip(t *testing.T) {
	b := &Bundle{Messages: [][]byte{
		[]byte("message to A"),
		[]byte("message to B"),
		{},
		[]byte("message to A again"),
	}}

	data, err := b.Marshal()
	if err != nil {
		t.Fatalf("Marshal returned error: %v", err)
	}
	got, err := UnmarshalBundle(data)
	if err != nil {
		t.Fatalf("UnmarshalBundle returned error: %v", err)
	}
	if len(got.Messages) != len(b.Messages) {
		t.Fatalf("got %d messages, want %d", len(got.Messages), len(b.Messages))
	}
	for i := range b.Messages {
		if !bytes.Equal(got.Messages[i], b.Messages[i]) {
			t.Errorf("message %d = %q, want %q", i, got.Messages[i], b.Messages[i])
		}
	}
}

func TestBundleMarshalEmptyBundle(t *testing.T) {
	b := &Bundle{}
	data, err := b.Marshal()
	if err != nil {
		t.Fatalf("Marshal returned error: %v", err)
	}
	got, err := UnmarshalBundle(data)
	if err != nil {
		t.Fatalf("UnmarshalBundle returned error: %v", err)
	}
	if len(got.Messages) != 0 {
		t.Errorf("got %d messages, want 0", len(got.Messages))
	}
}

func TestBundleMarshalRejectsTooManyMessages(t *testing.T) {
	b := &Bundle{Messages: make([][]byte, MaxBundleMessages+1)}
	if _, err := b.Marshal(); err == nil {
		t.Fatal("expected error for too many messages, got nil")
	}
}

func TestBundleMarshalRejectsOversizedMessage(t *testing.T) {
	b := &Bundle{Messages: [][]byte{make([]byte, MaxBundleMessageSize+1)}}
	if _, err := b.Marshal(); err == nil {
		t.Fatal("expected error for oversized message, got nil")
	}
}

func TestUnmarshalBundleRejectsTruncatedHeader(t *testing.T) {
	if _, err := UnmarshalBundle([]byte{0, 0}); err == nil {
		t.Fatal("expected error for truncated header, got nil")
	}
}

func TestUnmarshalBundleRejectsCountExceedingMax(t *testing.T) {
	var data []byte
	// Claims far more messages than MaxBundleMessages allows; must be
	// rejected before any allocation sized by this count.
	data = binary.BigEndian.AppendUint32(data, 0xFFFFFFFF)
	if _, err := UnmarshalBundle(data); err == nil {
		t.Fatal("expected error for count exceeding MaxBundleMessages, got nil")
	}
}

func TestUnmarshalBundleRejectsLengthExceedingBuffer(t *testing.T) {
	var data []byte
	data = binary.BigEndian.AppendUint32(data, 1)     // 1 message
	data = binary.BigEndian.AppendUint32(data, 1<<20) // claims a huge message that isn't actually there
	if _, err := UnmarshalBundle(data); err == nil {
		t.Fatal("expected error for message length exceeding buffer, got nil")
	}
}

func TestUnmarshalBundleRejectsMessageLengthExceedingMax(t *testing.T) {
	var data []byte
	data = binary.BigEndian.AppendUint32(data, 1)
	data = binary.BigEndian.AppendUint32(data, MaxBundleMessageSize+1)
	if _, err := UnmarshalBundle(data); err == nil {
		t.Fatal("expected error for message length exceeding MaxBundleMessageSize, got nil")
	}
}

func TestBundleAddCoverMessageHasRequestedSize(t *testing.T) {
	b := &Bundle{}
	if err := b.AddCoverMessage(64); err != nil {
		t.Fatalf("AddCoverMessage returned error: %v", err)
	}
	if len(b.Messages) != 1 || len(b.Messages[0]) != 64 {
		t.Fatalf("Messages = %v, want one 64-byte message", b.Messages)
	}
}

func TestBundleAddCoverMessageRejectsWhenFull(t *testing.T) {
	b := &Bundle{Messages: make([][]byte, MaxBundleMessages)}
	if err := b.AddCoverMessage(64); err == nil {
		t.Fatal("expected error adding a cover message to a full bundle, got nil")
	}
}
