import { poller } from '$lib/server/instance';
import { computeStatus } from '$lib/server/status';
import type { LayoutServerLoad } from './$types';

export const load: LayoutServerLoad = async () => {
  await poller.waitUntilReady(2000);
  return { status: computeStatus(poller.getSnapshot()) };
};
