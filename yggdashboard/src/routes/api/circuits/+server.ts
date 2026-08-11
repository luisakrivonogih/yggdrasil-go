import { json } from '@sveltejs/kit';
import { poller } from '$lib/server/instance';
import { computeCircuits } from '$lib/server/circuits';
import type { RequestHandler } from './$types';

export const GET: RequestHandler = async () => {
  await poller.waitUntilReady(2000);
  return json(computeCircuits(poller.getSnapshot()));
};
