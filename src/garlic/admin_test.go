package garlic_test

// Wire-level tests for the Garlic admin handlers the dashboard (Task 9
// of the yggdashboard v2 plan) consumes: verifies the exact JSON a
// client sees on the admin socket, not just Go-level return values -
// this is what actually reaches the browser-facing /api/* layer, so
// it's the right place to assert no private-key-shaped field ever
// appears.

import (
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

func TestGetGarlicCircuitsResponseShapeAndNoSecrets(t *testing.T) {
	g, c := newTestGarlicWithCore(t)
	sockPath := newTestAdminSocket(t, c, g)

	resp := callAdmin(t, sockPath, "getGarlicCircuits")
	for _, want := range []string{"originated", "relayed"} {
		if _, ok := resp[want]; !ok {
			t.Errorf("getGarlicCircuits response missing expected field %q, got %+v", want, resp)
		}
	}
	body, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("Marshal returned error: %v", err)
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
