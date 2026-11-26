import type { ChatMessage } from './types';

export type ChatSocketStatus = 'connecting' | 'connected' | 'reconnecting' | 'closed';

export interface WebSocketLike {
  readonly readyState: number;
  onopen: ((event: Event) => void) | null;
  onmessage: ((event: MessageEvent) => void) | null;
  onerror: ((event: Event) => void) | null;
  onclose: ((event: CloseEvent) => void) | null;
  close(code?: number, reason?: string): void;
  send(data: string): void;
}

export type WebSocketConstructor = new (
  url: string,
  protocols?: string | string[],
) => WebSocketLike;

export interface ChatDoneEvent {
  messageId: string;
  content: string;
  status: string;
  exitCode?: number | null;
  durationMs: number;
  error?: string;
}

export interface ChatSocketHandlers {
  onSync?: (messages: ChatMessage[]) => void;
  onMessage?: (message: ChatMessage) => void;
  onDelta?: (messageId: string, delta: string) => void;
  onDone?: (event: ChatDoneEvent) => void;
  onSessionClosed?: () => void;
  onStatusChange?: (status: ChatSocketStatus) => void;
}

export interface ChatSocketOptions {
  url: string;
  token: string;
  handlers: ChatSocketHandlers;
  webSocketFactory?: WebSocketConstructor;
  initialBackoffMs?: number;
  maxBackoffMs?: number;
}

interface RawMobileFrame {
  type?: string;
  sessionId?: string;
  messageId?: string;
  delta?: string;
  content?: string;
  status?: string;
  exitCode?: number | null;
  durationMs?: number;
  error?: string;
  messages?: ChatMessage[];
  chatMessage?: ChatMessage;
}

const DEFAULT_INITIAL_BACKOFF_MS = 500;
const DEFAULT_MAX_BACKOFF_MS = 15_000;

export function upsertChatMessage(
  messages: ChatMessage[],
  incoming: ChatMessage,
): ChatMessage[] {
  const idx = messages.findIndex((m) => m.id === incoming.id);
  if (idx === -1) {
    return [...messages, incoming];
  }
  const next = messages.slice();
  next[idx] = { ...next[idx], ...incoming };
  return next;
}

export class ChatSocket {
  private readonly url: string;
  private readonly token: string;
  private readonly handlers: ChatSocketHandlers;
  private readonly WS: WebSocketConstructor;
  private readonly initialBackoffMs: number;
  private readonly maxBackoffMs: number;

  private ws: WebSocketLike | null = null;
  private reconnectTimer: ReturnType<typeof setTimeout> | null = null;
  private backoffMs: number;
  private disposed = false;
  private sessionClosed = false;
  private hasConnectedOnce = false;

  constructor(options: ChatSocketOptions) {
    this.url = options.url;
    this.token = options.token.trim();
    this.handlers = options.handlers;
    this.WS = options.webSocketFactory ?? (window.WebSocket as unknown as WebSocketConstructor);
    this.initialBackoffMs = options.initialBackoffMs ?? DEFAULT_INITIAL_BACKOFF_MS;
    this.maxBackoffMs = options.maxBackoffMs ?? DEFAULT_MAX_BACKOFF_MS;
    this.backoffMs = this.initialBackoffMs;
  }

  connect(): void {
    if (this.disposed || this.sessionClosed) return;
    this.clearReconnectTimer();
    this.handlers.onStatusChange?.(this.hasConnectedOnce ? 'reconnecting' : 'connecting');

    const protocols = this.token
      ? ['agentpilot.v1', `bearer.${this.token}`]
      : ['agentpilot.v1'];

    let socket: WebSocketLike;
    try {
      socket = new this.WS(this.url, protocols);
    } catch {
      this.scheduleReconnect();
      return;
    }

    this.ws = socket;

    socket.onopen = () => {
      if (this.disposed || this.ws !== socket) return;
      this.hasConnectedOnce = true;
      this.backoffMs = this.initialBackoffMs;
      this.handlers.onStatusChange?.('connected');
    };

    socket.onmessage = (event: MessageEvent) => {
      if (this.disposed || this.ws !== socket) return;
      this.handleRawMessage(event.data);
    };

    socket.onerror = () => {
      // onclose handles reconnection
    };

    socket.onclose = () => {
      if (this.ws === socket) {
        this.ws = null;
      }
      if (this.disposed || this.sessionClosed) {
        this.handlers.onStatusChange?.('closed');
        return;
      }
      this.scheduleReconnect();
    };
  }

  dispose(): void {
    this.disposed = true;
    this.clearReconnectTimer();
    if (this.ws) {
      const socket = this.ws;
      this.ws = null;
      socket.onopen = null;
      socket.onmessage = null;
      socket.onerror = null;
      socket.onclose = null;
      try {
        socket.close(1000, 'client dispose');
      } catch {
        // Ignore close errors during teardown.
      }
    }
    this.handlers.onStatusChange?.('closed');
  }

  private handleRawMessage(raw: unknown): void {
    if (typeof raw !== 'string') return;
    let frame: RawMobileFrame;
    try {
      frame = JSON.parse(raw) as RawMobileFrame;
    } catch {
      return;
    }

    switch (frame.type) {
      case 'chat-sync':
        this.handlers.onSync?.(Array.isArray(frame.messages) ? frame.messages : []);
        break;
      case 'chat-message':
        if (frame.chatMessage) {
          this.handlers.onMessage?.(frame.chatMessage);
        }
        break;
      case 'chat-delta':
        if (frame.messageId && typeof frame.delta === 'string') {
          this.handlers.onDelta?.(frame.messageId, frame.delta);
        }
        break;
      case 'chat-done':
        if (frame.messageId) {
          this.handlers.onDone?.({
            messageId: frame.messageId,
            content: frame.content ?? '',
            status: frame.status ?? 'done',
            exitCode: frame.exitCode,
            durationMs: frame.durationMs ?? 0,
            error: frame.error,
          });
        }
        break;
      case 'session-closed':
        this.sessionClosed = true;
        this.clearReconnectTimer();
        this.handlers.onStatusChange?.('closed');
        this.handlers.onSessionClosed?.();
        if (this.ws) {
          try {
            this.ws.close(1000, 'session closed');
          } catch {
            // Ignore close errors.
          }
        }
        break;
      default:
        break;
    }
  }

  private scheduleReconnect(): void {
    if (this.disposed || this.sessionClosed) return;
    this.handlers.onStatusChange?.('reconnecting');
    const delay = this.backoffMs;
    this.backoffMs = Math.min(this.backoffMs * 2, this.maxBackoffMs);
    this.clearReconnectTimer();
    this.reconnectTimer = setTimeout(() => {
      this.reconnectTimer = null;
      this.connect();
    }, delay);
  }

  private clearReconnectTimer(): void {
    if (this.reconnectTimer !== null) {
      clearTimeout(this.reconnectTimer);
      this.reconnectTimer = null;
    }
  }
}
