import { describe, it, expect, beforeEach, afterEach } from 'vitest';
import { loadConfig } from './config';

const ENV_KEYS = ['ADMIN_SOCKET', 'POLL_INTERVAL_MS', 'HISTORY_WINDOW_MS'] as const;
const savedEnv: Record<string, string | undefined> = {};

beforeEach(() => {
  for (const key of ENV_KEYS) {
    savedEnv[key] = process.env[key];
    delete process.env[key];
  }
});

afterEach(() => {
  for (const key of ENV_KEYS) {
    if (savedEnv[key] === undefined) delete process.env[key];
    else process.env[key] = savedEnv[key];
  }
});

describe('loadConfig', () => {
  it('defaults to the platform admin socket path, a 1.5s poll interval, and 5 minutes of history', () => {
    const config = loadConfig();
    expect(config).toEqual({
      adminSocket: 'unix:///var/run/yggdrasil.sock',
      pollIntervalMs: 1500,
      historyWindowMs: 5 * 60 * 1000
    });
  });

  it('reads every field from the environment when set', () => {
    process.env.ADMIN_SOCKET = 'tcp://127.0.0.1:9001';
    process.env.POLL_INTERVAL_MS = '2000';
    process.env.HISTORY_WINDOW_MS = '60000';

    expect(loadConfig()).toEqual({
      adminSocket: 'tcp://127.0.0.1:9001',
      pollIntervalMs: 2000,
      historyWindowMs: 60000
    });
  });
});
