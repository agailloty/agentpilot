import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { NewSessionModal } from '../components/NewSessionModal';
import type { AgentManifest, Machine, Project, SessionMeta } from '../api/types';

const mockCreateSession = vi.fn();
const mockRefreshSessions = vi.fn();

const mockMachine: Machine = {
  id: 'm-1',
  name: 'workstation',
  os: 'windows',
  roots: ['C:\\repos'],
  online: true,
  lastSeenAt: '2026-11-26T20:00:00Z',
};

const mockProject: Project = {
  id: 'proj-1',
  name: 'mobile-harness',
  machineName: 'workstation',
  path: 'C:\\repos\\mobile-harness',
  archived: false,
  createdAt: '2026-11-26T18:00:00Z',
};

const installedAgent: AgentManifest = {
  agentId: 'copilot',
  name: 'GitHub Copilot CLI',
  launchCommand: 'copilot',
  args: [],
  chatMode: 'headless',
  commands: [],
  installed: true,
  executablePath: 'C:\\Program Files\\GitHub\\copilot.exe',
  installations: {
    workstation: {
      machineName: 'workstation',
      online: true,
      installed: true,
      path: 'C:\\Program Files\\GitHub\\copilot.exe',
    },
  },
};

const uninstalledAgent: AgentManifest = {
  agentId: 'mistral',
  name: 'Mistral Vibe CLI',
  launchCommand: 'vibe',
  args: [],
  chatMode: 'headless',
  commands: [],
  installed: false,
  installations: {
    workstation: {
      machineName: 'workstation',
      online: true,
      installed: false,
    },
  },
};

vi.mock('../context/AppContext', () => ({
  useApp: () => ({
    client: {
      createSession: mockCreateSession,
    },
    projects: [mockProject],
    agents: [installedAgent, uninstalledAgent],
    machines: [mockMachine],
    refreshSessions: mockRefreshSessions,
  }),
}));

describe('NewSessionModal', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mockRefreshSessions.mockResolvedValue(undefined);
  });

  it('displays Installé + executable path for installed CLI and disables uninstalled CLI', async () => {
    const createdSession: SessionMeta = {
      id: 'sess-1',
      projectId: 'proj-1',
      machineName: 'workstation',
      agentId: 'copilot',
      cwd: 'C:\\repos\\mobile-harness',
      status: 'running',
      waitingApproval: false,
      options: {},
      createdAt: '2026-11-26T21:00:00Z',
    };
    mockCreateSession.mockResolvedValue(createdSession);

    const onClose = vi.fn();
    const onCreated = vi.fn();

    render(
      <NewSessionModal
        open
        defaultProjectId="proj-1"
        onClose={onClose}
        onCreated={onCreated}
      />,
    );

    const copilotBtn = screen.getByRole('button', { name: /GitHub Copilot CLI/i });
    const mistralBtn = screen.getByRole('button', { name: /Mistral Vibe CLI/i });

    expect(copilotBtn).toBeEnabled();
    expect(copilotBtn).toHaveTextContent('Installé');
    expect(copilotBtn).toHaveTextContent('C:\\Program Files\\GitHub\\copilot.exe');

    expect(mistralBtn).toBeDisabled();
    expect(mistralBtn).toHaveTextContent('Non installé');

    fireEvent.click(screen.getByRole('button', { name: /Démarrer la session/i }));

    await waitFor(() => {
      expect(mockCreateSession).toHaveBeenCalledWith({
        projectId: 'proj-1',
        agentId: 'copilot',
      });
    });
    expect(onCreated).toHaveBeenCalledWith(createdSession);
    expect(onClose).toHaveBeenCalled();
  });
});
