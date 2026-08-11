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
