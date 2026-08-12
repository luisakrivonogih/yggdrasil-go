import { poller } from '$lib/server/instance';
import { computePeers } from '$lib/server/peers';
import type { PageServerLoad } from './$types';

export const load: PageServerLoad = async () => {
  await poller.waitUntilReady(2000);
  return { peers: computePeers(poller.getSnapshot()) };
};
