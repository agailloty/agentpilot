import { useEffect, useMemo, useState } from 'react';
import {
  Check,
  Copy,
  Loader2,
  Play,
  Search,
  SlidersHorizontal,
  Terminal,
  Wrench,
  X,
} from 'lucide-react';
import type { AgentManifest, MenuActionResult, SessionMeta } from '../api/types';

export type ProviderMenuTab = 'options' | 'commands' | 'inspect';

export interface ProviderMenuDrawerProps {
  open: boolean;
  initialTab?: ProviderMenuTab;
  manifest?: AgentManifest;
  session: SessionMeta;
  onClose: () => void;
  onUpdateOptions: (nextOptions: Record<string, string>) => Promise<void>;
  onSelectCommand: (commandText: string) => void;
  onRunInspectAction: (actionId: string) => Promise<MenuActionResult>;
}

function formatDuration(ms: number): string {
  if (ms < 1000) return `${ms} ms`;
  return `${(ms / 1000).toFixed(1)} s`;
}

export function ProviderMenuDrawer({
  open,
  initialTab = 'options',
  manifest,
  session,
  onClose,
  onUpdateOptions,
  onSelectCommand,
  onRunInspectAction,
}: ProviderMenuDrawerProps) {
  const [activeTab, setActiveTab] = useState<ProviderMenuTab>(initialTab);
  const [commandSearch, setCommandSearch] = useState('');
  const [savingOptionId, setSavingOptionId] = useState<string | null>(null);
  const [optionError, setOptionError] = useState<string | null>(null);
  const [runningActionId, setRunningActionId] = useState<string | null>(null);
  const [inspectResult, setInspectResult] = useState<MenuActionResult | null>(null);
  const [inspectError, setInspectError] = useState<string | null>(null);
  const [copiedInspect, setCopiedInspect] = useState(false);

  useEffect(() => {
    if (open) {
      setActiveTab(initialTab);
      setOptionError(null);
      setInspectError(null);
    }
  }, [open, initialTab]);

  useEffect(() => {
    if (!open) return;
    function onKeyDown(e: KeyboardEvent) {
      if (e.key === 'Escape') onClose();
    }
    window.addEventListener('keydown', onKeyDown);
    return () => window.removeEventListener('keydown', onKeyDown);
  }, [open, onClose]);

  const options = manifest?.providerMenus?.options ?? [];
  const commands = manifest?.commands ?? [];
  const inspectActions = manifest?.providerMenus?.inspectActions ?? [];

  const filteredCommands = useMemo(() => {
    const q = commandSearch.trim().toLowerCase();
    if (!q) return commands;
    return commands.filter(
      (cmd) =>
        cmd.name.toLowerCase().includes(q) ||
        cmd.description.toLowerCase().includes(q) ||
        (cmd.args ?? '').toLowerCase().includes(q),
    );
  }, [commands, commandSearch]);

  if (!open) return null;

  async function handleSelectChoice(optionId: string, value: string) {
    setSavingOptionId(optionId);
    setOptionError(null);
    try {
      await onUpdateOptions({
        ...(session.options ?? {}),
        [optionId]: value,
      });
    } catch (err) {
      setOptionError(
        err instanceof Error ? err.message : 'Impossible de mettre à jour le paramètre',
      );
    } finally {
      setSavingOptionId(null);
    }
  }

  async function handleRunInspect(actionId: string) {
    setRunningActionId(actionId);
    setInspectError(null);
    try {
      const res = await onRunInspectAction(actionId);
      setInspectResult(res);
    } catch (err) {
      setInspectError(
        err instanceof Error ? err.message : 'Impossible d’exécuter cette action CLI',
      );
    } finally {
      setRunningActionId(null);
    }
  }

  async function handleCopyInspect() {
    if (!inspectResult) return;
    try {
      await navigator.clipboard?.writeText(inspectResult.output);
      setCopiedInspect(true);
      setTimeout(() => setCopiedInspect(false), 1800);
    } catch {
      // Ignore clipboard failures.
    }
  }

  const tabs: { id: ProviderMenuTab; label: string; icon: typeof SlidersHorizontal }[] = [
    { id: 'options', label: 'Paramètres', icon: SlidersHorizontal },
    { id: 'commands', label: 'Commandes', icon: Terminal },
    { id: 'inspect', label: 'Menus CLI', icon: Wrench },
  ];

  return (
    <div
      className="fixed inset-0 z-50 flex items-end justify-center bg-black/40 backdrop-blur-[2px] md:items-stretch md:justify-end xl:static xl:z-auto xl:inset-auto xl:block xl:h-full xl:w-[360px] xl:shrink-0 xl:bg-transparent xl:backdrop-blur-none"
      role="dialog"
      aria-modal="true"
      aria-label="Menus du fournisseur"
      onClick={(e) => {
        if (e.target === e.currentTarget) onClose();
      }}
    >
      <div className="flex max-h-[85vh] w-full flex-col rounded-t-2xl border-t border-zinc-200 bg-white shadow-2xl md:h-full md:max-h-none md:w-[380px] md:rounded-none md:border-t-0 md:border-l xl:w-full xl:border-zinc-200/80 xl:bg-[#FAFAFB] xl:shadow-none dark:border-zinc-800 dark:bg-zinc-950 xl:dark:border-zinc-800/80 xl:dark:bg-[#0D0D10]">
        {/* Header */}
        <div className="flex h-14 shrink-0 items-center justify-between border-b border-zinc-200/80 px-4 dark:border-zinc-800/80">
          <div className="min-w-0">
            <h2 className="truncate text-[13px] font-semibold text-zinc-900 dark:text-zinc-100">
              {manifest?.name ?? session.agentId}
            </h2>
            <p className="truncate font-mono text-[10px] text-zinc-500 dark:text-zinc-400">
              Inspecteur · {session.machineName}
            </p>
          </div>
          <button
            type="button"
            onClick={onClose}
            aria-label="Fermer le panneau"
            className="flex h-7 w-7 items-center justify-center rounded-lg text-zinc-400 transition hover:bg-zinc-200/60 hover:text-zinc-700 dark:hover:bg-zinc-800 dark:hover:text-zinc-200"
          >
            <X className="h-4 w-4" />
          </button>
        </div>

        {/* Segmented tabs */}
        <div className="shrink-0 border-b border-zinc-200/80 px-4 py-2.5 dark:border-zinc-800/80">
          <div
            role="tablist"
            className="grid grid-cols-3 gap-1 rounded-lg bg-zinc-200/60 p-0.5 dark:bg-zinc-900"
          >
            {tabs.map((tab) => {
              const Icon = tab.icon;
              const active = activeTab === tab.id;
              return (
                <button
                  key={tab.id}
                  type="button"
                  role="tab"
                  aria-selected={active}
                  onClick={() => setActiveTab(tab.id)}
                  className={`inline-flex items-center justify-center gap-1.5 rounded-md px-2 py-1.5 text-[11px] font-medium transition ${
                    active
                      ? 'bg-white text-zinc-900 shadow-xs dark:bg-zinc-800 dark:text-zinc-100'
                      : 'text-zinc-500 hover:text-zinc-800 dark:text-zinc-400 dark:hover:text-zinc-200'
                  }`}
                >
                  <Icon className="h-3.5 w-3.5 shrink-0" />
                  <span className="truncate">{tab.label}</span>
                </button>
              );
            })}
          </div>
        </div>

        {/* Body */}
        <div className="min-h-0 flex-1 overflow-y-auto p-4">
          {activeTab === 'options' && (
            <div className="space-y-3.5">
              {optionError && (
                <div className="rounded-lg border border-red-200 bg-red-50 px-3 py-2 text-xs text-red-700 dark:border-red-900/60 dark:bg-red-950/40 dark:text-red-300">
                  {optionError}
                </div>
              )}

              {options.length === 0 ? (
                <div className="rounded-xl border border-dashed border-zinc-200 p-6 text-center text-xs text-zinc-500 dark:border-zinc-800 dark:text-zinc-400">
                  Aucun paramètre fournisseur configurable pour cet agent.
                </div>
              ) : (
                options.map((opt) => {
                  const activeValue = session.options?.[opt.id] ?? opt.defaultValue ?? '';
                  const isSaving = savingOptionId === opt.id;
                  return (
                    <div
                      key={opt.id}
                      className="rounded-xl border border-zinc-200/90 bg-white p-3.5 shadow-[0_1px_2px_rgba(0,0,0,0.02)] dark:border-zinc-800/90 dark:bg-zinc-900/60"
                    >
                      <div className="flex items-start justify-between gap-2">
                        <div>
                          <div className="text-xs font-semibold text-zinc-900 dark:text-zinc-100">
                            {opt.label}
                          </div>
                          {opt.description && (
                            <p className="mt-0.5 text-[11px] leading-snug text-zinc-500 dark:text-zinc-400">
                              {opt.description}
                            </p>
                          )}
                        </div>
                        <span className="shrink-0 rounded bg-zinc-100 px-1.5 py-0.5 font-mono text-[10px] text-zinc-500 dark:bg-zinc-800 dark:text-zinc-400">
                          {opt.flag}
                        </span>
                      </div>

                      <div className="mt-3 flex flex-wrap gap-1.5">
                        {opt.choices.map((choice) => {
                          const selected = activeValue === choice.value;
                          return (
                            <button
                              key={choice.value}
                              type="button"
                              disabled={isSaving}
                              onClick={() => void handleSelectChoice(opt.id, choice.value)}
                              className={`inline-flex items-center gap-1.5 rounded-lg border px-2.5 py-1.5 text-xs font-medium transition disabled:opacity-50 ${
                                selected
                                  ? 'border-zinc-900 bg-zinc-900 text-white dark:border-zinc-100 dark:bg-zinc-100 dark:text-zinc-950'
                                  : 'border-zinc-200 bg-zinc-50/70 text-zinc-700 hover:border-zinc-300 hover:bg-white dark:border-zinc-800 dark:bg-zinc-950 dark:text-zinc-300 dark:hover:border-zinc-700'
                              }`}
                            >
                              {isSaving && selected && (
                                <Loader2 className="h-3 w-3 animate-spin" />
                              )}
                              <span>{choice.label}</span>
                            </button>
                          );
                        })}
                      </div>
                    </div>
                  );
                })
              )}
            </div>
          )}

          {activeTab === 'commands' && (
            <div className="space-y-3">
              <div className="relative">
                <Search className="pointer-events-none absolute top-1/2 left-3 h-3.5 w-3.5 -translate-y-1/2 text-zinc-400" />
                <input
                  type="text"
                  value={commandSearch}
                  onChange={(e) => setCommandSearch(e.target.value)}
                  placeholder="Rechercher une commande…"
                  className="w-full rounded-lg border border-zinc-200 bg-white py-2 pr-3 pl-8 text-xs text-zinc-900 outline-none focus:border-zinc-400 dark:border-zinc-800 dark:bg-zinc-900 dark:text-zinc-100"
                />
              </div>

              {filteredCommands.length === 0 ? (
                <div className="rounded-xl border border-dashed border-zinc-200 p-6 text-center text-xs text-zinc-500 dark:border-zinc-800 dark:text-zinc-400">
                  Aucune commande correspondante.
                </div>
              ) : (
                <div className="space-y-1.5">
                  {filteredCommands.map((cmd) => (
                    <button
                      key={cmd.name}
                      type="button"
                      onClick={() => {
                        const text = cmd.args ? `${cmd.name} ` : cmd.name;
                        onSelectCommand(text);
                        if (window.innerWidth < 1280) {
                          onClose();
                        }
                      }}
                      className="flex w-full flex-col items-start gap-0.5 rounded-xl border border-zinc-200/90 bg-white p-3 text-left transition hover:border-zinc-300 hover:bg-zinc-50/70 dark:border-zinc-800/90 dark:bg-zinc-900/60 dark:hover:border-zinc-700 dark:hover:bg-zinc-800/60"
                    >
                      <div className="flex w-full items-center gap-2">
                        <span className="font-mono text-xs font-semibold text-indigo-600 dark:text-indigo-400">
                          {cmd.name}
                        </span>
                        {cmd.args && (
                          <span className="truncate font-mono text-[11px] text-zinc-400 dark:text-zinc-500">
                            {cmd.args}
                          </span>
                        )}
                      </div>
                      <span className="text-xs text-zinc-600 dark:text-zinc-400">
                        {cmd.description}
                      </span>
                    </button>
                  ))}
                </div>
              )}
            </div>
          )}

          {activeTab === 'inspect' && (
            <div className="space-y-4">
              {inspectActions.length === 0 ? (
                <div className="rounded-xl border border-dashed border-zinc-200 p-6 text-center text-xs text-zinc-500 dark:border-zinc-800 dark:text-zinc-400">
                  Aucune action CLI d’inspection déclarée pour cet agent.
                </div>
              ) : (
                <div className="space-y-2">
                  {inspectActions.map((action) => {
                    const running = runningActionId === action.id;
                    return (
                      <button
                        key={action.id}
                        type="button"
                        disabled={runningActionId !== null}
                        onClick={() => void handleRunInspect(action.id)}
                        className="flex w-full items-center justify-between gap-3 rounded-xl border border-zinc-200/90 bg-white p-3 text-left transition hover:border-zinc-300 hover:bg-zinc-50/70 disabled:opacity-50 dark:border-zinc-800/90 dark:bg-zinc-900/60 dark:hover:border-zinc-700"
                      >
                        <div className="min-w-0">
                          <div className="text-xs font-semibold text-zinc-900 dark:text-zinc-100">
                            {action.label}
                          </div>
                          {action.description && (
                            <p className="mt-0.5 text-[11px] text-zinc-500 dark:text-zinc-400">
                              {action.description}
                            </p>
                          )}
                          <div className="mt-1 truncate font-mono text-[10px] text-zinc-400">
                            {action.args.join(' ')}
                          </div>
                        </div>
                        <span className="flex h-7 w-7 shrink-0 items-center justify-center rounded-lg bg-zinc-100 text-zinc-700 dark:bg-zinc-800 dark:text-zinc-200">
                          {running ? (
                            <Loader2 className="h-3.5 w-3.5 animate-spin" />
                          ) : (
                            <Play className="h-3.5 w-3.5" />
                          )}
                        </span>
                      </button>
                    );
                  })}
                </div>
              )}

              {inspectError && (
                <div className="rounded-lg border border-red-200 bg-red-50 px-3 py-2 text-xs text-red-700 dark:border-red-900/60 dark:bg-red-950/40 dark:text-red-300">
                  {inspectError}
                </div>
              )}

              {inspectResult && (
                <div className="overflow-hidden rounded-xl border border-zinc-800 bg-[#111113] text-zinc-100">
                  <div className="flex flex-wrap items-center justify-between gap-2 border-b border-zinc-800 bg-[#18181B] px-3 py-2">
                    <div className="flex items-center gap-2">
                      <span className="text-xs font-medium text-zinc-200">
                        {inspectResult.label}
                      </span>
                      <span
                        className={`rounded px-1.5 py-0.5 font-mono text-[10px] font-medium ${
                          inspectResult.exitCode === 0
                            ? 'bg-emerald-500/15 text-emerald-400'
                            : 'bg-red-500/15 text-red-400'
                        }`}
                      >
                        exit {inspectResult.exitCode}
                      </span>
                      <span className="font-mono text-[10px] text-zinc-400">
                        {formatDuration(inspectResult.durationMs)}
                      </span>
                    </div>
                    <button
                      type="button"
                      onClick={() => void handleCopyInspect()}
                      className="inline-flex items-center gap-1 rounded-md px-2 py-0.5 text-[11px] font-medium text-zinc-300 transition hover:bg-zinc-800 hover:text-white"
                    >
                      {copiedInspect ? (
                        <>
                          <Check className="h-3 w-3 text-emerald-400" />
                          <span className="text-emerald-400">Copié</span>
                        </>
                      ) : (
                        <>
                          <Copy className="h-3 w-3" />
                          <span>Copier</span>
                        </>
                      )}
                    </button>
                  </div>
                  <pre className="max-h-64 overflow-auto p-3 font-mono text-xs leading-relaxed whitespace-pre-wrap break-words text-zinc-100">
                    {inspectResult.output || '(aucune sortie)'}
                  </pre>
                </div>
              )}
            </div>
          )}
        </div>
      </div>
    </div>
  );
}
