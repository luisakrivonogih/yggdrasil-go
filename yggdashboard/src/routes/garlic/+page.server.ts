import { poller } from '$lib/server/instance';
import { computeGarlic } from '$lib/server/garlic';
import type { PageServerLoad } from './$types';

export const load: PageServerLoad = async () => {
  await poller.waitUntilReady(2000);
  return { garlic: computeGarlic(poller.getSnapshot()) };
};
