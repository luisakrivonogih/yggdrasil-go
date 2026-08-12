import { poller } from '$lib/server/instance';
import { computeCircuits } from '$lib/server/circuits';
import type { PageServerLoad } from './$types';

export const load: PageServerLoad = async () => {
  await poller.waitUntilReady(2000);
  return { circuits: computeCircuits(poller.getSnapshot()) };
};
