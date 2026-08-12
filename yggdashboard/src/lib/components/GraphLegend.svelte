<div class="legend">
  <div class="item"><span class="swatch solid"></span> Yggdrasil connection</div>
  <div class="item"><span class="swatch dashed"></span> Garlic circuit</div>
  <div class="item"><span class="swatch active"></span> Active relay traffic</div>
</div>

<style>
  .legend {
    display: flex;
    gap: 1.25rem;
    flex-wrap: wrap;
    font-size: 0.8rem;
    color: var(--text-dim);
    margin-bottom: 0.5rem;
  }
  .item {
    display: inline-flex;
    align-items: center;
    gap: 0.4em;
  }
  .swatch {
    display: inline-block;
    width: 1.5em;
    height: 0;
    border-top-width: 2px;
    border-top-style: solid;
  }
  .swatch.solid {
    border-top-color: var(--text-dim);
  }
  .swatch.dashed {
    border-top-color: var(--warn);
    border-top-style: dashed;
  }
  .swatch.active {
    border-top-color: var(--bad);
    border-top-width: 3px;
  }
</style>
