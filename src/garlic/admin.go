package garlic

// Admin socket handlers (Phase 6/12 of the roadmap's API surface, see
// docs/garlic-architecture.md §3.12), following the same
// SetupAdminHandlers(a *admin.AdminSocket) convention already used by
// src/multicast and src/tun: each handler parses a JSON request, calls
// into the already-tested Garlic methods, and returns a JSON response.
// This file is deliberately thin - the logic it wraps is tested
// elsewhere (protocol.go, manager.go, circuit.go, ...).

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/yggdrasil-network/yggdrasil-go/src/admin"
)

// SetupAdminHandlers registers this Garlic instance's admin socket
// handlers, reachable via yggdrasilctl.
func (g *Garlic) SetupAdminHandlers(a *admin.AdminSocket) {
	_ = a.AddHandler("getGarlicIdentity", "Show this node's Garlic identity public key", []string{},
		func(in json.RawMessage) (interface{}, error) {
			return map[string]string{"publicKey": hex.EncodeToString(g.identity.PublicKey)}, nil
		})

	_ = a.AddHandler("garlicQueryCapability", "Query whether a node supports Garlic and its public key", []string{"key"},
		func(in json.RawMessage) (interface{}, error) {
			var req struct {
				Key string `json:"key"`
			}
			if err := json.Unmarshal(in, &req); err != nil {
				return nil, err
			}
			key, err := hex.DecodeString(req.Key)
			if err != nil {
				return nil, fmt.Errorf("invalid key: %w", err)
			}
			msg, err := g.QueryCapability(key)
			if err != nil {
				return nil, err
			}
			return map[string]interface{}{
				"versions":  msg.Versions,
				"publicKey": hex.EncodeToString(msg.PublicKey),
			}, nil
		})

	_ = a.AddHandler("createGarlicCircuit", "Build a circuit through the given ordered list of node keys", []string{"hops"},
		func(in json.RawMessage) (interface{}, error) {
			var req struct {
				Hops []string `json:"hops"`
			}
			if err := json.Unmarshal(in, &req); err != nil {
				return nil, err
			}
			path := make([]CapabilityMessage, len(req.Hops))
			nodeKeys := make([][]byte, len(req.Hops))
			for i, h := range req.Hops {
				key, err := hex.DecodeString(h)
				if err != nil {
					return nil, fmt.Errorf("invalid hop key: %w", err)
				}
				capability, err := g.QueryCapability(key)
				if err != nil {
					return nil, fmt.Errorf("hop %d: %w", i, err)
				}
				path[i] = *capability
				nodeKeys[i] = key
			}
			id, err := g.CreateCircuit(path, nodeKeys)
			if err != nil {
				return nil, err
			}
			return map[string]string{"circuitId": circuitIDToString(id)}, nil
		})

	_ = a.AddHandler("closeGarlicCircuit", "Close a previously created circuit", []string{"circuitId"},
		func(in json.RawMessage) (interface{}, error) {
			id, err := parseCircuitIDRequest(in)
			if err != nil {
				return nil, err
			}
			g.CloseCircuit(id)
			return map[string]interface{}{}, nil
		})

	_ = a.AddHandler("sendGarlic", "Send a payload (hex-encoded) over an existing circuit", []string{"circuitId", "payload"},
		func(in json.RawMessage) (interface{}, error) {
			var req struct {
				CircuitID string `json:"circuitId"`
				Payload   string `json:"payload"`
			}
			if err := json.Unmarshal(in, &req); err != nil {
				return nil, err
			}
			id, err := circuitIDFromString(req.CircuitID)
			if err != nil {
				return nil, err
			}
			payload, err := hex.DecodeString(req.Payload)
			if err != nil {
				return nil, fmt.Errorf("invalid payload: %w", err)
			}
			if err := g.SendGarlic(id, payload); err != nil {
				return nil, err
			}
			return map[string]interface{}{}, nil
		})

	_ = a.AddHandler("recvGarlic", "Wait for the next payload delivered to this node as a circuit's final hop", []string{"[timeoutSeconds]"},
		func(in json.RawMessage) (interface{}, error) {
			var req struct {
				TimeoutSeconds float64 `json:"timeoutSeconds"`
			}
			if err := json.Unmarshal(in, &req); err != nil {
				return nil, err
			}
			timeout := 5 * time.Second
			if req.TimeoutSeconds > 0 {
				timeout = time.Duration(req.TimeoutSeconds * float64(time.Second))
			}
			msg, err := g.RecvGarlic(timeout)
			if err != nil {
				return nil, err
			}
			return map[string]string{
				"circuitId": circuitIDToString(msg.CircuitID),
				"payload":   hex.EncodeToString(msg.Payload),
			}, nil
		})

	_ = a.AddHandler("publishGarlicService", "Publish this node's identity at a set of introduction points", []string{"serviceId", "introPoints", "[ttlSeconds]"},
		func(in json.RawMessage) (interface{}, error) {
			var req struct {
				ServiceID   string   `json:"serviceId"`
				IntroPoints []string `json:"introPoints"`
				TTLSeconds  float64  `json:"ttlSeconds"`
			}
			if err := json.Unmarshal(in, &req); err != nil {
				return nil, err
			}
			serviceID, err := hex.DecodeString(req.ServiceID)
			if err != nil {
				return nil, fmt.Errorf("invalid serviceId: %w", err)
			}
			points := make([]IntroPoint, len(req.IntroPoints))
			for i, p := range req.IntroPoints {
				key, err := hex.DecodeString(p)
				if err != nil {
					return nil, fmt.Errorf("invalid introduction point: %w", err)
				}
				points[i] = IntroPoint{NodeKey: key}
			}
			ttl := time.Hour
			if req.TTLSeconds > 0 {
				ttl = time.Duration(req.TTLSeconds * float64(time.Second))
			}
			gid, err := g.PublishService(serviceID, points, ttl)
			if err != nil {
				return nil, err
			}
			return map[string]string{"gid": gid.String()}, nil
		})

	_ = a.AddHandler("lookupGarlicService", "Look up the introduction points published for a GID", []string{"gid"},
		func(in json.RawMessage) (interface{}, error) {
			var req struct {
				GID string `json:"gid"`
			}
			if err := json.Unmarshal(in, &req); err != nil {
				return nil, err
			}
			gid, err := ParseGID(req.GID)
			if err != nil {
				return nil, fmt.Errorf("invalid gid: %w", err)
			}
			points, err := g.LookupService(gid)
			if err != nil {
				return nil, err
			}
			keys := make([]string, len(points))
			for i, p := range points {
				keys[i] = hex.EncodeToString(p.NodeKey)
			}
			return map[string]interface{}{"introPoints": keys}, nil
		})

	_ = a.AddHandler("getGarlicStats", "Show this node's current Garlic circuit counts", []string{},
		func(in json.RawMessage) (interface{}, error) {
			stats := g.GetStats()
			return map[string]int{
				"originatedCircuits": stats.OriginatedCircuits,
				"relayedCircuits":    stats.RelayedCircuits,
			}, nil
		})
}

func circuitIDToString(id CircuitID) string {
	return fmt.Sprintf("%d", uint64(id))
}

func circuitIDFromString(s string) (CircuitID, error) {
	var id uint64
	if _, err := fmt.Sscanf(s, "%d", &id); err != nil {
		return 0, fmt.Errorf("invalid circuitId: %w", err)
	}
	return CircuitID(id), nil
}

func parseCircuitIDRequest(in json.RawMessage) (CircuitID, error) {
	var req struct {
		CircuitID string `json:"circuitId"`
	}
	if err := json.Unmarshal(in, &req); err != nil {
		return 0, err
	}
	return circuitIDFromString(req.CircuitID)
}
