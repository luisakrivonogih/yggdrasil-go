package garlic_test

// Wire-level tests for the Garlic admin handlers the dashboard (Task 9
// of the yggdashboard v2 plan) consumes: verifies the exact JSON a
// client sees on the admin socket, not just Go-level return values -
// this is what actually reaches the browser-facing /api/* layer, so
// it's the right place to assert no private-key-shaped field ever
// appears.

import (
	"encoding/hex"
	"encoding/json"
	"io"
	"net"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gologme/log"

	"github.com/yggdrasil-network/yggdrasil-go/src/admin"
	"github.com/yggdrasil-network/yggdrasil-go/src/core"
	"github.com/yggdrasil-network/yggdrasil-go/src/garlic"
)

func newTestGarlicWithCore(t *testing.T) (*garlic.Garlic, *core.Core) {
	t.Helper()
	c := newLinkedTestNode(t)
	id, err := garlic.NewIdentity()
	if err != nil {
		t.Fatalf("NewIdentity returned error: %v", err)
	}
	g := garlic.New(c, id, garlic.DefaultConfig(), garlic.NewStaticRendezvous())
	t.Cleanup(g.Close)
	return g, c
}

// newTestAdminSocket wires a real admin.AdminSocket, listening on a
// temporary unix socket, with garlicInst's handlers registered - the
// same SetupAdminHandlers call cmd/yggdrasil/main.go makes.
func newTestAdminSocket(t *testing.T, c *core.Core, garlicInst *garlic.Garlic) string {
	t.Helper()
	sockPath := filepath.Join(t.TempDir(), "admin.sock")
	logger := log.New(io.Discard, "", 0)
	a, err := admin.New(c, logger, admin.ListenAddress("unix://"+sockPath))
	if err != nil {
		t.Fatalf("admin.New returned error: %v", err)
	}
	if a == nil {
		t.Fatal("admin.New returned a nil AdminSocket for a real unix listen address")
	}
	garlicInst.SetupAdminHandlers(a)
	return sockPath
}

// callAdmin sends one request to the admin socket at sockPath and
// returns the decoded "response" object.
func callAdmin(t *testing.T, sockPath, request string) map[string]interface{} {
	t.Helper()
	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		t.Fatalf("net.Dial returned error: %v", err)
	}
	defer conn.Close()

	enc := json.NewEncoder(conn)
	if err := enc.Encode(map[string]interface{}{"request": request, "arguments": map[string]interface{}{}}); err != nil {
		t.Fatalf("Encode returned error: %v", err)
	}
	var resp map[string]interface{}
	dec := json.NewDecoder(conn)
	if err := dec.Decode(&resp); err != nil {
		t.Fatalf("Decode returned error: %v", err)
	}
	if resp["status"] != "success" {
		t.Fatalf("admin request %q failed: %v", request, resp["error"])
	}
	respBody, _ := resp["response"].(map[string]interface{})
	return respBody
}

func TestGetGarlicStatsResponseShapeAndNoSecrets(t *testing.T) {
	g, c := newTestGarlicWithCore(t)
	sockPath := newTestAdminSocket(t, c, g)

	resp := callAdmin(t, sockPath, "getGarlicStats")
	for _, want := range []string{"originatedCircuits", "relayedCircuits", "originatedBytes", "relayedBytes", "security"} {
		if _, ok := resp[want]; !ok {
			t.Errorf("getGarlicStats response missing expected field %q, got %+v", want, resp)
		}
	}
	body, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("Marshal returned error: %v", err)
	}
	for _, forbidden := range []string{"privateKey", "PrivateKey", "secret", "Secret", "sessionKey", "aeadKey"} {
		if strings.Contains(string(body), forbidden) {
			t.Errorf("getGarlicStats response contains forbidden substring %q: %s", forbidden, body)
		}
	}
}

// TestGetGarlicCircuitsResponseShapeAndNoSecrets builds one real
// originated circuit through the same public API path
// createGarlicCircuit's admin handler uses (CreateCircuit + SendGarlic),
// rather than asserting against an empty {"originated":[],"relayed":[]}
// response. CreateCircuit derives a genuine per-hop AEAD key via
// ECDH+HKDF for that hop (Circuit.hops[i].Key) - HopKeys() is supposed
// to expose only the hop's NodeKey and never that derived key
// (circuit.go's doc comment on HopKeys). A no-secret-leak scan over a
// response with no circuits in it can't actually exercise that
// distinction; it would pass just as trivially if HopKeys() leaked
// Key too. This only strengthens the *originated* side: relayed-side
// circuit state (relaystate.go's relayCircuitInfo) never stores
// anything but hop NodeKeys and traffic counters in the first place -
// populating it would require a full multi-node mesh with capability
// negotiation (as in integration_test.go) for no corresponding increase
// in what's actually at risk of leaking.
func TestGetGarlicCircuitsResponseShapeAndNoSecrets(t *testing.T) {
	g, c := newTestGarlicWithCore(t)
	sockPath := newTestAdminSocket(t, c, g)

	// hopIdentity's public key stands in for a real hop's long-term
	// Garlic key - CreateCircuit ECDHs the circuit's ephemeral private
	// key against it to derive that hop's layer key, exactly as it
	// would for a real capability-verified peer.
	hopIdentity, err := garlic.NewIdentity()
	if err != nil {
		t.Fatalf("NewIdentity returned error: %v", err)
	}
	// nodeIdentity's public key is only ever used as the hop's NodeKey
	// (the mesh address CreateCircuit's caller already knows in
	// plaintext) - never as key material - but reuses NewIdentity for a
	// convenient 32-byte value.
	nodeIdentity, err := garlic.NewIdentity()
	if err != nil {
		t.Fatalf("NewIdentity returned error: %v", err)
	}
	nodeKey := nodeIdentity.PublicKey

	circuitID, err := g.CreateCircuit(
		[]garlic.CapabilityMessage{{Versions: []string{"garlic-v1"}, PublicKey: hopIdentity.PublicKey}},
		[][]byte{nodeKey},
	)
	if err != nil {
		t.Fatalf("CreateCircuit returned error: %v", err)
	}
	if err := g.SendGarlic(circuitID, []byte("hello")); err != nil {
		t.Fatalf("SendGarlic returned error: %v", err)
	}

	resp := callAdmin(t, sockPath, "getGarlicCircuits")
	for _, want := range []string{"originated", "relayed"} {
		if _, ok := resp[want]; !ok {
			t.Errorf("getGarlicCircuits response missing expected field %q, got %+v", want, resp)
		}
	}

	originated, ok := resp["originated"].([]interface{})
	if !ok || len(originated) != 1 {
		t.Fatalf("resp[\"originated\"] = %+v, want exactly 1 entry", resp["originated"])
	}
	entry, ok := originated[0].(map[string]interface{})
	if !ok {
		t.Fatalf("originated[0] = %+v, want a JSON object", originated[0])
	}
	wantHopHex := hex.EncodeToString(nodeKey)
	hops, ok := entry["hops"].([]interface{})
	if !ok || len(hops) != 1 || hops[0] != wantHopHex {
		t.Fatalf("originated[0][\"hops\"] = %+v, want [%q]", entry["hops"], wantHopHex)
	}
	if packets, _ := entry["packets"].(float64); packets != 1 {
		t.Errorf("originated[0][\"packets\"] = %v, want 1", entry["packets"])
	}
	if bytesSent, _ := entry["bytes"].(float64); bytesSent != 5 {
		t.Errorf("originated[0][\"bytes\"] = %v, want 5 (len(\"hello\"))", entry["bytes"])
	}

	body, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("Marshal returned error: %v", err)
	}
	// Positive control: confirms the scan below runs over a genuinely
	// populated response (containing the real hop key, hex-encoded) and
	// not a vacuously-passing empty one.
	if !strings.Contains(string(body), wantHopHex) {
		t.Fatalf("response does not contain the expected hop key %q - test failed to populate a real circuit: %s", wantHopHex, body)
	}
	for _, forbidden := range []string{"privateKey", "PrivateKey", "secret", "Secret", "sessionKey", "aeadKey"} {
		if strings.Contains(string(body), forbidden) {
			t.Errorf("getGarlicCircuits response contains forbidden substring %q: %s", forbidden, body)
		}
	}
}

func TestGetGarlicIdentityOnlyExposesPublicKey(t *testing.T) {
	g, c := newTestGarlicWithCore(t)
	sockPath := newTestAdminSocket(t, c, g)

	resp := callAdmin(t, sockPath, "getGarlicIdentity")
	if _, ok := resp["publicKey"]; !ok {
		t.Error("getGarlicIdentity response missing publicKey field")
	}
	if _, ok := resp["privateKey"]; ok {
		t.Error("getGarlicIdentity response must never contain a privateKey field")
	}
}

func TestGetSelfResponseHasNoPrivateKeyField(t *testing.T) {
	c := newLinkedTestNode(t)
	sockPath := filepath.Join(t.TempDir(), "admin2.sock")
	logger := log.New(io.Discard, "", 0)
	a, err := admin.New(c, logger, admin.ListenAddress("unix://"+sockPath))
	if err != nil {
		t.Fatalf("admin.New returned error: %v", err)
	}
	a.SetupAdminHandlers()

	resp := callAdmin(t, sockPath, "getSelf")
	if _, ok := resp["uptime"]; !ok {
		t.Error("getSelf response missing uptime field")
	}
	body, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("Marshal returned error: %v", err)
	}
	if strings.Contains(string(body), "privateKey") || strings.Contains(string(body), "PrivateKey") {
		t.Errorf("getSelf response contains a private key field: %s", body)
	}
}
