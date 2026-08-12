// @vitest-environment jsdom
import { describe, it, expect } from 'vitest';
import { render, screen } from '@testing-library/svelte';
import PeerTable from './PeerTable.svelte';

describe('PeerTable render', () => {
  it('shows an empty-state message and no table when there are no peers', () => {
    render(PeerTable, { props: { peers: [], onSelect: () => {} } });
    expect(screen.getByText('No peers connected.')).toBeInTheDocument();
    expect(screen.queryByRole('table')).not.toBeInTheDocument();
  });
});
