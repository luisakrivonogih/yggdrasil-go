package core

import "crypto/ed25519"

// GarlicHandler receives packets sent with WriteGarlic once decrypted from
// the mesh's own transport-level encryption (see src/garlic). from is the
// sending node's Yggdrasil public key.
//
// A GarlicHandler MUST NOT block: it is invoked synchronously from within
// Core.ReadFrom's read loop, exactly like the existing NodeInfo/debug
// protocol handlers, so a slow handler would stall ordinary IPv6 traffic
// for this node. Implementations should hand off to their own
// worker/queue and return immediately.
type GarlicHandler func(from ed25519.PublicKey, data []byte)

// SetGarlicHandler registers the callback that receives incoming Garlic
// Routing Overlay traffic (see WriteGarlic). Passing nil unregisters it,
// reverting to the default legacy behavior of silently dropping
// typeSessionGarlic packets. Safe to call concurrently with ReadFrom.
func (c *Core) SetGarlicHandler(h GarlicHandler) {
	if h == nil {
		c.garlicHandler.Store(nil)
		return
	}
	c.garlicHandler.Store(&h)
}

func (c *Core) getGarlicHandler() GarlicHandler {
	if p := c.garlicHandler.Load(); p != nil {
		return *p
	}
	return nil
}
