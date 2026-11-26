export interface Machine {
  id: string;
  name: string;
  os: string;
  roots: string[];
  online: boolean;
  lastSeenAt: string;
}

export interface FSEntry {
  name: string;
  path: string;
}

export interface FSListing {
  machineName: string;
  os: string;
  currentPath: string;
  parentPath?: string;
  roots: string[];
  entries: FSEntry[];
}

export interface Project {
  id: string;
  name: string;
  machineName: string;
  path: string;
  archived: boolean;
  createdAt: string;
}

export interface CommandSpec {
  name: string;
  description: string;
  args?: string;
  sendsRaw?: string;
}

export interface OptionChoice {
  value: string;
  label: string;
}

export interface ProviderOption {
  id: string;
  label: string;
  description?: string;
  flag: string;
  defaultValue?: string;
  choices: OptionChoice[];
}

export interface InspectAction {
  id: string;
  label: string;
  description?: string;
  args: string[];
}

export interface ProviderMenus {
  options: ProviderOption[];
  inspectActions: InspectAction[];
}

export interface AgentMachineInstallation {
  machineName: string;
  online: boolean;
  installed: boolean;
  path?: string;
}

export interface AgentManifest {
  agentId: string;
  name: string;
  launchCommand: string;
  args: string[];
  chatMode: string;
  providerMenus?: ProviderMenus;
  commands: CommandSpec[];
  installed: boolean;
  executablePath?: string;
  installations: Record<string, AgentMachineInstallation>;
}

export interface SessionMeta {
  id: string;
  projectId: string;
  machineName: string;
  agentId: string;
  cwd: string;
  status: string;
  exitCode?: number | null;
  waitingApproval: boolean;
  snapshotPath?: string;
  snapshotBytes?: number;
  options: Record<string, string>;
  createdAt: string;
  closedAt?: string | null;
}

export interface ChatMessage {
  id: string;
  sessionId: string;
  role: 'user' | 'assistant' | 'system' | string;
  content: string;
  status: 'pending' | 'running' | 'streaming' | 'done' | 'error' | 'cancelled' | string;
  exitCode?: number | null;
  durationMs: number;
  createdAt: string;
}

export interface CreateMessageResponse {
  userMessage: ChatMessage;
  assistantMessage: ChatMessage;
}

export interface MenuActionResult {
  actionId: string;
  label: string;
  output: string;
  exitCode: number;
  durationMs: number;
}

export interface ExecResult {
  exitCode: number;
  output: string;
  durationMs: number;
}

export interface NotificationConfig {
  enabled: boolean;
  ntfyUrl: string;
  ntfyTopic: string;
}

export interface TokenRecord {
  id: string;
  name: string;
  scope: 'mobile' | 'runner' | string;
  createdAt: string;
  revokedAt: string | null;
}

export interface CreatedToken {
  id: string;
  name: string;
  scope: 'mobile' | 'runner' | string;
  token: string;
  createdAt: string;
}

export interface CreateProjectInput {
  name: string;
  machineName: string;
  parentPath: string;
  gitInit: boolean;
}

export interface CreateSessionInput {
  projectId: string;
  agentId: string;
  cols?: number;
  rows?: number;
}

export interface SessionQueryParams {
  machine?: string;
  status?: string;
  projectId?: string;
  q?: string;
}

export type ThemeMode = 'system' | 'light' | 'dark';
