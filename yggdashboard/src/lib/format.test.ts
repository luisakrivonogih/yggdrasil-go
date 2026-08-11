import { describe, it, expect } from 'vitest';
import { formatBytes, formatRate, formatLatency, formatUptime, formatPercent, truncateKey } from './format';

describe('formatBytes', () => {
  it('formats bytes under 1KB as whole bytes', () => {
    expect(formatBytes(512)).toBe('512 B');
  });
  it('formats kilobytes', () => {
    expect(formatBytes(2048)).toBe('2.0 KB');
  });
  it('formats megabytes', () => {
    expect(formatBytes(5 * 1024 * 1024)).toBe('5.0 MB');
  });
  it('formats gigabytes', () => {
    expect(formatBytes(3 * 1024 * 1024 * 1024)).toBe('3.0 GB');
  });
});

describe('formatRate', () => {
  it('appends /s to a byte-rate value', () => {
    expect(formatRate(1024)).toBe('1.0 KB/s');
  });
});

describe('formatLatency', () => {
  it('converts nanoseconds to milliseconds', () => {
    expect(formatLatency(1_500_000)).toBe('1.5 ms');
  });
  it('renders an em-dash for null (no latency known)', () => {
    expect(formatLatency(null)).toBe('—');
  });
});

describe('formatUptime', () => {
  it('formats seconds only under a minute', () => {
    expect(formatUptime(45)).toBe('45s');
  });
  it('formats minutes and seconds under an hour', () => {
    expect(formatUptime(125)).toBe('2m 5s');
  });
  it('formats hours and minutes under a day', () => {
    expect(formatUptime(3 * 3600 + 20 * 60)).toBe('3h 20m');
  });
  it('formats days and hours at or over a day', () => {
    expect(formatUptime(3 * 86400 + 14 * 3600)).toBe('3d 14h');
  });
});

describe('formatPercent', () => {
  it('formats to one decimal place with a % sign', () => {
    expect(formatPercent(63.44)).toBe('63.4%');
  });
});

describe('truncateKey', () => {
  it('shortens a long key to prefix...suffix', () => {
    expect(truncateKey('abcdef1234567890', 8, 4)).toBe('abcdef12...7890');
  });
  it('returns a short key unchanged', () => {
    expect(truncateKey('short', 8, 4)).toBe('short');
  });
});
