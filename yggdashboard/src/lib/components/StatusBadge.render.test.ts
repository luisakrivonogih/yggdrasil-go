// @vitest-environment jsdom
import { describe, it, expect } from 'vitest';
import { render, screen } from '@testing-library/svelte';
import StatusBadge from './StatusBadge.svelte';

describe('StatusBadge render', () => {
  it.each([
    ['online', 'Online'],
    ['degraded', 'Degraded'],
    ['disconnected', 'Disconnected']
  ] as const)('renders the %s label for status %s', (status, label) => {
    render(StatusBadge, { props: { status } });
    expect(screen.getByText(label)).toBeInTheDocument();
  });
});
