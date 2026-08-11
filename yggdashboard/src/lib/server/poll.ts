import type { AdminClient } from './admin-client';
import {
  EMPTY_SNAPSHOT,
  EMPTY_GARLIC,
  type Snapshot,
  type SelfInfo,
  type PeerEntry,
  type SessionEntry,
  type TreeEntry,
  type PathEntry,
  type GarlicSnapshot,
  type GarlicIdentity,
  type GarlicStats,
  type GarlicCircuits,
  type GarlicKnownPeer,
  type HistorySample
} from './types';

/**
 * Polls every admin endpoint the dashboard needs over one shared
 * AdminClient (one persistent keepalive connection, pipelined - see
 * admin-client.ts), keeps the latest Snapshot plus a bounded in-memory
 * history ring buffer, and serves every caller (every /api/* route,
 * every SSR load function) from that one copy - the admin-socket poll
 * rate never scales with how many browser tabs are open.
 *
 * Garlic calls are tried as a group: if getGarlicStats fails (the admin
 * socket has no such handler at all when Garlic.Enabled is false on the
 * node), the whole Garlic snapshot for this tick is the explicit
 * disabled/zeroed shape, and the other three Garlic calls aren't even
 * attempted that tick - not treated as an error to log, just the normal
 * disabled state.
 */
export class Poller {
  private client: AdminClient;
  private intervalMs: number;
  private historyWindowMs: number;
  private timer: ReturnType<typeof setInterval> | null = null;
  private latest: Snapshot = EMPTY_SNAPSHOT;
  private history: HistorySample[] = [];
  private prevGarlicBytes: { originated: number; relayed: number; t: number } | null = null;
  private readyWaiters: Array<() => void> = [];
  private hasPolledOnce = false;
  // Incremented on every tick() start and on every stop(). A tick only
  // commits its result if this still matches the token it captured when
  // it began - so a tick still in flight when stop() is called (or a
  // slower, older tick superseded by a newer one that already started)
  // never overwrites this.latest/this.history after the fact.
  private tickToken = 0;

  constructor(client: AdminClient, intervalMs: number, historyWindowMs: number) {
    this.client = client;
    this.intervalMs = intervalMs;
    this.historyWindowMs = historyWindowMs;
  }

  start(): void {
    if (this.timer) return;
    void this.tick();
    this.timer = setInterval(() => void this.tick(), this.intervalMs);
  }

  stop(): void {
    if (this.timer) clearInterval(this.timer);
    this.timer = null;
    this.tickToken++;
  }

  getSnapshot(): Snapshot {
    return this.latest;
  }

  /**
   * Resolves once the first poll has completed, or after timeoutMs -
   * whichever comes first. Lets an SSR load function show real data on
   * the very first request after the dashboard process starts, without
   * blocking indefinitely if the admin socket is unreachable.
   */
  waitUntilReady(timeoutMs: number): Promise<void> {
    if (this.hasPolledOnce) return Promise.resolve();
    return new Promise<void>((resolve) => {
      const timer = setTimeout(resolve, timeoutMs);
      this.readyWaiters.push(() => {
        clearTimeout(timer);
        resolve();
      });
    });
  }

  private async tick(): Promise<void> {
    const token = ++this.tickToken;

    const [selfRes, peersRes, sessionsRes, treeRes, pathsRes] = await Promise.allSettled([
      this.client.request<SelfInfo>('getSelf'),
      this.client.request<{ peers: PeerEntry[] }>('getPeers'),
      this.client.request<{ sessions: SessionEntry[] }>('getSessions'),
      this.client.request<{ tree: TreeEntry[] }>('getTree'),
      this.client.request<{ paths: PathEntry[] }>('getPaths')
    ]);
    const garlic = await this.pollGarlic();

    // This tick was superseded (stop() was called, or a newer tick
    // already started) while the requests above were in flight - discard
    // its result rather than write stale data over whatever's current.
    if (token !== this.tickToken) return;

    const self = selfRes.status === 'fulfilled' ? selfRes.value : this.latest.self;
    const peers = peersRes.status === 'fulfilled' ? peersRes.value.peers : this.latest.peers;
    const sessions = sessionsRes.status === 'fulfilled' ? sessionsRes.value.sessions : this.latest.sessions;
    const tree = treeRes.status === 'fulfilled' ? treeRes.value.tree : this.latest.tree;
    const paths = pathsRes.status === 'fulfilled' ? pathsRes.value.paths : this.latest.paths;

    for (const [label, r] of [
      ['getSelf', selfRes],
      ['getPeers', peersRes],
      ['getSessions', sessionsRes],
      ['getTree', treeRes],
      ['getPaths', pathsRes]
    ] as const) {
      if (r.status === 'rejected') {
        console.error(`yggdashboard: poll request ${label} failed:`, r.reason);
      }
    }

    const now = Date.now();
    const rxRate = peers.reduce((sum, p) => sum + (p.rate_recvd ?? 0), 0);
    const txRate = peers.reduce((sum, p) => sum + (p.rate_sent ?? 0), 0);

    let garlicRelayedRate = 0;
    let garlicOriginatedRate = 0;
    if (garlic.enabled && this.prevGarlicBytes) {
      const elapsedSeconds = (now - this.prevGarlicBytes.t) / 1000;
      if (elapsedSeconds > 0) {
        garlicRelayedRate = Math.max(0, (garlic.stats.relayedBytes - this.prevGarlicBytes.relayed) / elapsedSeconds);
        garlicOriginatedRate = Math.max(0, (garlic.stats.originatedBytes - this.prevGarlicBytes.originated) / elapsedSeconds);
      }
    }
    this.prevGarlicBytes = garlic.enabled
      ? { originated: garlic.stats.originatedBytes, relayed: garlic.stats.relayedBytes, t: now }
      : null;

    // Build a new array rather than mutating this.history in place - an
    // older Snapshot returned by an earlier getSnapshot() call still
    // holds a reference to the previous history array, and it must not
    // silently gain elements or otherwise change after the fact.
    const sample = { t: now, rxRate, txRate, garlicRelayedRate, garlicOriginatedRate };
    this.history = [...this.history, sample].filter((s) => now - s.t <= this.historyWindowMs);

    this.latest = {
      self,
      peers,
      sessions,
      tree,
      paths,
      garlic,
      history: this.history,
      polledAt: new Date(now).toISOString(),
      ready: true
    };

    if (!this.hasPolledOnce) {
      this.hasPolledOnce = true;
      const waiters = this.readyWaiters.splice(0);
      for (const resolve of waiters) resolve();
    }
  }

  private async pollGarlic(): Promise<GarlicSnapshot> {
    let stats: GarlicStats;
    try {
      stats = await this.client.request<GarlicStats>('getGarlicStats');
    } catch {
      return EMPTY_GARLIC;
    }

    const [identityRes, circuitsRes, knownPeersRes] = await Promise.allSettled([
      this.client.request<GarlicIdentity>('getGarlicIdentity'),
      this.client.request<GarlicCircuits>('getGarlicCircuits'),
      this.client.request<{ peers: GarlicKnownPeer[] }>('getGarlicKnownPeers')
    ]);

    return {
      enabled: true,
      identity: identityRes.status === 'fulfilled' ? identityRes.value : this.latest.garlic.identity,
      stats,
      circuits: circuitsRes.status === 'fulfilled' ? circuitsRes.value : this.latest.garlic.circuits,
      knownPeers: knownPeersRes.status === 'fulfilled' ? knownPeersRes.value.peers : this.latest.garlic.knownPeers
    };
  }
}
