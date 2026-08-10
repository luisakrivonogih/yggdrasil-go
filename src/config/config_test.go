package config

import (
	"bytes"
	"testing"
)

// ReadFrom previously sliced conf[0:2] for the BOM check without
// guarding the length, so empty or single-byte configs piped via
// -useconf panicked with index out of range.
func TestConfigReadFromEmpty(t *testing.T) {
	for _, tc := range []struct {
		name string
		body []byte
	}{
		{name: "empty", body: nil},
		{name: "single byte", body: []byte{'{'}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("ReadFrom must not panic on short input, got: %v", r)
				}
			}()
			var cfg NodeConfig
			_, _ = cfg.ReadFrom(bytes.NewReader(tc.body))
		})
	}
}

func TestGarlicConfigDefaultsDisabled(t *testing.T) {
	cfg := GenerateConfig()
	if cfg.Garlic.Enabled {
		t.Error("Garlic.Enabled = true by default, want false")
	}
}

func TestGarlicConfigPaddingAndJitterDefaults(t *testing.T) {
	cfg := GenerateConfig()
	if !cfg.Garlic.Padding.Enabled {
		t.Error("Garlic.Padding.Enabled = false by default, want true")
	}
	if cfg.Garlic.Padding.MinSize <= 0 || cfg.Garlic.Padding.MaxSize <= cfg.Garlic.Padding.MinSize {
		t.Errorf("Garlic.Padding min/max = %d/%d, want 0 < min < max", cfg.Garlic.Padding.MinSize, cfg.Garlic.Padding.MaxSize)
	}
	if !cfg.Garlic.Jitter.Enabled {
		t.Error("Garlic.Jitter.Enabled = false by default, want true")
	}
	if cfg.Garlic.Jitter.MaxDelay == "" {
		t.Error("Garlic.Jitter.MaxDelay is empty by default")
	}
	if cfg.Garlic.MaxDiscoveredPeers <= 0 {
		t.Error("Garlic.MaxDiscoveredPeers <= 0 by default, want a positive bound")
	}
}

// A config file written before the Garlic block existed must keep
// working, and must not silently enable an experimental feature it
// never mentioned.
func TestGarlicConfigAbsentFromInputStaysDisabled(t *testing.T) {
	var cfg NodeConfig
	if _, err := cfg.ReadFrom(bytes.NewReader([]byte("{}"))); err != nil {
		t.Fatalf("ReadFrom returned error: %v", err)
	}
	if cfg.Garlic.Enabled {
		t.Error("Garlic.Enabled = true for a config that never mentions garlic, want false")
	}
}

func TestConfig_Keys(t *testing.T) {
	/*
		var nodeConfig NodeConfig
		nodeConfig.NewKeys()

		publicKey1, err := hex.DecodeString(nodeConfig.PublicKey)

		if err != nil {
			t.Fatal("can not decode generated public key")
		}

		if len(publicKey1) == 0 {
			t.Fatal("empty public key generated")
		}

		privateKey1, err := hex.DecodeString(nodeConfig.PrivateKey)

		if err != nil {
			t.Fatal("can not decode generated private key")
		}

		if len(privateKey1) == 0 {
			t.Fatal("empty private key generated")
		}

		nodeConfig.NewKeys()

		publicKey2, err := hex.DecodeString(nodeConfig.PublicKey)

		if err != nil {
			t.Fatal("can not decode generated public key")
		}

		if bytes.Equal(publicKey2, publicKey1) {
			t.Fatal("same public key generated")
		}

		privateKey2, err := hex.DecodeString(nodeConfig.PrivateKey)

		if err != nil {
			t.Fatal("can not decode generated private key")
		}

		if bytes.Equal(privateKey2, privateKey1) {
			t.Fatal("same private key generated")
		}
	*/
}

func TestDashboardConfigDefaults(t *testing.T) {
	cfg := GenerateConfig()
	if cfg.Dashboard.Enabled {
		t.Error("Dashboard.Enabled = true by default, want false")
	}
	if cfg.Dashboard.Listen != "127.0.0.1:8080" {
		t.Errorf("Dashboard.Listen = %q, want \"127.0.0.1:8080\"", cfg.Dashboard.Listen)
	}
	if cfg.Dashboard.Path != "" {
		t.Errorf("Dashboard.Path = %q, want empty (tries conventional install paths)", cfg.Dashboard.Path)
	}
}
