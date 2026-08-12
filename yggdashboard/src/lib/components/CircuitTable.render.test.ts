// @vitest-environment jsdom
import { describe, it, expect } from 'vitest';
import { render, screen } from '@testing-library/svelte';
import CircuitTable from './CircuitTable.svelte';

describe('CircuitTable render', () => {
  it('shows both empty-state messages when there are no circuits at all', () => {
    render(CircuitTable, { props: { originated: [], relayed: [], onSelectOriginated: () => {}, onSelectRelayed: () => {} } });
    expect(screen.getByText('No originated circuits.')).toBeInTheDocument();
    expect(screen.getByText('No relayed circuits.')).toBeInTheDocument();
  });
});
