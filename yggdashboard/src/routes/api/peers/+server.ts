import { json } from '@sveltejs/kit';
import { poller } from '$lib/server/instance';
import { computePeers } from '$lib/server/peers';
import type { RequestHandler } from './$types';

export const GET: RequestHandler = async () => {
  await poller.waitUntilReady(2000);
  return json(computePeers(poller.getSnapshot()));
};
