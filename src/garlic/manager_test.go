package garlic

import (
	"bytes"
	"testing"
	"time"
)

func TestBuildCircuitDataMessageAppliesRandomPadding(t *testing.T) {
	cfg := DefaultConfig() // PaddingEnabled, [512, 1400]
	ephemeralPub, _, err := GenerateKeypair()
	if err != nil {
		t.Fatalf("GenerateKeypair returned error: %v", err)
	}

	sizes := map[int]bool{}
	for range 20 {
		msg, err := buildCircuitDataMessage(ephemeralPub, CircuitID(1), 0, uint64(time.Now().Add(time.Minute).Unix()), []byte("onion"), cfg)
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

	msg, err := buildCircuitDataMessage(ephemeralPub, CircuitID(1), 0, uint64(time.Now().Add(time.Minute).Unix()), []byte("onion"), cfg)
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

	msg, err := buildCircuitDataMessage(ephemeralPub, CircuitID(1), 0, uint64(time.Now().Add(time.Minute).Unix()), []byte("onion"), cfg)
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

func TestBuildCircuitDataMessageRoundTripsOnion(t *testing.T) {
	cfg := DefaultConfig()
	ephemeralPub, _, err := GenerateKeypair()
	if err != nil {
		t.Fatalf("GenerateKeypair returned error: %v", err)
	}
	onion := []byte("onion ciphertext bytes")

	msg, err := buildCircuitDataMessage(ephemeralPub, CircuitID(42), 7, 999, onion, cfg)
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
	if env.CircuitID != 42 || env.PacketCounter != 7 || env.Expiration != 999 {
		t.Fatalf("envelope fields = %+v, want CircuitID=42 PacketCounter=7 Expiration=999", env)
	}
	if !bytes.Equal(env.Body, onion) {
		t.Fatalf("Body = %q, want %q", env.Body, onion)
	}
}
