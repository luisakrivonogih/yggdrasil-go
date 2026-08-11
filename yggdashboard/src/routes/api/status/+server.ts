import { json } from '@sveltejs/kit';
import { poller } from '$lib/server/instance';
import { computeStatus } from '$lib/server/status';
import type { RequestHandler } from './$types';

export const GET: RequestHandler = async () => {
  await poller.waitUntilReady(2000);
  return json(computeStatus(poller.getSnapshot()));
};
