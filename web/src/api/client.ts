import type {
  AgentManifest,
  ChatMessage,
  CommandSpec,
  CreatedToken,
  CreateMessageResponse,
  CreateProjectInput,
  CreateSessionInput,
  ExecResult,
  FSListing,
  Machine,
  MenuActionResult,
  ModelsRefreshResult,
  NotificationConfig,
  Project,
  SessionCommandInfo,
  SessionMeta,
  SessionQueryParams,
  TokenRecord,
} from './types';

export class ApiError extends Error {
  readonly status: number;

  constructor(status: number, message: string) {
    super(message);
    this.name = 'ApiError';
    this.status = status;
  }
}

function normalizeBaseUrl(raw?: string): string {
  if (!raw) return '';
  return raw.trim().replace(/\/+$/, '');
}

export class AgentPilotClient {
  readonly baseUrl: string;
  readonly token: string;

  constructor(token: string, baseUrl = '') {
    this.token = token.trim();
    this.baseUrl = normalizeBaseUrl(baseUrl);
  }

  private async request<T>(path: string, init: RequestInit = {}): Promise<T> {
    const headers = new Headers(init.headers);
    if (this.token) {
      headers.set('Authorization', `Bearer ${this.token}`);
    }
    if (init.body !== undefined && !headers.has('Content-Type')) {
      headers.set('Content-Type', 'application/json');
    }

    const res = await fetch(`${this.baseUrl}${path}`, {
      ...init,
      headers,
    });

    if (!res.ok) {
      let message = `Erreur HTTP ${res.status}`;
      try {
        const payload = (await res.json()) as { error?: string };
        if (payload?.error) {
          message = payload.error;
        }
      } catch {
        // Keep status fallback when body is not JSON.
      }
      throw new ApiError(res.status, message);
    }

    if (res.status === 204) {
      return undefined as T;
    }

    return (await res.json()) as T;
  }

  wsUrl(path: string): string {
    const base = this.baseUrl || window.location.origin;
    const url = new URL(path, base);
    url.protocol = url.protocol === 'https:' ? 'wss:' : 'ws:';
    if (this.token) {
      url.searchParams.set('token', this.token);
    }
    return url.toString();
  }

  fetchMachines(): Promise<Machine[]> {
    return this.request<Machine[]>('/api/machines');
  }

  listMachineDirectories(machineName: string, dirPath?: string): Promise<FSListing> {
    const params = new URLSearchParams();
    if (dirPath !== undefined && dirPath !== '') {
      params.set('path', dirPath);
    }
    const qs = params.toString();
    const encoded = encodeURIComponent(machineName);
    return this.request<FSListing>(`/api/machines/${encoded}/fs${qs ? `?${qs}` : ''}`);
  }

  fetchProjects(machine?: string): Promise<Project[]> {
    const params = new URLSearchParams();
    if (machine) params.set('machine', machine);
    const qs = params.toString();
    return this.request<Project[]>(`/api/projects${qs ? `?${qs}` : ''}`);
  }

  createProject(input: CreateProjectInput): Promise<Project> {
    return this.request<Project>('/api/projects', {
      method: 'POST',
      body: JSON.stringify(input),
    });
  }

  deleteProject(id: string, deleteFiles = false): Promise<{ deleted: boolean }> {
    const params = new URLSearchParams();
    if (deleteFiles) params.set('deleteFiles', 'true');
    const qs = params.toString();
    return this.request<{ deleted: boolean }>(
      `/api/projects/${encodeURIComponent(id)}${qs ? `?${qs}` : ''}`,
      { method: 'DELETE' },
    );
  }

  fetchAgents(machine?: string): Promise<AgentManifest[]> {
    const params = new URLSearchParams();
    if (machine) params.set('machine', machine);
    const qs = params.toString();
    return this.request<AgentManifest[]>(`/api/agents${qs ? `?${qs}` : ''}`);
  }

  fetchAgentCommands(agentId: string): Promise<CommandSpec[]> {
    return this.request<CommandSpec[]>(`/api/agents/${encodeURIComponent(agentId)}/commands`);
  }

  refreshAgentModels(agentId: string, machine?: string): Promise<ModelsRefreshResult> {
    const params = new URLSearchParams();
    if (machine) params.set('machine', machine);
    const qs = params.toString();
    return this.request<ModelsRefreshResult>(
      `/api/agents/${encodeURIComponent(agentId)}/models/refresh${qs ? `?${qs}` : ''}`,
      { method: 'POST' },
    );
  }

  fetchSessions(query?: SessionQueryParams): Promise<SessionMeta[]> {
    const params = new URLSearchParams();
    if (query?.machine) params.set('machine', query.machine);
    if (query?.status) params.set('status', query.status);
    if (query?.projectId) params.set('projectId', query.projectId);
    if (query?.q) params.set('q', query.q);
    const qs = params.toString();
    return this.request<SessionMeta[]>(`/api/sessions${qs ? `?${qs}` : ''}`);
  }

  createSession(input: CreateSessionInput): Promise<SessionMeta> {
    return this.request<SessionMeta>('/api/sessions', {
      method: 'POST',
      body: JSON.stringify({
        projectId: input.projectId,
        agentId: input.agentId,
        cols: input.cols ?? 100,
        rows: input.rows ?? 30,
      }),
    });
  }

  closeSession(id: string): Promise<{ closed?: boolean; deleted?: boolean }> {
    return this.request<{ closed?: boolean; deleted?: boolean }>(
      `/api/sessions/${encodeURIComponent(id)}`,
      { method: 'DELETE' },
    );
  }

  fetchSessionMessages(sessionId: string): Promise<ChatMessage[]> {
    return this.request<ChatMessage[]>(`/api/sessions/${encodeURIComponent(sessionId)}/messages`);
  }

  fetchSessionCommand(sessionId: string): Promise<SessionCommandInfo> {
    return this.request<SessionCommandInfo>(
      `/api/sessions/${encodeURIComponent(sessionId)}/command`,
    );
  }

  sendChatMessage(sessionId: string, content: string): Promise<CreateMessageResponse> {
    return this.request<CreateMessageResponse>(
      `/api/sessions/${encodeURIComponent(sessionId)}/messages`,
      {
        method: 'POST',
        body: JSON.stringify({ content }),
      },
    );
  }

  cancelChatTurn(sessionId: string): Promise<{ cancelled?: boolean }> {
    return this.request<{ cancelled?: boolean }>(
      `/api/sessions/${encodeURIComponent(sessionId)}/cancel`,
      { method: 'POST' },
    );
  }

  updateSessionOptions(sessionId: string, options: Record<string, string>): Promise<SessionMeta> {
    return this.request<SessionMeta>(`/api/sessions/${encodeURIComponent(sessionId)}/options`, {
      method: 'PATCH',
      body: JSON.stringify({ options }),
    });
  }

  runProviderMenuAction(sessionId: string, actionId: string): Promise<MenuActionResult> {
    return this.request<MenuActionResult>(
      `/api/sessions/${encodeURIComponent(sessionId)}/menu-action`,
      {
        method: 'POST',
        body: JSON.stringify({ actionId }),
      },
    );
  }

  execCommand(projectId: string, command: string, timeoutSec = 60): Promise<ExecResult> {
    return this.request<ExecResult>(`/api/projects/${encodeURIComponent(projectId)}/exec`, {
      method: 'POST',
      body: JSON.stringify({ command, timeoutSec }),
    });
  }

  fetchNotificationConfig(): Promise<NotificationConfig> {
    return this.request<NotificationConfig>('/api/config/notifications');
  }

  fetchTokens(): Promise<TokenRecord[]> {
    return this.request<TokenRecord[]>('/api/tokens');
  }

  createToken(name: string, scope: 'mobile' | 'runner'): Promise<CreatedToken> {
    return this.request<CreatedToken>('/api/tokens', {
      method: 'POST',
      body: JSON.stringify({ name, scope }),
    });
  }

  revokeToken(id: string): Promise<{ revoked: boolean }> {
    return this.request<{ revoked: boolean }>(`/api/tokens/${encodeURIComponent(id)}`, {
      method: 'DELETE',
    });
  }
}
