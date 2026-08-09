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
	"strings"
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

	_ = a.AddHandler("createGarlicCircuit", "Build a circuit through the given comma-separated, ordered list of hex node keys", []string{"hops"},
		func(in json.RawMessage) (interface{}, error) {
			var req struct {
				Hops string `json:"hops"`
			}
			if err := json.Unmarshal(in, &req); err != nil {
				return nil, err
			}
			hops := splitCommaList(req.Hops)
			path := make([]CapabilityMessage, len(hops))
			nodeKeys := make([][]byte, len(hops))
			for i, h := range hops {
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
				TimeoutSeconds string `json:"timeoutSeconds"`
			}
			if err := json.Unmarshal(in, &req); err != nil {
				return nil, err
			}
			timeout, err := parseSecondsOrDefault(req.TimeoutSeconds, 5*time.Second)
			if err != nil {
				return nil, err
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
				ServiceID   string `json:"serviceId"`
				IntroPoints string `json:"introPoints"`
				TTLSeconds  string `json:"ttlSeconds"`
			}
			if err := json.Unmarshal(in, &req); err != nil {
				return nil, err
			}
			serviceID, err := hex.DecodeString(req.ServiceID)
			if err != nil {
				return nil, fmt.Errorf("invalid serviceId: %w", err)
			}
			introPoints := splitCommaList(req.IntroPoints)
			points := make([]IntroPoint, len(introPoints))
			for i, p := range introPoints {
				key, err := hex.DecodeString(p)
				if err != nil {
					return nil, fmt.Errorf("invalid introduction point: %w", err)
				}
				points[i] = IntroPoint{NodeKey: key}
			}
			ttl, err := parseSecondsOrDefault(req.TTLSeconds, time.Hour)
			if err != nil {
				return nil, err
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

	_ = a.AddHandler("getGarlicKnownPeers", "List Garlic peers this node knows about (direct or via gossip)", []string{},
		func(in json.RawMessage) (interface{}, error) {
			peers := g.KnownPeers()
			out := make([]map[string]string, len(peers))
			for i, p := range peers {
				out[i] = map[string]string{
					"nodeKey":         hex.EncodeToString(p.NodeKey),
					"garlicPublicKey": hex.EncodeToString(p.GarlicPublicKey),
					"lastSeen":        p.LastSeen.UTC().Format(time.RFC3339),
				}
			}
			return map[string]interface{}{"peers": out}, nil
		})

	_ = a.AddHandler("garlicGossip", "Send this node's known-peer sample to an already-verified peer", []string{"key"},
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
			if err := g.GossipAnnounce(key); err != nil {
				return nil, err
			}
			return map[string]interface{}{}, nil
		})

	_ = a.AddHandler("createGarlicCircuitPool", "Build several independent circuits at once; paths are semicolon-separated, hops within a path comma-separated (e.g. \"keyB;keyC\" for two 1-hop paths)", []string{"paths"},
		func(in json.RawMessage) (interface{}, error) {
			var req struct {
				Paths string `json:"paths"`
			}
			if err := json.Unmarshal(in, &req); err != nil {
				return nil, err
			}
			pathStrs := strings.Split(req.Paths, ";")
			paths := make([][]CapabilityMessage, len(pathStrs))
			nodeKeys := make([][][]byte, len(pathStrs))
			for i, pathStr := range pathStrs {
				hops := splitCommaList(pathStr)
				path := make([]CapabilityMessage, len(hops))
				keys := make([][]byte, len(hops))
				for j, h := range hops {
					key, err := hex.DecodeString(h)
					if err != nil {
						return nil, fmt.Errorf("path %d hop %d: invalid key: %w", i, j, err)
					}
					capability, err := g.QueryCapability(key)
					if err != nil {
						return nil, fmt.Errorf("path %d hop %d: %w", i, j, err)
					}
					path[j] = *capability
					keys[j] = key
				}
				paths[i] = path
				nodeKeys[i] = keys
			}
			pool, err := g.CreateCircuitPool(paths, nodeKeys)
			if err != nil {
				return nil, err
			}
			return map[string]string{"poolId": poolIDToString(pool)}, nil
		})

	_ = a.AddHandler("closeGarlicCircuitPool", "Close every circuit in a pool", []string{"poolId"},
		func(in json.RawMessage) (interface{}, error) {
			var req struct {
				PoolID string `json:"poolId"`
			}
			if err := json.Unmarshal(in, &req); err != nil {
				return nil, err
			}
			pool, err := poolIDFromString(req.PoolID)
			if err != nil {
				return nil, err
			}
			g.ClosePool(pool)
			return map[string]interface{}{}, nil
		})

	_ = a.AddHandler("sendGarlicMultipath", "Send a hex-encoded payload over the next circuit in a pool (round-robin)", []string{"poolId", "payload"},
		func(in json.RawMessage) (interface{}, error) {
			var req struct {
				PoolID  string `json:"poolId"`
				Payload string `json:"payload"`
			}
			if err := json.Unmarshal(in, &req); err != nil {
				return nil, err
			}
			pool, err := poolIDFromString(req.PoolID)
			if err != nil {
				return nil, err
			}
			payload, err := hex.DecodeString(req.Payload)
			if err != nil {
				return nil, fmt.Errorf("invalid payload: %w", err)
			}
			if err := g.SendGarlicMultipath(pool, payload); err != nil {
				return nil, err
			}
			return map[string]interface{}{}, nil
		})
}

func poolIDToString(id PoolID) string {
	return fmt.Sprintf("%d", uint64(id))
}

func poolIDFromString(s string) (PoolID, error) {
	var id uint64
	if _, err := fmt.Sscanf(s, "%d", &id); err != nil {
		return 0, fmt.Errorf("invalid poolId: %w", err)
	}
	return PoolID(id), nil
}

// parseSecondsOrDefault parses s as a floating-point number of seconds,
// returning def if s is empty. Numeric admin arguments are strings for
// the same reason list arguments are comma-separated - see
// splitCommaList's doc comment.
func parseSecondsOrDefault(s string, def time.Duration) (time.Duration, error) {
	if s == "" {
		return def, nil
	}
	var seconds float64
	if _, err := fmt.Sscanf(s, "%g", &seconds); err != nil {
		return 0, fmt.Errorf("invalid seconds value %q: %w", s, err)
	}
	return time.Duration(seconds * float64(time.Second)), nil
}

// splitCommaList splits a comma-separated list argument, as sent by
// yggdrasilctl's plain key=value CLI syntax (which only ever passes flat
// strings, never JSON arrays - see cmd/yggdrasilctl/main.go). An empty
// string yields an empty (not single-element) list.
func splitCommaList(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(s, ",")
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
