import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { ChatView } from '../components/ChatView';
import type { WebSocketLike } from '../api/chatSocket';
import type {
  AgentManifest,
  ChatMessage,
  MenuActionResult,
  SessionMeta,
} from '../api/types';

class MockWebSocket implements WebSocketLike {
  static instances: MockWebSocket[] = [];

  readonly url: string;
  readonly protocols?: string | string[];
  readyState = 0;

  onopen: ((event: Event) => void) | null = null;
  onmessage: ((event: MessageEvent) => void) | null = null;
  onerror: ((event: Event) => void) | null = null;
  onclose: ((event: CloseEvent) => void) | null = null;

  closed = false;

  constructor(url: string, protocols?: string | string[]) {
    this.url = url;
    this.protocols = protocols;
    MockWebSocket.instances.push(this);
  }

  close(): void {
    this.closed = true;
    this.readyState = 3;
  }

  send(): void {
    // no-op for unit tests
  }

  emitOpen(): void {
    this.readyState = 1;
    this.onopen?.(new Event('open'));
  }

  emitFrame(frame: Record<string, unknown>): void {
    this.onmessage?.(new MessageEvent('message', { data: JSON.stringify(frame) }));
  }
}

const mockFetchSessionMessages = vi.fn();
const mockSendChatMessage = vi.fn();
const mockCancelChatTurn = vi.fn();
const mockUpdateSessionOptions = vi.fn();
const mockRunProviderMenuAction = vi.fn();
const mockFetchSessionCommand = vi.fn();
const mockCloseSession = vi.fn();
const mockCreateSession = vi.fn();
const mockRefreshSessions = vi.fn();

const copilotManifest: AgentManifest = {
  agentId: 'copilot',
  name: 'GitHub Copilot CLI',
  launchCommand: 'copilot',
  args: [],
  chatMode: 'headless',
  installed: true,
  installations: {},
  commands: [
    { name: '/explain', description: 'Expliquer un fichier ou symbole', args: '<cible>' },
    { name: '/review', description: 'Relire le diff courant' },
  ],
  providerMenus: {
    options: [
      {
        id: 'model',
        label: 'Modèle',
        description: 'Choix du modèle LLM',
        flag: '--model',
        defaultValue: 'gpt-4.1',
        choices: [
          { value: 'gpt-4.1', label: 'GPT-4.1' },
          { value: 'claude-sonnet', label: 'Claude Sonnet' },
        ],
      },
    ],
    inspectActions: [
      {
        id: 'version',
        label: 'Version CLI',
        description: 'Affiche la version installée',
        args: ['--version'],
      },
    ],
  },
};

const mockClient = {
  token: 'test-token',
  wsUrl: (path: string) => `ws://localhost:8088${path}?token=test-token`,
  fetchSessionMessages: mockFetchSessionMessages,
  sendChatMessage: mockSendChatMessage,
  cancelChatTurn: mockCancelChatTurn,
  updateSessionOptions: mockUpdateSessionOptions,
  runProviderMenuAction: mockRunProviderMenuAction,
  fetchSessionCommand: mockFetchSessionCommand,
  closeSession: mockCloseSession,
  createSession: mockCreateSession,
};

const mockAgents = [copilotManifest];

vi.mock('../context/AppContext', () => ({
  useApp: () => ({
    client: mockClient,
    agents: mockAgents,
    refreshSessions: mockRefreshSessions,
  }),
}));

function makeRunningSession(overrides?: Partial<SessionMeta>): SessionMeta {
  return {
    id: 'sess-42',
    projectId: 'proj-1',
    machineName: 'homelab-01',
    agentId: 'copilot',
    cwd: '/srv/repos/agentpilot',
    status: 'running',
    waitingApproval: false,
    options: {},
    createdAt: '2026-11-26T20:00:00Z',
    ...overrides,
  };
}

describe('ChatView', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    MockWebSocket.instances = [];
    mockFetchSessionMessages.mockResolvedValue([]);
    mockRefreshSessions.mockResolvedValue(undefined);
  });

  it('sends a message, streams chat-delta + chat-done via WS, renders Markdown code block, and supports Stop', async () => {
    const session = makeRunningSession();

    const userMsg: ChatMessage = {
      id: 'm-user-1',
      sessionId: session.id,
      role: 'user',
      content: 'Montre un exemple TypeScript',
      status: 'done',
      durationMs: 0,
      createdAt: '2026-11-26T20:01:00Z',
    };
    const assistantMsg: ChatMessage = {
      id: 'm-asst-1',
      sessionId: session.id,
      role: 'assistant',
      content: '',
      status: 'streaming',
      durationMs: 0,
      createdAt: '2026-11-26T20:01:01Z',
    };
    mockSendChatMessage.mockResolvedValue({
      userMessage: userMsg,
      assistantMessage: assistantMsg,
    });
    mockCancelChatTurn.mockResolvedValue({ cancelled: true });

    render(
      <ChatView
        session={session}
        projectName="AgentPilot"
        onBack={vi.fn()}
        onSessionClosed={vi.fn()}
        webSocketFactory={MockWebSocket}
      />,
    );

    expect(await screen.findByText('Comment puis-je vous aider ?')).toBeInTheDocument();

    // Verify WebSocket connected with subprotocols
    expect(MockWebSocket.instances).toHaveLength(1);
    const ws = MockWebSocket.instances[0];
    expect(ws.protocols).toEqual(['agentpilot.v1', 'bearer.test-token']);

    act(() => {
      ws.emitOpen();
    });
    expect(await screen.findByText('Connecté')).toBeInTheDocument();

    // Send a prompt
    const textarea = screen.getByPlaceholderText(/Message à GitHub Copilot CLI/i);
    fireEvent.change(textarea, { target: { value: 'Montre un exemple TypeScript' } });
    fireEvent.click(screen.getByRole('button', { name: 'Envoyer' }));

    await waitFor(() => {
      expect(mockSendChatMessage).toHaveBeenCalledWith(
        'sess-42',
        'Montre un exemple TypeScript',
      );
    });
    expect(await screen.findByText('Montre un exemple TypeScript')).toBeInTheDocument();

    // While generating, Stop button is shown and calls cancelChatTurn
    const stopBtn = screen.getByRole('button', { name: 'Arrêter' });
    fireEvent.click(stopBtn);
    await waitFor(() => {
      expect(mockCancelChatTurn).toHaveBeenCalledWith('sess-42');
    });

    // Receive chat-delta chunks + chat-done with fenced code block
    act(() => {
      ws.emitFrame({
        type: 'chat-delta',
        sessionId: session.id,
        messageId: 'm-asst-1',
        delta: 'Voici un exemple :\n\n```ts\nconst answer = 42;\n```',
      });
    });

    expect(await screen.findByText('const answer = 42;')).toBeInTheDocument();

    act(() => {
      ws.emitFrame({
        type: 'chat-done',
        sessionId: session.id,
        messageId: 'm-asst-1',
        content: 'Voici un exemple :\n\n```ts\nconst answer = 42;\n```',
        status: 'done',
        exitCode: 0,
        durationMs: 1200,
      });
    });

    expect(await screen.findByText('ts')).toBeInTheDocument();
    expect(screen.getByText('1.2 s')).toBeInTheDocument();
    expect(screen.getAllByRole('button', { name: /Copier/i }).length).toBeGreaterThanOrEqual(2);
  });

  it('opens ProviderMenuDrawer to change options and run an inspect action', async () => {
    const session = makeRunningSession();

    const updatedSession: SessionMeta = {
      ...session,
      options: { model: 'claude-sonnet' },
    };
    mockUpdateSessionOptions.mockResolvedValue(updatedSession);

    const inspectRes: MenuActionResult = {
      actionId: 'version',
      label: 'Version CLI',
      output: 'copilot 1.4.2',
      exitCode: 0,
      durationMs: 145,
    };
    mockRunProviderMenuAction.mockResolvedValue(inspectRes);

    render(
      <ChatView
        session={session}
        onBack={vi.fn()}
        onSessionClosed={vi.fn()}
        webSocketFactory={MockWebSocket}
      />,
    );

    await screen.findByText('Comment puis-je vous aider ?');

    // Open ProviderMenuDrawer
    fireEvent.click(screen.getByRole('button', { name: /Menus/i }));
    expect(screen.getByRole('dialog', { name: 'Menus du fournisseur' })).toBeInTheDocument();

    // Update model option
    fireEvent.click(screen.getByRole('button', { name: 'Claude Sonnet' }));
    await waitFor(() => {
      expect(mockUpdateSessionOptions).toHaveBeenCalledWith('sess-42', {
        model: 'claude-sonnet',
      });
    });

    // Switch to Menus CLI tab and run inspect action
    fireEvent.click(screen.getByRole('tab', { name: 'Menus CLI' }));
    fireEvent.click(screen.getByRole('button', { name: /Version CLI/i }));

    await waitFor(() => {
      expect(mockRunProviderMenuAction).toHaveBeenCalledWith('sess-42', 'version');
    });
    expect(await screen.findByText('copilot 1.4.2')).toBeInTheDocument();
    expect(screen.getByText('exit 0')).toBeInTheDocument();
  });

  it('shows the headless command in the Debug tab for debugging', async () => {
    const session = makeRunningSession();
    mockFetchSessionCommand.mockResolvedValue({
      sessionId: session.id,
      agentId: 'copilot',
      agentName: 'GitHub Copilot CLI',
      chatMode: 'headless',
      launchCommand: 'copilot',
      firstTurnArgs: ['--headless', '-p', '{prompt}'],
      nextTurnArgs: ['--headless', '-p', '{prompt}', '--continue'],
      cwd: '/srv/repos/agentpilot',
      machineName: 'homelab-01',
      options: { model: 'claude-sonnet' },
    });

    render(
      <ChatView
        session={session}
        onBack={vi.fn()}
        onSessionClosed={vi.fn()}
        webSocketFactory={MockWebSocket}
      />,
    );

    await screen.findByText('Comment puis-je vous aider ?');

    // Open the Debug tab via the composer chip
    fireEvent.click(screen.getByRole('button', { name: 'Debug' }));

    expect(await screen.findByText('Ligne de commande')).toBeInTheDocument();
    expect(mockFetchSessionCommand).toHaveBeenCalledWith('sess-42');
    expect(screen.getAllByText('copilot').length).toBeGreaterThan(0);
    expect(screen.getAllByText('/srv/repos/agentpilot').length).toBeGreaterThan(0);

    // Non-default selected options appear as active flags
    expect(screen.getByText('--model')).toBeInTheDocument();
    expect(screen.getByText('Claude Sonnet')).toBeInTheDocument();
  });

  it('renders closed session footer and relaunches session via Relancer', async () => {
    const closedSession = makeRunningSession({ status: 'closed' });
    const relaunchedSession = makeRunningSession({ id: 'sess-99', status: 'running' });
    mockCreateSession.mockResolvedValue(relaunchedSession);

    const onOpenSession = vi.fn();

    render(
      <ChatView
        session={closedSession}
        onBack={vi.fn()}
        onSessionClosed={vi.fn()}
        onOpenSession={onOpenSession}
        webSocketFactory={MockWebSocket}
      />,
    );

    expect(await screen.findByText('Session terminée')).toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: /Relancer/i }));

    await waitFor(() => {
      expect(mockCreateSession).toHaveBeenCalledWith({
        projectId: 'proj-1',
        agentId: 'copilot',
      });
    });
    expect(onOpenSession).toHaveBeenCalledWith(relaunchedSession);
  });
});
