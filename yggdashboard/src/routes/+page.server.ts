import { poller } from '$lib/server/instance';
import { computeStats } from '$lib/server/stats';
import type { PageServerLoad } from './$types';

export const load: PageServerLoad = async () => {
  await poller.waitUntilReady(2000);
  const snap = poller.getSnapshot();
  return {
    stats: computeStats(snap),
    self: { buildName: snap.self.build_name, buildVersion: snap.self.build_version, address: snap.self.address, key: snap.self.key },
    peerCount: snap.peers.length,
    peersUp: snap.peers.filter((p) => p.up).length
  };
};
