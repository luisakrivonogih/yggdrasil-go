import { AdminClient } from './admin-client';
import { loadConfig } from './config';
import { Poller } from './poll';

const config = loadConfig();
const client = new AdminClient(config.adminSocket);

/**
 * The one Poller instance for this server process. Created at module
 * load time (Node caches modules, so every importer gets this same
 * instance) and started immediately - every /api/* route and every
 * +page.server.ts load function reads from it, none of them poll the
 * admin socket themselves.
 */
export const poller = new Poller(client, config.pollIntervalMs, config.historyWindowMs);
poller.start();

// Vite's dev-mode SSR module graph re-evaluates this module on HMR
// (unlike a production build, where Node's module cache guarantees a
// single evaluation) - without this, every edit touching instance.ts or
// its dependency chain would spawn a brand-new Poller/AdminClient/timer
// on top of the old one, which is never told to stop.
if (import.meta.hot) {
  import.meta.hot.dispose(() => {
    poller.stop();
  });
}
