import { json } from '@sveltejs/kit';
import { poller } from '$lib/server/instance';
import { computeGarlic } from '$lib/server/garlic';
import type { RequestHandler } from './$types';

export const GET: RequestHandler = async () => {
  await poller.waitUntilReady(2000);
  return json(computeGarlic(poller.getSnapshot()));
};
