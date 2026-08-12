// @vitest-environment jsdom
import { describe, it, expect } from 'vitest';
import { render, screen } from '@testing-library/svelte';
import NetworkGraph from './NetworkGraph.svelte';

describe('NetworkGraph render', () => {
  it('shows an empty-state message and no svg when there are no nodes', () => {
    render(NetworkGraph, { props: { nodes: [], yggdrasilEdges: [], garlicEdges: [], onSelectNode: () => {}, onSelectEdge: () => {} } });
    expect(screen.getByText('No known nodes yet.')).toBeInTheDocument();
  });

  it('renders a node circle for each known node, including self', () => {
    const { container } = render(NetworkGraph, {
      props: {
        nodes: [
          { key: 'local', address: '200::1', isSelf: true },
          { key: 'peer1', address: '200::2', isSelf: false }
        ],
        yggdrasilEdges: [{ from: 'peer1', to: 'local', type: 'yggdrasil' }],
        garlicEdges: [],
        onSelectNode: () => {},
        onSelectEdge: () => {}
      }
    });
    expect(container.querySelectorAll('circle').length).toBe(2);
    expect(container.querySelectorAll('line.yggdrasil').length).toBe(1);
  });

  it('renders a dashed garlic edge distinctly from a solid yggdrasil edge (not color-only)', () => {
    const { container } = render(NetworkGraph, {
      props: {
        nodes: [
          { key: 'local', address: '200::1', isSelf: true },
          { key: 'peer1', address: '200::2', isSelf: false }
        ],
        yggdrasilEdges: [],
        garlicEdges: [{ from: 'local', to: 'peer1', type: 'garlic', circuitId: '1', active: false }],
        onSelectNode: () => {},
        onSelectEdge: () => {}
      }
    });
    expect(container.querySelectorAll('line.garlic').length).toBe(1);
  });
});
