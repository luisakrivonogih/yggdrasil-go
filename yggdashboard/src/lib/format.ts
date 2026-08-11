export function formatBytes(n: number): string {
  if (n >= 1024 ** 3) return (n / 1024 ** 3).toFixed(1) + ' GB';
  if (n >= 1024 ** 2) return (n / 1024 ** 2).toFixed(1) + ' MB';
  if (n >= 1024) return (n / 1024).toFixed(1) + ' KB';
  return Math.round(n) + ' B';
}

export function formatRate(bytesPerSecond: number): string {
  return formatBytes(bytesPerSecond) + '/s';
}

export function formatLatency(ns: number | null): string {
  if (ns === null) return '—';
  return (ns / 1e6).toFixed(1) + ' ms';
}

export function formatUptime(totalSeconds: number): string {
  const seconds = Math.floor(totalSeconds);
  const days = Math.floor(seconds / 86400);
  const hours = Math.floor((seconds % 86400) / 3600);
  const minutes = Math.floor((seconds % 3600) / 60);
  const secs = seconds % 60;
  if (days > 0) return `${days}d ${hours}h`;
  if (hours > 0) return `${hours}h ${minutes}m`;
  if (minutes > 0) return `${minutes}m ${secs}s`;
  return `${secs}s`;
}

export function formatPercent(n: number): string {
  return `${n.toFixed(1)}%`;
}

/** Shortens key to "prefix...suffix"; returns key unchanged if it's already that short or shorter. */
export function truncateKey(key: string, prefixLen = 8, suffixLen = 4): string {
  if (key.length <= prefixLen + suffixLen + 3) return key;
  return `${key.slice(0, prefixLen)}...${key.slice(-suffixLen)}`;
}
