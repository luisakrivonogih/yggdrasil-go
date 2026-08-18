package garlic

import (
	"bytes"
	"testing"
	"time"

	"github.com/yggdrasil-network/yggdrasil-go/src/core"
)

func TestBuildCircuitDataMessageAppliesRandomPadding(t *testing.T) {
	cfg := DefaultConfig() // PaddingEnabled, [512, 1400]
	ephemeralPub, _, err := GenerateKeypair()
	if err != nil {
		t.Fatalf("GenerateKeypair returned error: %v", err)
	}

	sizes := map[int]bool{}
	for range 20 {
		msg, err := buildCircuitDataMessage(ephemeralPub, testCircuitID(1), 0, uint64(time.Now().Add(time.Minute).Unix()), []byte("onion"), cfg)
		if err != nil {
			t.Fatalf("buildCircuitDataMessage returned error: %v", err)
		}
		sizes[len(msg)] = true
	}
	if len(sizes) < 2 {
		t.Fatalf("got %d distinct message size(s) across 20 calls, want variety from padding randomization", len(sizes))
	}
}

func TestBuildCircuitDataMessageWithinConfiguredRange(t *testing.T) {
	cfg := DefaultConfig()
	cfg.MinPaddedSize = 1000
	cfg.MaxPaddedSize = 1200
	ephemeralPub, _, err := GenerateKeypair()
	if err != nil {
		t.Fatalf("GenerateKeypair returned error: %v", err)
	}

	msg, err := buildCircuitDataMessage(ephemeralPub, testCircuitID(1), 0, uint64(time.Now().Add(time.Minute).Unix()), []byte("onion"), cfg)
	if err != nil {
		t.Fatalf("buildCircuitDataMessage returned error: %v", err)
	}
	envSize := len(msg) - 1 - KeySize
	if envSize < cfg.MinPaddedSize || envSize > cfg.MaxPaddedSize {
		t.Fatalf("envelope size = %d, want in [%d, %d]", envSize, cfg.MinPaddedSize, cfg.MaxPaddedSize)
	}
}

func TestBuildCircuitDataMessageSkipsPaddingWhenDisabled(t *testing.T) {
	cfg := DefaultConfig()
	cfg.PaddingEnabled = false
	ephemeralPub, _, err := GenerateKeypair()
	if err != nil {
		t.Fatalf("GenerateKeypair returned error: %v", err)
	}

	msg, err := buildCircuitDataMessage(ephemeralPub, testCircuitID(1), 0, uint64(time.Now().Add(time.Minute).Unix()), []byte("onion"), cfg)
	if err != nil {
		t.Fatalf("buildCircuitDataMessage returned error: %v", err)
	}
	env, err := Unmarshal(msg[1+KeySize:])
	if err != nil {
		t.Fatalf("Unmarshal returned error: %v", err)
	}
	if len(env.Padding) != 0 {
		t.Fatalf("Padding = %d bytes, want 0 (padding disabled)", len(env.Padding))
	}
}

func TestHopCountFromPathsFindsMatchingKey(t *testing.T) {
	peer := []byte("peer-key")
	paths := []core.PathEntryInfo{
		{Key: []byte("other-key"), Path: []uint64{1, 2}},
		{Key: peer, Path: []uint64{1, 2, 3, 4}},
	}
	hops, ok := hopCountFromPaths(paths, peer)
	if !ok {
		t.Fatal("ok = false, want true")
	}
	if hops != 4 {
		t.Fatalf("hops = %d, want 4", hops)
	}
}

func TestHopCountFromPathsMissingKeyReturnsFalse(t *testing.T) {
	paths := []core.PathEntryInfo{
		{Key: []byte("other-key"), Path: []uint64{1, 2}},
	}
	_, ok := hopCountFromPaths(paths, []byte("unknown-peer"))
	if ok {
		t.Fatal("ok = true, want false for a peer with no cached path")
	}
}

func TestHopCountFromPathsEmptyList(t *testing.T) {
	_, ok := hopCountFromPaths(nil, []byte("peer"))
	if ok {
		t.Fatal("ok = true, want false for an empty path list")
	}
}

func TestBuildCircuitDataMessageRoundTripsOnion(t *testing.T) {
	cfg := DefaultConfig()
	ephemeralPub, _, err := GenerateKeypair()
	if err != nil {
		t.Fatalf("GenerateKeypair returned error: %v", err)
	}
	onion := []byte("onion ciphertext bytes")

	msg, err := buildCircuitDataMessage(ephemeralPub, testCircuitID(42), 7, 999, onion, cfg)
	if err != nil {
		t.Fatalf("buildCircuitDataMessage returned error: %v", err)
	}
	if msg[0] != msgTypeCircuitData {
		t.Fatalf("msg[0] = %d, want msgTypeCircuitData", msg[0])
	}
	if !bytes.Equal(msg[1:1+KeySize], ephemeralPub) {
		t.Fatalf("ephemeral pubkey in message = %x, want %x", msg[1:1+KeySize], ephemeralPub)
	}
	env, err := Unmarshal(msg[1+KeySize:])
	if err != nil {
		t.Fatalf("Unmarshal returned error: %v", err)
	}
	if env.CircuitID != testCircuitID(42) || env.PacketCounter != 7 || env.Expiration != 999 {
		t.Fatalf("envelope fields = %+v, want CircuitID=42 PacketCounter=7 Expiration=999", env)
	}
	if !bytes.Equal(env.Body, onion) {
		t.Fatalf("Body = %q, want %q", env.Body, onion)
	}
}

func TestGetStatsIncludesTrafficAndSecurityTotals(t *testing.T) {
	g := newTestGarlic(t)
	stats := g.GetStats()
	if stats.OriginatedCircuits != 0 || stats.RelayedCircuits != 0 {
		t.Fatalf("stats = %+v, want zero circuit counts with nothing set up", stats)
	}
	if stats.OriginatedBytes != 0 || stats.RelayedBytes != 0 {
		t.Fatalf("stats = %+v, want zero traffic totals with nothing set up", stats)
	}
	if stats.Security != (SecurityCounterSnapshot{}) {
		t.Fatalf("stats.Security = %+v, want all zeros", stats.Security)
	}
}

func TestOriginatedCircuitsExposesCircuitManagerList(t *testing.T) {
	g := newTestGarlic(t)
	g.circuits = NewCircuitManager(CircuitManagerConfig{MaxCircuits: 10, MaxCircuitsPerPeer: 10})
	c, err := g.circuits.Add([]Hop{{NodeKey: []byte("peer-a"), Key: make([]byte, 32)}}, time.Minute, 100, 100000)
	if err != nil {
		t.Fatalf("Add returned error: %v", err)
	}
	if _, _, _, err := c.Seal([]byte("hi")); err != nil {
		t.Fatalf("Seal returned error: %v", err)
	}

	list := g.OriginatedCircuits()
	if len(list) != 1 || list[0].ID != c.ID {
		t.Fatalf("OriginatedCircuits() = %+v, want [circuit %d]", list, c.ID)
	}

	stats := g.GetStats()
	if stats.OriginatedCircuits != 1 {
		t.Fatalf("stats.OriginatedCircuits = %d, want 1", stats.OriginatedCircuits)
	}
	if stats.OriginatedPackets != 1 || stats.OriginatedBytes != 2 {
		t.Fatalf("stats.OriginatedPackets, OriginatedBytes = %d, %d, want 1, 2", stats.OriginatedPackets, stats.OriginatedBytes)
	}
}

func TestPublishServiceThenLookupServiceRoundTrips(t *testing.T) {
	id, err := NewIdentity()
	if err != nil {
		t.Fatalf("NewIdentity returned error: %v", err)
	}
	g := &Garlic{
		identity:   id,
		cfg:        DefaultConfig(),
		rendezvous: NewStaticRendezvous(),
	}
	points := []IntroPoint{{NodeKey: []byte("intro-1")}}

	gid, err := g.PublishService([]byte("svc"), points, time.Hour)
	if err != nil {
		t.Fatalf("PublishService returned error: %v", err)
	}
	got, err := g.LookupService(gid)
	if err != nil {
		t.Fatalf("LookupService returned error: %v", err)
	}
	if len(got) != 1 || !bytes.Equal(got[0].NodeKey, []byte("intro-1")) {
		t.Fatalf("LookupService = %+v, want one intro point %q", got, "intro-1")
	}
}

func TestLookupServiceRejectsBogusRendezvousResponse(t *testing.T) {
	id, err := NewIdentity()
	if err != nil {
		t.Fatalf("NewIdentity returned error: %v", err)
	}
	attacker, err := NewIdentity()
	if err != nil {
		t.Fatalf("NewIdentity returned error: %v", err)
	}
	rendezvous := NewStaticRendezvous()
	g := &Garlic{identity: id, cfg: DefaultConfig(), rendezvous: rendezvous}

	gid, err := g.PublishService([]byte("svc"), []IntroPoint{{NodeKey: []byte("real-intro")}}, time.Hour)
	if err != nil {
		t.Fatalf("PublishService returned error: %v", err)
	}

	// A malicious rendezvous overwrites the entry with an
	// attacker-signed descriptor claiming attacker-controlled intro
	// points - but it cannot make this validate against the real GID,
	// since the GID is derived from the real service's signing key.
	forgedPublishedAt := uint64(time.Now().Unix())
	forged, err := SignServiceDescriptor(attacker.SigningPublicKey, attacker.SigningPrivateKey, []byte("svc"), []IntroPoint{{NodeKey: []byte("attacker-intro")}}, forgedPublishedAt, forgedPublishedAt+uint64(time.Hour.Seconds()))
	if err != nil {
		t.Fatalf("SignServiceDescriptor returned error: %v", err)
	}
	if err := rendezvous.Publish(gid, forged); err != nil {
		t.Fatalf("Publish returned error: %v", err)
	}

	if _, err := g.LookupService(gid); err == nil {
		t.Fatal("expected LookupService to reject the bogus rendezvous response, got nil")
	}
}
