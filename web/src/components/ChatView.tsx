import {
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
  type FormEvent,
  type KeyboardEvent,
} from 'react';
import {
  AlertCircle,
  ArrowDown,
  ArrowLeft,
  ArrowUp,
  Bot,
  Check,
  ChevronDown,
  Copy,
  FileCode2,
  FolderOpen,
  GitBranch,
  Loader2,
  Power,
  RotateCcw,
  SlidersHorizontal,
  Sparkles,
  Square,
  TestTube2,
} from 'lucide-react';
import { useApp } from '../context/AppContext';
import {
  ChatSocket,
  upsertChatMessage,
  type ChatSocketStatus,
  type WebSocketConstructor,
} from '../api/chatSocket';
import type { ChatMessage, MenuActionResult, SessionMeta } from '../api/types';
import { MarkdownMessage } from './MarkdownMessage';
import { ProviderMenuDrawer, type ProviderMenuTab } from './ProviderMenuDrawer';

export interface ChatViewProps {
  session: SessionMeta;
  projectName?: string;
  onBack: () => void;
  onSessionClosed: () => void;
  onOpenSession?: (session: SessionMeta) => void;
  webSocketFactory?: WebSocketConstructor;
}

const STARTER_SUGGESTIONS = [
  {
    title: 'Architecture',
    prompt: 'Explique l’architecture de ce projet',
    icon: FileCode2,
  },
  {
    title: 'Diagnostic',
    prompt: 'Vérifie les tests et erreurs récentes',
    icon: TestTube2,
  },
  {
    title: 'Historique Git',
    prompt: 'Résume les derniers changements git',
    icon: GitBranch,
  },
];

function formatDuration(ms: number): string {
  if (ms < 1000) return `${ms} ms`;
  return `${(ms / 1000).toFixed(1)} s`;
}

function isMessageGenerating(msg: ChatMessage): boolean {
  return (
    msg.role === 'assistant' &&
    (msg.status === 'streaming' || msg.status === 'running' || msg.status === 'pending')
  );
}

export function ChatView({
  session,
  projectName,
  onBack,
  onSessionClosed,
  onOpenSession,
  webSocketFactory,
}: ChatViewProps) {
  const { client, agents, refreshSessions } = useApp();

  const [sessionState, setSessionState] = useState<SessionMeta>(session);
  const [messages, setMessages] = useState<ChatMessage[]>([]);
  const [loading, setLoading] = useState(true);
  const [wsStatus, setWsStatus] = useState<ChatSocketStatus>(
    session.status === 'running' ? 'connecting' : 'closed',
  );
  const [input, setInput] = useState('');
  const [sending, setSending] = useState(false);
  const [cancelling, setCancelling] = useState(false);
  const [closing, setClosing] = useState(false);
  const [confirmClose, setConfirmClose] = useState(false);
  const [relaunching, setRelaunching] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [copiedId, setCopiedId] = useState<string | null>(null);
  const [drawerOpen, setDrawerOpen] = useState(false);
  const [drawerTab, setDrawerTab] = useState<ProviderMenuTab>('options');
  const [isAtBottom, setIsAtBottom] = useState(true);
  const [slashDismissed, setSlashDismissed] = useState(false);

  const feedRef = useRef<HTMLDivElement | null>(null);
  const textareaRef = useRef<HTMLTextAreaElement | null>(null);

  useEffect(() => {
    setSessionState(session);
    setConfirmClose(false);
  }, [session]);

  const manifest = useMemo(
    () => agents.find((a) => a.agentId === sessionState.agentId),
    [agents, sessionState.agentId],
  );

  const isRunning = sessionState.status === 'running';
  const isGenerating = useMemo(() => messages.some(isMessageGenerating), [messages]);
  const providerName = manifest?.name ?? sessionState.agentId;

  const scrollToBottom = useCallback((behavior: ScrollBehavior = 'smooth') => {
    const el = feedRef.current;
    if (!el) return;
    if (typeof el.scrollTo === 'function') {
      el.scrollTo({ top: el.scrollHeight, behavior });
    } else {
      el.scrollTop = el.scrollHeight;
    }
    setIsAtBottom(true);
  }, []);

  // Load initial messages over HTTP (also serves closed sessions).
  useEffect(() => {
    if (!client) return;
    let cancelled = false;
    setLoading(true);
    setError(null);
    client
      .fetchSessionMessages(session.id)
      .then((list) => {
        if (!cancelled) {
          setMessages(list);
        }
      })
      .catch((err) => {
        if (!cancelled) {
          setError(err instanceof Error ? err.message : 'Erreur de chargement des messages');
        }
      })
      .finally(() => {
        if (!cancelled) setLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, [client, session.id]);

  // Connect live WebSocket while running.
  useEffect(() => {
    if (!client || session.status !== 'running') {
      setWsStatus('closed');
      return;
    }

    const socket = new ChatSocket({
      url: client.wsUrl(`/ws/sessions/${encodeURIComponent(session.id)}`),
      token: client.token,
      webSocketFactory,
      handlers: {
        onStatusChange: (status) => setWsStatus(status),
        onSync: (synced) => {
          setMessages(synced);
          setLoading(false);
        },
        onMessage: (msg) => {
          setMessages((prev) => upsertChatMessage(prev, msg));
        },
        onDelta: (messageId, delta) => {
          setMessages((prev) => {
            const idx = prev.findIndex((m) => m.id === messageId);
            if (idx === -1) {
              return [
                ...prev,
                {
                  id: messageId,
                  sessionId: session.id,
                  role: 'assistant',
                  content: delta,
                  status: 'streaming',
                  durationMs: 0,
                  createdAt: new Date().toISOString(),
                },
              ];
            }
            const next = prev.slice();
            next[idx] = {
              ...next[idx],
              content: next[idx].content + delta,
              status: 'streaming',
            };
            return next;
          });
        },
        onDone: (evt) => {
          setMessages((prev) => {
            const idx = prev.findIndex((m) => m.id === evt.messageId);
            if (idx === -1) {
              return [
                ...prev,
                {
                  id: evt.messageId,
                  sessionId: session.id,
                  role: 'assistant',
                  content: evt.content,
                  status: evt.status,
                  exitCode: evt.exitCode,
                  durationMs: evt.durationMs,
                  createdAt: new Date().toISOString(),
                },
              ];
            }
            const next = prev.slice();
            next[idx] = {
              ...next[idx],
              content: evt.content || next[idx].content,
              status: evt.status,
              exitCode: evt.exitCode,
              durationMs: evt.durationMs,
            };
            return next;
          });
        },
        onSessionClosed: () => {
          setSessionState((prev) => ({ ...prev, status: 'closed' }));
          void refreshSessions();
        },
      },
    });

    socket.connect();
    return () => {
      socket.dispose();
    };
  }, [client, session.id, session.status, webSocketFactory, refreshSessions]);

  // Smart autoscroll when messages change and user is near bottom.
  useEffect(() => {
    if (isAtBottom) {
      scrollToBottom('auto');
    }
  }, [messages, isAtBottom, scrollToBottom]);

  // Auto-grow textarea height.
  useEffect(() => {
    const el = textareaRef.current;
    if (!el) return;
    el.style.height = 'auto';
    el.style.height = `${Math.min(el.scrollHeight, 180)}px`;
  }, [input]);

  function handleFeedScroll() {
    const el = feedRef.current;
    if (!el) return;
    const distance = el.scrollHeight - el.scrollTop - el.clientHeight;
    setIsAtBottom(distance < 80);
  }

  const activeOptionChips = useMemo(() => {
    const opts = manifest?.providerMenus?.options ?? [];
    const current = sessionState.options ?? {};
    return opts
      .map((opt) => {
        const val = current[opt.id];
        if (!val || val === (opt.defaultValue ?? '')) return null;
        const choice = opt.choices.find((c) => c.value === val);
        return {
          id: opt.id,
          label: opt.label,
          valueLabel: choice?.label ?? val,
        };
      })
      .filter((x): x is { id: string; label: string; valueLabel: string } => x !== null);
  }, [manifest, sessionState.options]);

  const slashSuggestions = useMemo(() => {
    if (slashDismissed || !input.startsWith('/') || input.includes('\n')) return [];
    const commands = manifest?.commands ?? [];
    const query = input.slice(1).split(/\s+/)[0].toLowerCase();
    return commands
      .filter((cmd) => {
        const cleanName = cmd.name.replace(/^\//, '').toLowerCase();
        return (
          cleanName.includes(query) || cmd.description.toLowerCase().includes(query)
        );
      })
      .slice(0, 6);
  }, [input, manifest, slashDismissed]);

  async function sendPrompt(text: string) {
    const trimmed = text.trim();
    if (!client || !trimmed || !isRunning || sending) return;
    setSending(true);
    setError(null);
    setSlashDismissed(false);
    try {
      const res = await client.sendChatMessage(sessionState.id, trimmed);
      setMessages((prev) =>
        upsertChatMessage(upsertChatMessage(prev, res.userMessage), res.assistantMessage),
      );
      setInput('');
      setIsAtBottom(true);
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Échec d’envoi du message');
    } finally {
      setSending(false);
    }
  }

  function handleFormSubmit(e: FormEvent) {
    e.preventDefault();
    void sendPrompt(input);
  }

  function handleTextareaKeyDown(e: KeyboardEvent<HTMLTextAreaElement>) {
    if (e.key === 'Escape' && slashSuggestions.length > 0) {
      e.preventDefault();
      setSlashDismissed(true);
      return;
    }
    if (e.key === 'Enter' && !e.shiftKey) {
      e.preventDefault();
      if (!isGenerating && input.trim()) {
        void sendPrompt(input);
      }
    }
  }

  async function handleCancelTurn() {
    if (!client || cancelling) return;
    setCancelling(true);
    setError(null);
    try {
      await client.cancelChatTurn(sessionState.id);
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Impossible d’interrompre la génération');
    } finally {
      setCancelling(false);
    }
  }

  async function handleCloseSession() {
    if (!client || closing) return;
    setClosing(true);
    setError(null);
    try {
      await client.closeSession(sessionState.id);
      setSessionState((prev) => ({ ...prev, status: 'closed' }));
      setConfirmClose(false);
      await refreshSessions();
      onSessionClosed();
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Impossible de fermer la session');
    } finally {
      setClosing(false);
    }
  }

  async function handleRelaunch() {
    if (!client || relaunching) return;
    setRelaunching(true);
    setError(null);
    try {
      const next = await client.createSession({
        projectId: sessionState.projectId,
        agentId: sessionState.agentId,
      });
      await refreshSessions();
      onOpenSession?.(next);
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Impossible de relancer la session');
    } finally {
      setRelaunching(false);
    }
  }

  async function handleUpdateOptions(nextOptions: Record<string, string>) {
    if (!client) return;
    const updated = await client.updateSessionOptions(sessionState.id, nextOptions);
    setSessionState(updated);
    void refreshSessions();
  }

  async function handleRunInspectAction(actionId: string): Promise<MenuActionResult> {
    if (!client) {
      throw new Error('Client non authentifié');
    }
    return client.runProviderMenuAction(sessionState.id, actionId);
  }

  function handleSelectCommand(commandText: string) {
    setInput(commandText);
    setSlashDismissed(true);
    setTimeout(() => textareaRef.current?.focus(), 0);
  }

  async function handleCopyMessage(msg: ChatMessage) {
    try {
      await navigator.clipboard?.writeText(msg.content);
      setCopiedId(msg.id);
      setTimeout(() => {
        setCopiedId((cur) => (cur === msg.id ? null : cur));
      }, 1800);
    } catch {
      // Ignore clipboard failures.
    }
  }

  function openDrawer(tab: ProviderMenuTab) {
    if (drawerOpen && drawerTab === tab) {
      setDrawerOpen(false);
      return;
    }
    setDrawerTab(tab);
    setDrawerOpen(true);
  }

  const statusBadge = !isRunning
    ? {
        label: 'Terminée',
        dotClass: 'bg-zinc-400',
        pillClass:
          'border-zinc-200/90 bg-zinc-100/80 text-zinc-600 dark:border-zinc-800 dark:bg-zinc-900 dark:text-zinc-400',
      }
    : wsStatus === 'connected'
      ? {
          label: 'Connecté',
          dotClass: 'bg-emerald-500',
          pillClass:
            'border-emerald-200/80 bg-emerald-50/70 text-emerald-700 dark:border-emerald-500/25 dark:bg-emerald-500/10 dark:text-emerald-300',
        }
      : {
          label: 'Reconnexion…',
          dotClass: 'bg-amber-500 animate-pulse',
          pillClass:
            'border-amber-200/80 bg-amber-50/70 text-amber-700 dark:border-amber-500/25 dark:bg-amber-500/10 dark:text-amber-300',
        };

  return (
    <div className="flex h-full min-h-0 w-full overflow-hidden bg-white dark:bg-[#09090B]">
      {/* Primary Chat Column */}
      <div className="relative flex min-h-0 min-w-0 flex-1 flex-col">
        {/* Edge-to-Edge Sticky Header */}
        <header className="z-10 flex h-14 shrink-0 items-center justify-between gap-3 border-b border-zinc-200/80 bg-white/85 px-4 backdrop-blur-md md:px-6 dark:border-zinc-800/80 dark:bg-[#09090B]/85">
          {/* Left: Back + Breadcrumb + Workspace */}
          <div className="flex min-w-0 flex-1 items-center gap-2.5">
            <button
              type="button"
              onClick={onBack}
              aria-label="Retour"
              title="Retour"
              className="inline-flex h-8 shrink-0 items-center gap-1.5 rounded-lg border border-zinc-200/90 bg-white px-2.5 text-xs font-medium text-zinc-600 transition hover:border-zinc-300 hover:bg-zinc-50 hover:text-zinc-900 dark:border-zinc-800 dark:bg-zinc-900 dark:text-zinc-300 dark:hover:bg-zinc-800"
            >
              <ArrowLeft className="h-3.5 w-3.5" />
              <span className="hidden lg:inline">Retour</span>
            </button>

            <div className="min-w-0 flex-1">
              <div className="flex min-w-0 items-center gap-2">
                {projectName && (
                  <span className="truncate text-[13px] font-semibold tracking-tight text-zinc-900 dark:text-zinc-100">
                    {projectName}
                  </span>
                )}
                {projectName && (
                  <span className="hidden text-zinc-300 sm:inline dark:text-zinc-700">/</span>
                )}
                <button
                  type="button"
                  onClick={() => openDrawer('options')}
                  className="inline-flex shrink-0 items-center gap-1 rounded-md border border-zinc-200/80 bg-zinc-50 px-2 py-0.5 text-xs font-medium text-zinc-700 transition hover:border-zinc-300 hover:bg-zinc-100 dark:border-zinc-800 dark:bg-zinc-900 dark:text-zinc-200 dark:hover:border-zinc-700"
                >
                  <span className="max-w-[140px] truncate">{providerName}</span>
                  <ChevronDown className="h-3 w-3 text-zinc-400" />
                </button>
              </div>
              <p
                title={`${sessionState.machineName} · ${sessionState.cwd}`}
                className="mt-0.5 hidden truncate font-mono text-[11px] text-zinc-400 sm:block dark:text-zinc-500"
              >
                {sessionState.machineName} · {sessionState.cwd}
              </p>
            </div>
          </div>

          {/* Right: Status + Inspector Toggle + Close (always shrink-0, never clipped) */}
          <div className="flex shrink-0 items-center gap-2">
            <span
              className={`inline-flex items-center gap-1.5 rounded-full border px-2.5 py-1 text-[11px] font-medium ${statusBadge.pillClass}`}
            >
              <span className={`h-1.5 w-1.5 rounded-full ${statusBadge.dotClass}`} />
              <span className="hidden sm:inline">{statusBadge.label}</span>
            </span>

            <button
              type="button"
              onClick={() => openDrawer('options')}
              className={`inline-flex h-8 shrink-0 items-center gap-1.5 rounded-lg border px-2.5 text-xs font-medium transition ${
                drawerOpen
                  ? 'border-zinc-900 bg-zinc-900 text-white dark:border-zinc-100 dark:bg-zinc-100 dark:text-zinc-950'
                  : 'border-zinc-200/90 bg-white text-zinc-700 hover:border-zinc-300 hover:bg-zinc-50 dark:border-zinc-800 dark:bg-zinc-900 dark:text-zinc-200 dark:hover:bg-zinc-800'
              }`}
            >
              <SlidersHorizontal className="h-3.5 w-3.5" />
              <span className="hidden sm:inline">Menus</span>
            </button>

            {isRunning && !confirmClose && (
              <button
                type="button"
                onClick={() => setConfirmClose(true)}
                className="inline-flex h-8 shrink-0 items-center gap-1.5 rounded-lg border border-zinc-200/90 bg-white px-2.5 text-xs font-medium text-zinc-600 transition hover:border-red-200 hover:bg-red-50 hover:text-red-600 dark:border-zinc-800 dark:bg-zinc-900 dark:text-zinc-300 dark:hover:border-red-900/60 dark:hover:bg-red-950/30 dark:hover:text-red-400"
              >
                <Power className="h-3.5 w-3.5" />
                <span className="hidden sm:inline">Fermer</span>
              </button>
            )}

            {isRunning && confirmClose && (
              <div className="flex shrink-0 items-center gap-1 rounded-lg border border-red-200 bg-red-50/90 px-2 py-1 dark:border-red-900/60 dark:bg-red-950/50">
                <span className="text-[11px] font-medium text-red-700 dark:text-red-300">
                  Fermer ?
                </span>
                <button
                  type="button"
                  disabled={closing}
                  onClick={() => void handleCloseSession()}
                  className="rounded bg-red-600 px-2 py-0.5 text-[11px] font-medium text-white hover:bg-red-700 disabled:opacity-50"
                >
                  {closing ? '…' : 'Oui'}
                </button>
                <button
                  type="button"
                  disabled={closing}
                  onClick={() => setConfirmClose(false)}
                  className="rounded px-1.5 py-0.5 text-[11px] text-zinc-600 hover:bg-zinc-200/70 dark:text-zinc-300 dark:hover:bg-zinc-800"
                >
                  Non
                </button>
              </div>
            )}
          </div>
        </header>

        {/* Error Banner */}
        {error && (
          <div className="mx-auto mt-3 flex w-full max-w-3xl items-center gap-2 rounded-xl border border-red-200 bg-red-50 px-3.5 py-2.5 text-xs text-red-700 dark:border-red-900/60 dark:bg-red-950/40 dark:text-red-300">
            <AlertCircle className="h-3.5 w-3.5 shrink-0" />
            <span>{error}</span>
          </div>
        )}

        {/* Scrollable Message Canvas */}
        <div
          ref={feedRef}
          onScroll={handleFeedScroll}
          className="relative min-h-0 flex-1 overflow-y-auto px-4 py-7 sm:px-8"
        >
          <div className="mx-auto flex w-full max-w-[760px] flex-col gap-7 pb-6">
            {loading ? (
              <div className="flex items-center justify-center gap-2 py-20 text-xs text-zinc-500 dark:text-zinc-400">
                <Loader2 className="h-4 w-4 animate-spin" />
                <span>Chargement de la conversation…</span>
              </div>
            ) : messages.length === 0 && isRunning ? (
              <div className="flex flex-col items-center justify-center py-14 text-center">
                <div className="flex h-12 w-12 items-center justify-center rounded-2xl bg-zinc-900 text-white shadow-sm dark:bg-zinc-100 dark:text-zinc-950">
                  <Sparkles className="h-5 w-5" />
                </div>
                <h2 className="mt-5 text-2xl font-semibold tracking-tight text-zinc-950 dark:text-zinc-50">
                  Comment puis-je vous aider ?
                </h2>
                <div className="mt-2 inline-flex max-w-full items-center gap-1.5 rounded-full border border-zinc-200/80 bg-zinc-50 px-3 py-1 font-mono text-[11px] text-zinc-500 dark:border-zinc-800 dark:bg-zinc-900 dark:text-zinc-400">
                  <FolderOpen className="h-3 w-3 shrink-0" />
                  <span className="truncate">{sessionState.cwd}</span>
                </div>

                <div className="mt-8 grid w-full max-w-2xl gap-2.5 sm:grid-cols-3">
                  {STARTER_SUGGESTIONS.map((item) => {
                    const Icon = item.icon;
                    return (
                      <button
                        key={item.prompt}
                        type="button"
                        onClick={() => void sendPrompt(item.prompt)}
                        className="group flex flex-col items-start rounded-xl border border-zinc-200/90 bg-white p-3.5 text-left shadow-[0_1px_2px_rgba(0,0,0,0.03)] transition hover:border-zinc-300 hover:bg-zinc-50/70 dark:border-zinc-800/90 dark:bg-zinc-900/50 dark:hover:border-zinc-700 dark:hover:bg-zinc-900"
                      >
                        <span className="flex h-7 w-7 items-center justify-center rounded-lg bg-zinc-100 text-zinc-600 transition group-hover:bg-zinc-900 group-hover:text-white dark:bg-zinc-800 dark:text-zinc-300 dark:group-hover:bg-zinc-100 dark:group-hover:text-zinc-950">
                          <Icon className="h-3.5 w-3.5" />
                        </span>
                        <span className="mt-2.5 text-xs font-semibold text-zinc-900 dark:text-zinc-100">
                          {item.title}
                        </span>
                        <span className="mt-0.5 text-xs leading-snug text-zinc-500 dark:text-zinc-400">
                          {item.prompt}
                        </span>
                      </button>
                    );
                  })}
                </div>
              </div>
            ) : messages.length === 0 ? (
              <div className="py-20 text-center text-xs text-zinc-500 dark:text-zinc-400">
                Aucun message dans cette session.
              </div>
            ) : (
              messages.map((msg) => {
                if (msg.role === 'user') {
                  return (
                    <div key={msg.id} className="flex justify-end">
                      <div className="max-w-[82%] rounded-2xl rounded-br-md bg-zinc-100 px-4 py-3 text-[15px] leading-relaxed whitespace-pre-wrap break-words text-zinc-900 dark:bg-zinc-800/90 dark:text-zinc-100">
                        {msg.content}
                      </div>
                    </div>
                  );
                }

                const streaming = isMessageGenerating(msg);
                const isError = msg.status === 'error';
                const isCancelled = msg.status === 'cancelled';

                return (
                  <article key={msg.id} className="group flex w-full items-start gap-3.5">
                    <div className="mt-0.5 flex h-7 w-7 shrink-0 items-center justify-center rounded-lg border border-zinc-200/90 bg-zinc-50 text-zinc-700 dark:border-zinc-800 dark:bg-zinc-900 dark:text-zinc-300">
                      <Bot className="h-4 w-4" />
                    </div>

                    <div className="min-w-0 flex-1">
                      <div className="mb-1.5 flex flex-wrap items-center gap-2">
                        <span className="text-xs font-semibold text-zinc-900 dark:text-zinc-100">
                          {providerName}
                        </span>
                        {streaming && (
                          <span className="inline-flex items-center gap-1.5 text-[11px] font-medium text-indigo-600 dark:text-indigo-400">
                            <span className="h-1.5 w-1.5 animate-pulse rounded-full bg-indigo-500 dark:bg-indigo-400" />
                            <span>Génération…</span>
                          </span>
                        )}
                        {isError && (
                          <span className="inline-flex items-center gap-1 rounded-full bg-red-50 px-2 py-0.5 text-[10px] font-medium text-red-600 dark:bg-red-500/15 dark:text-red-400">
                            <AlertCircle className="h-3 w-3" />
                            Erreur
                          </span>
                        )}
                        {isCancelled && (
                          <span className="rounded-full bg-zinc-100 px-2 py-0.5 text-[10px] font-medium text-zinc-500 dark:bg-zinc-800 dark:text-zinc-400">
                            Interrompu
                          </span>
                        )}
                      </div>

                      {msg.content ? (
                        <MarkdownMessage content={msg.content} />
                      ) : streaming ? (
                        <div className="flex items-center gap-1.5 py-2.5">
                          <span className="h-2 w-2 animate-pulse rounded-full bg-zinc-300 dark:bg-zinc-700" />
                          <span className="h-2 w-2 animate-pulse rounded-full bg-zinc-300 [animation-delay:150ms] dark:bg-zinc-700" />
                          <span className="h-2 w-2 animate-pulse rounded-full bg-zinc-300 [animation-delay:300ms] dark:bg-zinc-700" />
                        </div>
                      ) : null}

                      {!streaming && (msg.content || msg.durationMs > 0 || msg.exitCode != null) && (
                        <div className="mt-3 flex flex-wrap items-center gap-2.5 text-[11px] text-zinc-400 dark:text-zinc-500">
                          {msg.content && (
                            <button
                              type="button"
                              onClick={() => void handleCopyMessage(msg)}
                              className="inline-flex items-center gap-1 rounded-md px-2 py-1 text-zinc-500 transition hover:bg-zinc-100 hover:text-zinc-800 dark:text-zinc-400 dark:hover:bg-zinc-800/80 dark:hover:text-zinc-200"
                            >
                              {copiedId === msg.id ? (
                                <>
                                  <Check className="h-3 w-3 text-emerald-500" />
                                  <span className="text-emerald-600 dark:text-emerald-400">
                                    Copié
                                  </span>
                                </>
                              ) : (
                                <>
                                  <Copy className="h-3 w-3" />
                                  <span>Copier</span>
                                </>
                              )}
                            </button>
                          )}
                          {msg.durationMs > 0 && (
                            <span className="font-mono">{formatDuration(msg.durationMs)}</span>
                          )}
                          {msg.exitCode != null && (
                            <span
                              className={`rounded px-1.5 py-0.5 font-mono text-[10px] ${
                                msg.exitCode === 0
                                  ? 'bg-zinc-100 text-zinc-500 dark:bg-zinc-900 dark:text-zinc-400'
                                  : 'bg-red-50 text-red-600 dark:bg-red-500/15 dark:text-red-400'
                              }`}
                            >
                              exit {msg.exitCode}
                            </span>
                          )}
                        </div>
                      )}
                    </div>
                  </article>
                );
              })
            )}
          </div>
        </div>

        {/* Floating scroll-to-bottom button */}
        {!isAtBottom && messages.length > 0 && (
          <div className="pointer-events-none absolute right-6 bottom-32 z-10 flex justify-end">
            <button
              type="button"
              onClick={() => scrollToBottom('smooth')}
              aria-label="Aller en bas"
              className="pointer-events-auto flex h-9 w-9 items-center justify-center rounded-full border border-zinc-200 bg-white text-zinc-600 shadow-md transition hover:bg-zinc-50 dark:border-zinc-700 dark:bg-zinc-900 dark:text-zinc-200 dark:hover:bg-zinc-800"
            >
              <ArrowDown className="h-4 w-4" />
            </button>
          </div>
        )}

        {/* Docked Bottom Composer or Closed Footer */}
        {isRunning ? (
          <div className="safe-bottom shrink-0 bg-gradient-to-t from-white via-white to-white/80 px-4 pt-2 pb-4 sm:px-8 sm:pb-5 dark:from-[#09090B] dark:via-[#09090B] dark:to-[#09090B]/80">
            <div className="mx-auto w-full max-w-[760px]">
              {/* Active non-default provider option chips */}
              {activeOptionChips.length > 0 && (
                <div className="mb-2 flex flex-wrap items-center gap-1.5">
                  {activeOptionChips.map((chip) => (
                    <button
                      key={chip.id}
                      type="button"
                      onClick={() => openDrawer('options')}
                      className="inline-flex items-center gap-1 rounded-full border border-indigo-200/90 bg-indigo-50/70 px-2.5 py-0.5 text-[11px] font-medium text-indigo-700 transition hover:bg-indigo-100 dark:border-indigo-500/30 dark:bg-indigo-500/15 dark:text-indigo-300 dark:hover:bg-indigo-500/25"
                    >
                      <span>{chip.label}:</span>
                      <span className="font-mono">{chip.valueLabel}</span>
                    </button>
                  ))}
                </div>
              )}

              {/* Inline / slash autocomplete popup */}
              {slashSuggestions.length > 0 && (
                <div
                  role="listbox"
                  aria-label="Suggestions de commandes"
                  className="mb-2 overflow-hidden rounded-xl border border-zinc-200 bg-white shadow-lg dark:border-zinc-800 dark:bg-zinc-900"
                >
                  {slashSuggestions.map((cmd) => (
                    <button
                      key={cmd.name}
                      type="button"
                      role="option"
                      aria-selected={false}
                      onClick={() => handleSelectCommand(cmd.args ? `${cmd.name} ` : cmd.name)}
                      className="flex w-full items-center justify-between gap-3 border-b border-zinc-100 px-3.5 py-2.5 text-left text-xs transition last:border-b-0 hover:bg-zinc-50 dark:border-zinc-800/80 dark:hover:bg-zinc-800/60"
                    >
                      <div className="flex min-w-0 items-center gap-2">
                        <span className="font-mono font-semibold text-indigo-600 dark:text-indigo-400">
                          {cmd.name}
                        </span>
                        {cmd.args && (
                          <span className="truncate font-mono text-[11px] text-zinc-400">
                            {cmd.args}
                          </span>
                        )}
                      </div>
                      <span className="truncate text-[11px] text-zinc-500 dark:text-zinc-400">
                        {cmd.description}
                      </span>
                    </button>
                  ))}
                </div>
              )}

              {/* Elevated Multi-Bar Composer Card */}
              <form
                onSubmit={handleFormSubmit}
                className="overflow-hidden rounded-2xl border border-zinc-200/95 bg-white shadow-[0_2px_12px_-2px_rgba(0,0,0,0.06)] transition focus-within:border-zinc-300 focus-within:shadow-[0_6px_24px_-6px_rgba(0,0,0,0.1)] dark:border-zinc-800 dark:bg-zinc-900/90 dark:shadow-none dark:focus-within:border-zinc-700"
              >
                <textarea
                  ref={textareaRef}
                  rows={1}
                  value={input}
                  onChange={(e) => {
                    setInput(e.target.value);
                    setSlashDismissed(false);
                  }}
                  onKeyDown={handleTextareaKeyDown}
                  placeholder={`Message à ${providerName}…`}
                  className="block max-h-44 min-h-[48px] w-full resize-none bg-transparent px-4 pt-3.5 pb-2 text-[15px] leading-relaxed text-zinc-900 outline-none placeholder:text-zinc-400 dark:text-zinc-100 dark:placeholder:text-zinc-500"
                />

                <div className="flex items-center justify-between gap-2 px-3 pb-2.5 pt-1">
                  <div className="flex items-center gap-1.5">
                    <button
                      type="button"
                      onClick={() => openDrawer('commands')}
                      aria-label="Commandes"
                      title="Commandes fournisseur"
                      className="inline-flex h-7 items-center gap-1 rounded-lg border border-zinc-200/90 bg-zinc-50 px-2 font-mono text-[11px] font-semibold text-zinc-600 transition hover:border-zinc-300 hover:bg-zinc-100 hover:text-zinc-900 dark:border-zinc-800 dark:bg-zinc-950 dark:text-zinc-300 dark:hover:border-zinc-700"
                    >
                      <span>/</span>
                      <span className="hidden font-sans font-medium sm:inline">Commandes</span>
                    </button>

                    <button
                      type="button"
                      onClick={() => openDrawer('inspect')}
                      className="hidden h-7 items-center gap-1 rounded-lg px-2 text-[11px] font-medium text-zinc-500 transition hover:bg-zinc-100 hover:text-zinc-800 sm:inline-flex dark:text-zinc-400 dark:hover:bg-zinc-800 dark:hover:text-zinc-200"
                    >
                      <SlidersHorizontal className="h-3 w-3" />
                      <span>Outils CLI</span>
                    </button>
                  </div>

                  <div className="flex items-center gap-2.5">
                    <span className="hidden text-[11px] text-zinc-400 md:inline dark:text-zinc-500">
                      <kbd className="font-mono">Entrée</kbd> envoyer ·{' '}
                      <kbd className="font-mono">Maj+Entrée</kbd> ligne
                    </span>

                    {isGenerating ? (
                      <button
                        type="button"
                        onClick={() => void handleCancelTurn()}
                        disabled={cancelling}
                        aria-label="Arrêter"
                        title="Arrêter la génération"
                        className="flex h-8 w-8 shrink-0 items-center justify-center rounded-full bg-zinc-900 text-white transition hover:bg-zinc-700 disabled:opacity-50 dark:bg-zinc-100 dark:text-zinc-950 dark:hover:bg-zinc-300"
                      >
                        {cancelling ? (
                          <Loader2 className="h-3.5 w-3.5 animate-spin" />
                        ) : (
                          <Square className="h-3.5 w-3.5 fill-current" />
                        )}
                      </button>
                    ) : (
                      <button
                        type="submit"
                        disabled={sending || !input.trim()}
                        aria-label="Envoyer"
                        title="Envoyer"
                        className="flex h-8 w-8 shrink-0 items-center justify-center rounded-full bg-zinc-900 text-white transition hover:bg-zinc-700 disabled:bg-zinc-200 disabled:text-zinc-400 dark:bg-zinc-100 dark:text-zinc-950 dark:hover:bg-zinc-300 dark:disabled:bg-zinc-800 dark:disabled:text-zinc-600"
                      >
                        {sending ? (
                          <Loader2 className="h-3.5 w-3.5 animate-spin" />
                        ) : (
                          <ArrowUp className="h-4 w-4" />
                        )}
                      </button>
                    )}
                  </div>
                </div>
              </form>
            </div>
          </div>
        ) : (
          <div className="safe-bottom shrink-0 border-t border-zinc-200/80 bg-zinc-50/70 px-4 py-3.5 sm:px-8 dark:border-zinc-800/80 dark:bg-zinc-900/40">
            <div className="mx-auto flex w-full max-w-[760px] flex-wrap items-center justify-between gap-3">
              <div>
                <p className="text-xs font-semibold text-zinc-800 dark:text-zinc-200">
                  Session terminée
                </p>
                <p className="text-[11px] text-zinc-500 dark:text-zinc-400">
                  L&apos;historique reste consultable. Relancez une session pour continuer.
                </p>
              </div>
              <button
                type="button"
                disabled={relaunching}
                onClick={() => void handleRelaunch()}
                className="inline-flex h-8 items-center gap-1.5 rounded-lg bg-zinc-900 px-3.5 text-xs font-medium text-white transition hover:bg-zinc-700 disabled:opacity-50 dark:bg-zinc-100 dark:text-zinc-950 dark:hover:bg-zinc-300"
              >
                {relaunching ? (
                  <Loader2 className="h-3.5 w-3.5 animate-spin" />
                ) : (
                  <RotateCcw className="h-3.5 w-3.5" />
                )}
                <span>Relancer</span>
              </button>
            </div>
          </div>
        )}
      </div>

      {/* Right-hand Provider Inspector (docked on xl:, drawer on mobile/tablet) */}
      <ProviderMenuDrawer
        open={drawerOpen}
        initialTab={drawerTab}
        manifest={manifest}
        session={sessionState}
        onClose={() => setDrawerOpen(false)}
        onUpdateOptions={handleUpdateOptions}
        onSelectCommand={handleSelectCommand}
        onRunInspectAction={handleRunInspectAction}
      />
    </div>
  );
}
