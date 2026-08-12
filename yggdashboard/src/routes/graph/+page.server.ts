import { poller } from '$lib/server/instance';
import { computeGraph } from '$lib/server/graph';
import type { PageServerLoad } from './$types';

export const load: PageServerLoad = async () => {
  await poller.waitUntilReady(2000);
  return { graph: computeGraph(poller.getSnapshot()) };
};
