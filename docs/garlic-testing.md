# Testing the Garlic Routing Overlay on a real network

This walks through running two `yggdrasil` nodes with Garlic enabled,
peering them, and sending a message end-to-end through a Garlic circuit
via `yggdrasilctl` — the exact sequence below was run against real
built binaries while writing this document, not written from theory.

For a multi-machine / real-Internet deployment, skip straight to
["On a real multi-node network"](#on-a-real-multi-node-network) below —
the two-node walkthrough exists to get you a working local sanity check
first.

## Build

```sh
go build -o ./yggdrasil ./cmd/yggdrasil
go build -o ./yggdrasilctl ./cmd/yggdrasilctl
```

## 1. Generate two configs and enable Garlic

```sh
./yggdrasil -genconf > nodeA.conf
./yggdrasil -genconf > nodeB.conf
```

Edit each file (HJSON, plain text) and change three things — the rest of
the generated config is fine as-is:

```hjson
AdminListen: tcp://localhost:9001    # 9002 for nodeB
Listen: [
  tls://localhost:9101               # 9102 for nodeB - omit if you don't need inbound peers
]
Garlic: {
  Enabled: true
  ...                                 # leave the rest at their defaults
}
```

(`AdminListen` isn't present in the generated file by default on Linux —
it defaults to a fixed Unix socket path, which two nodes on one machine
can't both use, so add the `AdminListen:` line explicitly as shown.)

`IfName: none` is worth setting for this kind of headless test (no TUN
device, no root required) — Garlic doesn't need a TUN interface to work,
since it rides on `core.Core`'s own transport, not on IPv6 packets
through the TUN device. See `docs/garlic-architecture.md` §2 for why.

## 2. Start both nodes

```sh
./yggdrasil -useconffile nodeA.conf -logto nodeA.log &
./yggdrasil -useconffile nodeB.conf -logto nodeB.log &
```

Check `nodeA.log`/`nodeB.log` for a line like:

```
Your Garlic public key is 9434b21a22c8a361b36e341eb7b76b651ce495157936e74010971c10010bce52
```

If you didn't set `Garlic.PrivateKey` in the config, you'll also see a
warning that an ephemeral identity was generated for this run only — expected
for a quick test; for a stable identity across restarts, generate a key
once and put it in the config (see `docs/garlic-architecture.md` §1.1 for
why this is a separate key from your main Yggdrasil identity).

## 3. Peer them

```sh
./yggdrasilctl -endpoint=tcp://localhost:9002 addPeer uri=tls://localhost:9101
./yggdrasilctl -endpoint=tcp://localhost:9001 getPeers   # confirm "Up"
```

Give it a few seconds — DHT convergence isn't instant, and
`garlicQueryCapability`/`createGarlicCircuit` below will fail with a
"capability request timed out" error if you try immediately. This is
the normal, expected timeout for a peer that hasn't been reachable long
enough yet, not a bug — retry after a couple of seconds.

## 4. Exercise the Garlic API via yggdrasilctl

**Important CLI quirk:** `yggdrasilctl`'s plain `key=value` syntax only
ever sends string values — it can't send a JSON array or number. Because
of this, list-valued Garlic arguments (`hops`, `introPoints`) are
**comma-separated strings**, and numeric ones (`timeoutSeconds`,
`ttlSeconds`) are **numeric strings**, not JSON arrays/numbers. All the
examples below already account for this.

```sh
# Get nodeB's Yggdrasil node key (needed as a circuit hop identifier -
# note this is the main Yggdrasil key, not the Garlic public key).
NODEB_KEY=$(./yggdrasilctl -endpoint=tcp://localhost:9002 -json getself | python3 -c "import json,sys; print(json.load(sys.stdin)['key'])")

# Confirm nodeB is Garlic-capable and fetch its Garlic public key.
./yggdrasilctl -endpoint=tcp://localhost:9001 -json garlicQueryCapability key=$NODEB_KEY

# Build a 1-hop circuit through nodeB. For multiple hops, pass
# hops=key1,key2,key3 (comma-separated, ordered).
./yggdrasilctl -endpoint=tcp://localhost:9001 -json createGarlicCircuit hops=$NODEB_KEY
# => {"circuitId": "11668724407072267096"}

CIRCUIT_ID=11668724407072267096
PAYLOAD_HEX=$(python3 -c "print('hello bob, from alice, via garlic'.encode().hex())")

./yggdrasilctl -endpoint=tcp://localhost:9001 -json sendGarlic circuitId=$CIRCUIT_ID payload=$PAYLOAD_HEX

# On nodeB, receive it (blocks up to timeoutSeconds waiting for delivery):
./yggdrasilctl -endpoint=tcp://localhost:9002 -json recvGarlic timeoutSeconds=5
# => {"circuitId": "11668724407072267096", "payload": "68656c6c6f..."}

python3 -c "print(bytes.fromhex('68656c6c6f20626f622c2066726f6d20616c6963652c20766961206761726c6963').decode())"
# => hello bob, from alice, via garlic
```

```sh
# Circuit/relay counts on each side:
./yggdrasilctl -endpoint=tcp://localhost:9001 -json getGarlicStats   # {"originatedCircuits":1,"relayedCircuits":0}
./yggdrasilctl -endpoint=tcp://localhost:9002 -json getGarlicStats   # {"originatedCircuits":0,"relayedCircuits":1}

# Clean up:
./yggdrasilctl -endpoint=tcp://localhost:9001 closeGarlicCircuit circuitId=$CIRCUIT_ID
```

Full handler list (`src/garlic/admin.go`): `getGarlicIdentity`,
`garlicQueryCapability`, `createGarlicCircuit`, `closeGarlicCircuit`,
`sendGarlic`, `recvGarlic`, `publishGarlicService`, `lookupGarlicService`,
`getGarlicStats`.

## On a real multi-node network

Same procedure, three changes:

1. Set `Listen` to a real reachable address (`tls://0.0.0.0:PORT` or
   similar) and configure `Peers`/exchange connection strings with
   whoever you're testing against, exactly as you would for ordinary
   Yggdrasil peering — Garlic doesn't change how peering works at all.
2. Point `-endpoint=` at each node's actual `AdminListen` address (or run
   `yggdrasilctl` directly on each machine against its local admin
   socket).
3. **You do not need every node on the path to run Garlic.** Per
   `docs/garlic-compatibility.md`, only the nodes you name in `hops=`
   need `Garlic.Enabled: true` — ordinary Yggdrasil nodes between them
   (existing infrastructure, other people's nodes, whatever) carry the
   traffic transparently with zero configuration changes on their part.
   That's the whole point, and it's what
   `TestIntegrationSendGarlicThroughLegacyRelay`
   (`src/garlic/integration_test.go`) proves automatically, in-process,
   without needing real hardware.

## Confirming legacy compatibility yourself

If you want to see the "legacy node doesn't even notice" property
directly rather than take the docs' word for it: query a node that has
`Garlic.Enabled: false` (or is running an older `yggdrasil` build
entirely) with `garlicQueryCapability` — it will time out, identically
to querying an offline node. That timeout, and the total absence of any
error, log line, or state change on the legacy node's side, *is* the
compatibility guarantee.

## Automated tests, if you'd rather not do this by hand

```sh
go test ./src/garlic/...                                    # everything, ~10-70s (mesh convergence timing varies)
go test ./src/garlic/... -run TestIntegrationSendGarlicThroughLegacyRelay -v   # just the 5-node legacy-relay proof
go test ./src/garlic/... -run '^$' -bench . -benchmem        # performance numbers
go test ./src/garlic/... -run '^$' -fuzz=FuzzEnvelopeUnmarshal -fuzztime=60s   # adversarial input, any Fuzz* target
```
