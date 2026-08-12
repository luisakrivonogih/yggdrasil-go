import { describe, it, expect } from 'vitest';
import { scalePoints } from './TrafficChart.svelte';

describe('scalePoints', () => {
  it('maps a two-sample series across the full width and height', () => {
    const history = [
      { t: 0, rxRate: 0, txRate: 0, garlicRelayedRate: 0, garlicOriginatedRate: 0 },
      { t: 1000, rxRate: 100, txRate: 0, garlicRelayedRate: 0, garlicOriginatedRate: 0 }
    ];
    const points = scalePoints(history, 'rxRate', 100, 200, 100, 0);
    expect(points).toBe('0.0,200.0 100.0,0.0');
  });

  it('returns an empty string for fewer than two samples', () => {
    expect(scalePoints([], 'rxRate', 100, 200, 10, 0)).toBe('');
    expect(
      scalePoints([{ t: 0, rxRate: 1, txRate: 0, garlicRelayedRate: 0, garlicOriginatedRate: 0 }], 'rxRate', 100, 200, 10, 0)
    ).toBe('');
  });

  it('clamps against a maxValue of at least 1 to avoid division by zero when every sample is 0', () => {
    const history = [
      { t: 0, rxRate: 0, txRate: 0, garlicRelayedRate: 0, garlicOriginatedRate: 0 },
      { t: 1000, rxRate: 0, txRate: 0, garlicRelayedRate: 0, garlicOriginatedRate: 0 }
    ];
    expect(() => scalePoints(history, 'rxRate', 100, 200, 0, 0)).not.toThrow();
  });
});
