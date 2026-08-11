import { json } from '@sveltejs/kit';
import { poller } from '$lib/server/instance';
import { computeStats } from '$lib/server/stats';
import type { RequestHandler } from './$types';

export const GET: RequestHandler = async () => {
  await poller.waitUntilReady(2000);
  return json(computeStats(poller.getSnapshot()));
};
