import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';

describe('createPolledResource (via createStatusResource)', () => {
  beforeEach(() => {
    vi.useFakeTimers();
    vi.stubGlobal('fetch', vi.fn());
  });
  afterEach(() => {
    vi.useRealTimers();
    vi.unstubAllGlobals();
  });

  it('fetches immediately on start and exposes the parsed JSON as data', async () => {
    (fetch as ReturnType<typeof vi.fn>).mockResolvedValue({ ok: true, json: async () => ({ hello: 'world' }) });
    const { createStatusResource } = await import('./dashboard.svelte');
    const resource = createStatusResource(1000);
    resource.start();
    await vi.advanceTimersByTimeAsync(0);
    expect(resource.data).toEqual({ hello: 'world' });
    expect(resource.connected).toBe(true);
    resource.stop();
  });

  it('marks connected false when the fetch rejects, without clearing prior data', async () => {
    (fetch as ReturnType<typeof vi.fn>)
      .mockResolvedValueOnce({ ok: true, json: async () => ({ a: 1 }) })
      .mockRejectedValueOnce(new Error('network down'));
    const { createStatusResource } = await import('./dashboard.svelte');
    const resource = createStatusResource(1000);
    resource.start();
    await vi.advanceTimersByTimeAsync(0);
    expect(resource.data).toEqual({ a: 1 });

    await vi.advanceTimersByTimeAsync(1000);
    expect(resource.connected).toBe(false);
    expect(resource.data).toEqual({ a: 1 });
    resource.stop();
  });

  it('marks connected false on a non-ok HTTP response', async () => {
    (fetch as ReturnType<typeof vi.fn>).mockResolvedValue({ ok: false, status: 500, json: async () => ({}) });
    const { createStatusResource } = await import('./dashboard.svelte');
    const resource = createStatusResource(1000);
    resource.start();
    await vi.advanceTimersByTimeAsync(0);
    expect(resource.connected).toBe(false);
    resource.stop();
  });

  it('stop halts further polling', async () => {
    (fetch as ReturnType<typeof vi.fn>).mockResolvedValue({ ok: true, json: async () => ({ n: 1 }) });
    const { createStatusResource } = await import('./dashboard.svelte');
    const resource = createStatusResource(1000);
    resource.start();
    await vi.advanceTimersByTimeAsync(0);
    resource.stop();
    const callsBefore = (fetch as ReturnType<typeof vi.fn>).mock.calls.length;
    await vi.advanceTimersByTimeAsync(5000);
    expect((fetch as ReturnType<typeof vi.fn>).mock.calls.length).toBe(callsBefore);
  });

  it('records a non-negative latencyMs after a successful fetch', async () => {
    (fetch as ReturnType<typeof vi.fn>).mockResolvedValue({ ok: true, json: async () => ({}) });
    const { createStatusResource } = await import('./dashboard.svelte');
    const resource = createStatusResource(1000);
    resource.start();
    await vi.advanceTimersByTimeAsync(0);
    expect(resource.latencyMs).not.toBeNull();
    expect(resource.latencyMs!).toBeGreaterThanOrEqual(0);
    resource.stop();
  });
});
