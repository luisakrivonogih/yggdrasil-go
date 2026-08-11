import type {
  StatusResponse,
  StatsResponse,
  PeersResponse,
  CircuitsResponse,
  GarlicResponse,
  GraphResponse
} from '$lib/api-types';

export interface PolledResource<T> {
  readonly data: T | null;
  readonly connected: boolean;
  readonly latencyMs: number | null;
  start(): void;
  stop(): void;
}

/**
 * A small reactive polling primitive: fetches url every intervalMs,
 * exposing the parsed JSON as $state, plus connection health. On a
 * failed fetch, `connected` goes false but `data` keeps its last good
 * value - matches the "stale metrics, not a blank screen" requirement.
 * Uses Svelte 5 runes, hence the .svelte.ts extension.
 */
export function createPolledResource<T>(url: string, intervalMs: number): PolledResource<T> {
  let data = $state<T | null>(null);
  let connected = $state(true);
  let latencyMs = $state<number | null>(null);
  let timer: ReturnType<typeof setInterval> | undefined;

  async function pollOnce() {
    const start = performance.now();
    try {
      const res = await fetch(url);
      if (!res.ok) throw new Error(`${url} returned HTTP ${res.status}`);
      data = (await res.json()) as T;
      connected = true;
      latencyMs = Math.round(performance.now() - start);
    } catch {
      connected = false;
    }
  }

  function start() {
    if (timer) return;
    void pollOnce();
    timer = setInterval(() => void pollOnce(), intervalMs);
  }

  function stop() {
    if (timer) clearInterval(timer);
    timer = undefined;
  }

  return {
    get data() {
      return data;
    },
    get connected() {
      return connected;
    },
    get latencyMs() {
      return latencyMs;
    },
    start,
    stop
  };
}

export function createStatusResource(intervalMs = 1500): PolledResource<StatusResponse> {
  return createPolledResource<StatusResponse>('/api/status', intervalMs);
}
export function createStatsResource(intervalMs = 1500): PolledResource<StatsResponse> {
  return createPolledResource<StatsResponse>('/api/stats', intervalMs);
}
export function createPeersResource(intervalMs = 2000): PolledResource<PeersResponse> {
  return createPolledResource<PeersResponse>('/api/peers', intervalMs);
}
export function createCircuitsResource(intervalMs = 2000): PolledResource<CircuitsResponse> {
  return createPolledResource<CircuitsResponse>('/api/circuits', intervalMs);
}
export function createGarlicResource(intervalMs = 2000): PolledResource<GarlicResponse> {
  return createPolledResource<GarlicResponse>('/api/garlic', intervalMs);
}
export function createGraphResource(intervalMs = 2000): PolledResource<GraphResponse> {
  return createPolledResource<GraphResponse>('/api/graph', intervalMs);
}
