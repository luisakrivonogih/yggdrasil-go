package garlic

import (
	"bytes"
	"encoding/binary"
	"testing"
	"time"
)

func TestAnnounceMessageMarshalUnmarshalRoundTrip(t *testing.T) {
	msg := &AnnounceMessage{Peers: []AnnouncePeer{
		{NodeKey: []byte("node-key-a"), GarlicPublicKey: []byte("garlic-key-a")},
		{NodeKey: []byte("node-key-b"), GarlicPublicKey: []byte("garlic-key-b")},
	}}
	data, err := msg.Marshal()
	if err != nil {
		t.Fatalf("Marshal returned error: %v", err)
	}
	got, err := UnmarshalAnnounceMessage(data)
	if err != nil {
		t.Fatalf("UnmarshalAnnounceMessage returned error: %v", err)
	}
	if len(got.Peers) != len(msg.Peers) {
		t.Fatalf("got %d peers, want %d", len(got.Peers), len(msg.Peers))
	}
	for i := range msg.Peers {
		if !bytes.Equal(got.Peers[i].NodeKey, msg.Peers[i].NodeKey) {
			t.Errorf("peer %d NodeKey = %q, want %q", i, got.Peers[i].NodeKey, msg.Peers[i].NodeKey)
		}
		if !bytes.Equal(got.Peers[i].GarlicPublicKey, msg.Peers[i].GarlicPublicKey) {
			t.Errorf("peer %d GarlicPublicKey = %q, want %q", i, got.Peers[i].GarlicPublicKey, msg.Peers[i].GarlicPublicKey)
		}
	}
}

func TestAnnounceMessageMarshalEmpty(t *testing.T) {
	msg := &AnnounceMessage{}
	data, err := msg.Marshal()
	if err != nil {
		t.Fatalf("Marshal returned error: %v", err)
	}
	got, err := UnmarshalAnnounceMessage(data)
	if err != nil {
		t.Fatalf("UnmarshalAnnounceMessage returned error: %v", err)
	}
	if len(got.Peers) != 0 {
		t.Fatalf("got %d peers, want 0", len(got.Peers))
	}
}

func TestAnnounceMessageMarshalRejectsTooManyPeers(t *testing.T) {
	msg := &AnnounceMessage{Peers: make([]AnnouncePeer, maxAnnouncePeers+1)}
	if _, err := msg.Marshal(); err == nil {
		t.Fatal("expected error for too many peers, got nil")
	}
}

func TestAnnounceMessageMarshalRejectsOversizedKey(t *testing.T) {
	msg := &AnnounceMessage{Peers: []AnnouncePeer{{NodeKey: make([]byte, maxAnnounceKeySize+1), GarlicPublicKey: []byte("g")}}}
	if _, err := msg.Marshal(); err == nil {
		t.Fatal("expected error for oversized node key, got nil")
	}
}

func TestUnmarshalAnnounceMessageRejectsTruncated(t *testing.T) {
	if _, err := UnmarshalAnnounceMessage([]byte{2}); err == nil {
		t.Fatal("expected error for truncated announce, got nil")
	}
}

func TestUnmarshalAnnounceMessageRejectsCountExceedingMax(t *testing.T) {
	var data []byte
	data = binary.BigEndian.AppendUint32(data, 0xFFFFFFFF)
	if _, err := UnmarshalAnnounceMessage(data); err == nil {
		t.Fatal("expected error for count exceeding maxAnnouncePeers, got nil")
	}
}

func TestUnmarshalAnnounceMessageRejectsLengthExceedingBuffer(t *testing.T) {
	var data []byte
	data = binary.BigEndian.AppendUint32(data, 1)
	data = append(data, 200) // node_key_len claims 200 bytes that aren't there
	if _, err := UnmarshalAnnounceMessage(data); err == nil {
		t.Fatal("expected error for key length exceeding buffer, got nil")
	}
}

func TestDiscoveryRegistryRecordAndList(t *testing.T) {
	r := newDiscoveryRegistry(16)
	r.record(DiscoveredPeer{NodeKey: []byte("a"), GarlicPublicKey: []byte("ga")})
	r.record(DiscoveredPeer{NodeKey: []byte("b"), GarlicPublicKey: []byte("gb")})

	peers := r.list()
	if len(peers) != 2 {
		t.Fatalf("list() returned %d peers, want 2", len(peers))
	}
}

func TestDiscoveryRegistryRecordUpdatesExisting(t *testing.T) {
	r := newDiscoveryRegistry(16)
	r.record(DiscoveredPeer{NodeKey: []byte("a"), GarlicPublicKey: []byte("ga-old")})
	r.record(DiscoveredPeer{NodeKey: []byte("a"), GarlicPublicKey: []byte("ga-new")})

	peers := r.list()
	if len(peers) != 1 {
		t.Fatalf("list() returned %d peers, want 1 (same NodeKey should update, not duplicate)", len(peers))
	}
	if string(peers[0].GarlicPublicKey) != "ga-new" {
		t.Fatalf("GarlicPublicKey = %q, want %q", peers[0].GarlicPublicKey, "ga-new")
	}
}

func TestDiscoveryRegistryEvictsOldestWhenFull(t *testing.T) {
	r := newDiscoveryRegistry(2)
	r.record(DiscoveredPeer{NodeKey: []byte("old"), GarlicPublicKey: []byte("g")})
	// Force a distinguishable LastSeen ordering.
	time.Sleep(2 * time.Millisecond)
	r.record(DiscoveredPeer{NodeKey: []byte("newer"), GarlicPublicKey: []byte("g")})
	time.Sleep(2 * time.Millisecond)
	r.record(DiscoveredPeer{NodeKey: []byte("newest"), GarlicPublicKey: []byte("g")}) // should evict "old"

	peers := r.list()
	if len(peers) != 2 {
		t.Fatalf("list() returned %d peers, want 2 (capacity bound)", len(peers))
	}
	for _, p := range peers {
		if string(p.NodeKey) == "old" {
			t.Fatal("oldest entry was not evicted when registry was full")
		}
	}
}

func TestDiscoveryRegistrySampleBounded(t *testing.T) {
	r := newDiscoveryRegistry(16)
	for i := range 10 {
		r.record(DiscoveredPeer{NodeKey: []byte{byte(i)}, GarlicPublicKey: []byte("g")})
	}
	sample := r.sample(3)
	if len(sample) != 3 {
		t.Fatalf("sample(3) returned %d peers, want 3", len(sample))
	}
}

func TestDiscoveryRegistrySampleCappedByAvailable(t *testing.T) {
	r := newDiscoveryRegistry(16)
	r.record(DiscoveredPeer{NodeKey: []byte("a"), GarlicPublicKey: []byte("g")})
	sample := r.sample(5)
	if len(sample) != 1 {
		t.Fatalf("sample(5) returned %d peers, want 1 (only one recorded)", len(sample))
	}
}
