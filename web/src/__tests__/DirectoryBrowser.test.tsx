import { useState } from 'react';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { DirectoryBrowser } from '../components/DirectoryBrowser';
import type { AgentPilotClient } from '../api/client';
import type { FSListing, Machine } from '../api/types';

function createMockMachine(overrides?: Partial<Machine>): Machine {
  return {
    id: 'm-1',
    name: 'dev-win',
    os: 'windows',
    roots: ['C:\\Users\\alice\\repos'],
    online: true,
    lastSeenAt: '2026-11-26T20:00:00Z',
    ...overrides,
  };
}

function createMockClient(listings: Record<string, FSListing>) {
  const listMachineDirectories = vi.fn(async (_machineName: string, dirPath?: string) => {
    const key = dirPath ?? '';
    const found = listings[key];
    if (!found) {
      throw new Error(`Unexpected path: ${key}`);
    }
    return found;
  });

  return {
    client: { listMachineDirectories } as unknown as AgentPilotClient,
    listMachineDirectories,
  };
}

function StatefulBrowser({
  client,
  machine,
  initialPath,
}: {
  client: AgentPilotClient;
  machine: Machine;
  initialPath: string;
}) {
  const [path, setPath] = useState(initialPath);
  return (
    <div>
      <div data-testid="selected-path">{path}</div>
      <DirectoryBrowser
        client={client}
        machine={machine}
        currentPath={path}
        onSelectPath={setPath}
      />
    </div>
  );
}

describe('DirectoryBrowser', () => {
  it('navigates subfolders, parent directory, and breadcrumbs', async () => {
    const machine = createMockMachine();

    const listings: Record<string, FSListing> = {
      'C:\\Users\\alice\\repos': {
        machineName: 'dev-win',
        os: 'windows',
        currentPath: 'C:\\Users\\alice\\repos',
        parentPath: 'C:\\Users\\alice',
        roots: ['C:\\Users\\alice\\repos'],
        entries: [
          { name: 'agent-pilot', path: 'C:\\Users\\alice\\repos\\agent-pilot' },
          { name: 'notes-app', path: 'C:\\Users\\alice\\repos\\notes-app' },
        ],
      },
      'C:\\Users\\alice\\repos\\agent-pilot': {
        machineName: 'dev-win',
        os: 'windows',
        currentPath: 'C:\\Users\\alice\\repos\\agent-pilot',
        parentPath: 'C:\\Users\\alice\\repos',
        roots: ['C:\\Users\\alice\\repos'],
        entries: [
          { name: 'server', path: 'C:\\Users\\alice\\repos\\agent-pilot\\server' },
          { name: 'web', path: 'C:\\Users\\alice\\repos\\agent-pilot\\web' },
        ],
      },
      'C:\\Users\\alice': {
        machineName: 'dev-win',
        os: 'windows',
        currentPath: 'C:\\Users\\alice',
        parentPath: 'C:\\Users',
        roots: ['C:\\Users\\alice\\repos'],
        entries: [{ name: 'repos', path: 'C:\\Users\\alice\\repos' }],
      },
    };

    const { client, listMachineDirectories } = createMockClient(listings);
    render(
      <StatefulBrowser
        client={client}
        machine={machine}
        initialPath={'C:\\Users\\alice\\repos'}
      />,
    );

    expect(await screen.findByText('agent-pilot')).toBeInTheDocument();
    expect(screen.getByText('notes-app')).toBeInTheDocument();

    // Navigate into subfolder
    fireEvent.click(screen.getByRole('button', { name: 'agent-pilot' }));
    expect(await screen.findByText('server')).toBeInTheDocument();
    expect(screen.getByTestId('selected-path')).toHaveTextContent(
      'C:\\Users\\alice\\repos\\agent-pilot',
    );

    // Navigate up using parent button
    fireEvent.click(screen.getByTitle('Dossier parent'));
    expect(await screen.findByText('notes-app')).toBeInTheDocument();
    expect(screen.getByTestId('selected-path')).toHaveTextContent('C:\\Users\\alice\\repos');

    // Navigate via breadcrumb click
    fireEvent.click(screen.getByRole('button', { name: 'alice' }));
    await waitFor(() => {
      expect(screen.getByTestId('selected-path')).toHaveTextContent('C:\\Users\\alice');
    });
    expect(listMachineDirectories).toHaveBeenCalledWith('dev-win', 'C:\\Users\\alice');
  });

  it('filters visible subfolders instantaneously', async () => {
    const machine = createMockMachine();

    const listings: Record<string, FSListing> = {
      'C:\\Users\\alice\\repos': {
        machineName: 'dev-win',
        os: 'windows',
        currentPath: 'C:\\Users\\alice\\repos',
        parentPath: 'C:\\Users\\alice',
        roots: ['C:\\Users\\alice\\repos'],
        entries: [
          { name: 'alpha-service', path: 'C:\\Users\\alice\\repos\\alpha-service' },
          { name: 'beta-ui', path: 'C:\\Users\\alice\\repos\\beta-ui' },
        ],
      },
    };

    const { client } = createMockClient(listings);
    render(
      <StatefulBrowser
        client={client}
        machine={machine}
        initialPath={'C:\\Users\\alice\\repos'}
      />,
    );

    expect(await screen.findByText('alpha-service')).toBeInTheDocument();
    expect(screen.getByText('beta-ui')).toBeInTheDocument();

    const filterInput = screen.getByPlaceholderText('Filtrer les sous-dossiers…');
    fireEvent.change(filterInput, { target: { value: 'beta' } });

    expect(screen.queryByText('alpha-service')).not.toBeInTheDocument();
    expect(screen.getByText('beta-ui')).toBeInTheDocument();
  });

  it('supports Windows __drives__ listing and drive selection', async () => {
    const machine = createMockMachine({ os: 'windows' });

    const listings: Record<string, FSListing> = {
      'C:\\Users\\alice\\repos': {
        machineName: 'dev-win',
        os: 'windows',
        currentPath: 'C:\\Users\\alice\\repos',
        parentPath: 'C:\\Users\\alice',
        roots: ['C:\\Users\\alice\\repos'],
        entries: [{ name: 'demo', path: 'C:\\Users\\alice\\repos\\demo' }],
      },
      __drives__: {
        machineName: 'dev-win',
        os: 'windows',
        currentPath: '__drives__',
        roots: ['C:\\Users\\alice\\repos'],
        entries: [
          { name: 'C:\\', path: 'C:\\' },
          { name: 'D:\\', path: 'D:\\' },
        ],
      },
      'D:\\': {
        machineName: 'dev-win',
        os: 'windows',
        currentPath: 'D:\\',
        parentPath: '__drives__',
        roots: ['C:\\Users\\alice\\repos'],
        entries: [{ name: 'Projects', path: 'D:\\Projects' }],
      },
    };

    const { client, listMachineDirectories } = createMockClient(listings);
    render(
      <StatefulBrowser
        client={client}
        machine={machine}
        initialPath={'C:\\Users\\alice\\repos'}
      />,
    );

    expect(await screen.findByText('demo')).toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: /Disques/i }));
    expect(await screen.findByText('Disques Windows')).toBeInTheDocument();
    expect(listMachineDirectories).toHaveBeenCalledWith('dev-win', '__drives__');

    fireEvent.click(screen.getByRole('button', { name: 'D:\\' }));
    expect(await screen.findByText('Projects')).toBeInTheDocument();
    expect(screen.getByTestId('selected-path')).toHaveTextContent('D:\\');
  });
});
