import { json } from '@sveltejs/kit';
import { poller } from '$lib/server/instance';
import { computeGraph } from '$lib/server/graph';
import type { RequestHandler } from './$types';

export const GET: RequestHandler = async () => {
  await poller.waitUntilReady(2000);
  return json(computeGraph(poller.getSnapshot()));
};
