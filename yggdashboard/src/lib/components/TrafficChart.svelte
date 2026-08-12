<script lang="ts" module>
  import type { HistorySample } from '$lib/api-types';

  export type SeriesKey = 'rxRate' | 'txRate' | 'garlicRelayedRate' | 'garlicOriginatedRate';

  /**
   * Maps history's samples for one series onto an SVG polyline's
   * "x,y x,y ..." points string, scaled into [padding, width-padding] x
   * [padding, height-padding]. Pure and exported for direct unit
   * testing - the rest of this component is just markup around it.
   */
  export function scalePoints(
    history: HistorySample[],
    key: SeriesKey,
    width: number,
    height: number,
    maxValue: number,
    padding: number
  ): string {
    if (history.length < 2) return '';
    const safeMax = Math.max(1, maxValue);
    const minT = history[0].t;
    const maxT = history[history.length - 1].t;
    const spanT = Math.max(1, maxT - minT);
    return history
      .map((s) => {
        const x = padding + ((s.t - minT) / spanT) * (width - 2 * padding);
        const y = height - padding - (s[key] / safeMax) * (height - 2 * padding);
        return `${x.toFixed(1)},${y.toFixed(1)}`;
      })
      .join(' ');
  }
</script>

<script lang="ts">
  let { history }: { history: HistorySample[] } = $props();

  const SERIES: Array<{ key: SeriesKey; label: string; color: string }> = [
    { key: 'rxRate', label: 'Download', color: 'var(--accent)' },
    { key: 'txRate', label: 'Upload', color: 'var(--ok)' },
    { key: 'garlicRelayedRate', label: 'Garlic transit', color: 'var(--warn)' },
    { key: 'garlicOriginatedRate', label: 'Garlic originated', color: 'var(--bad)' }
  ];

  let enabled = $state<Record<SeriesKey, boolean>>({
    rxRate: true,
    txRate: true,
    garlicRelayedRate: true,
    garlicOriginatedRate: true
  });

  const width = 800;
  const height = 200;
  const padding = 8;

  let maxValue = $derived(
    Math.max(1, ...history.flatMap((s) => SERIES.filter((series) => enabled[series.key]).map((series) => s[series.key])))
  );

  function toggle(key: SeriesKey) {
    enabled[key] = !enabled[key];
  }
</script>

<div class="chart">
  <svg viewBox="0 0 {width} {height}" preserveAspectRatio="none" role="img" aria-label="Traffic over the last 5 minutes">
    {#if history.length < 2}
      <text x={width / 2} y={height / 2} text-anchor="middle" class="empty">Waiting for data…</text>
    {:else}
      {#each SERIES as series (series.key)}
        {#if enabled[series.key]}
          <polyline points={scalePoints(history, series.key, width, height, maxValue, padding)} fill="none" stroke={series.color} stroke-width="2" />
        {/if}
      {/each}
    {/if}
  </svg>
  <div class="legend">
    {#each SERIES as series (series.key)}
      <button type="button" class:enabled={enabled[series.key]} onclick={() => toggle(series.key)} style="--series-color: {series.color}">
        <span class="swatch"></span>
        {series.label}
      </button>
    {/each}
  </div>
</div>

<style>
  .chart {
    background: var(--bg-raised);
    border: 1px solid var(--border);
    border-radius: var(--radius);
    padding: 0.75rem;
  }
  svg {
    width: 100%;
    height: 200px;
    display: block;
  }
  .empty {
    fill: var(--text-dim);
    font-size: 14px;
  }
  .legend {
    display: flex;
    flex-wrap: wrap;
    gap: 0.75rem;
    margin-top: 0.5rem;
  }
  .legend button {
    display: inline-flex;
    align-items: center;
    gap: 0.35em;
    background: transparent;
    border: none;
    color: var(--text-dim);
    font-size: 0.8rem;
    cursor: pointer;
    opacity: 0.5;
  }
  .legend button.enabled {
    color: var(--text);
    opacity: 1;
  }
  .swatch {
    width: 0.7em;
    height: 0.7em;
    border-radius: 2px;
    background: var(--series-color);
  }
</style>
