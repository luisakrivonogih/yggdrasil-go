import { describe, it, expect } from 'vitest';
import { ageSeconds, remainingSeconds } from './CircuitTable.svelte';

describe('ageSeconds', () => {
  it('returns elapsed seconds since createdAt', () => {
    const createdAt = new Date(Date.now() - 65_000).toISOString();
    expect(ageSeconds(createdAt, Date.now())).toBeCloseTo(65, 0);
  });
});

describe('remainingSeconds', () => {
  it('returns seconds until expiresAt', () => {
    const expiresAt = new Date(Date.now() + 30_000).toISOString();
    expect(remainingSeconds(expiresAt, Date.now())).toBeCloseTo(30, 0);
  });

  it('clamps to zero once past expiry', () => {
    const expiresAt = new Date(Date.now() - 5_000).toISOString();
    expect(remainingSeconds(expiresAt, Date.now())).toBe(0);
  });
});
