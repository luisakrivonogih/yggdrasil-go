export interface DashboardConfig {
  adminSocket: string;
  pollIntervalMs: number;
  historyWindowMs: number;
}

// unix:///var/run/yggdrasil.sock matches src/config/defaults_linux.go's
// DefaultAdminListen exactly - verified against the Go source, not
// assumed. HOST/PORT are deliberately not read here: this dashboard
// process is a normal @sveltejs/adapter-node app, which already reads
// those itself when started via `node build/index.js`.
export function loadConfig(): DashboardConfig {
  return {
    adminSocket: process.env.ADMIN_SOCKET ?? 'unix:///var/run/yggdrasil.sock',
    pollIntervalMs: Number(process.env.POLL_INTERVAL_MS ?? 1500),
    historyWindowMs: Number(process.env.HISTORY_WINDOW_MS ?? 5 * 60 * 1000)
  };
}
