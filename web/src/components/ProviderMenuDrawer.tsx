import { useEffect, useMemo, useRef, useState } from 'react';
import {
  Check,
  Copy,
  Loader2,
  Play,
  RefreshCw,
  SlidersHorizontal,
  Bug,
  Wrench,
  X,
} from 'lucide-react';
import type {
  AgentManifest,
  MenuActionResult,
  ModelsRefreshResult,
  OptionChoice,
  SessionCommandInfo,
  SessionMeta,
} from '../api/types';

export type ProviderMenuTab = 'options' | 'debug' | 'inspect';

export interface ProviderMenuDrawerProps {
  open: boolean;
  initialTab?: ProviderMenuTab;
  manifest?: AgentManifest;
  session: SessionMeta;
  onClose: () => void;
  onUpdateOptions: (nextOptions: Record<string, string>) => Promise<void>;
  onLoadSessionCommand: () => Promise<SessionCommandInfo>;
  onRunInspectAction: (actionId: string) => Promise<MenuActionResult>;
  onRefreshModels?: (agentId: string, machine?: string) => Promise<ModelsRefreshResult>;
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
  onLoadSessionCommand,
  onRunInspectAction,
  onRefreshModels,
}: ProviderMenuDrawerProps) {
  const [activeTab, setActiveTab] = useState<ProviderMenuTab>(initialTab);
  const [savingOptionId, setSavingOptionId] = useState<string | null>(null);
  const [optionError, setOptionError] = useState<string | null>(null);
  const [commandInfo, setCommandInfo] = useState<SessionCommandInfo | null>(null);
  const [commandLoading, setCommandLoading] = useState(false);
  const [commandError, setCommandError] = useState<string | null>(null);
  const [copiedCommand, setCopiedCommand] = useState(false);
  const [runningActionId, setRunningActionId] = useState<string | null>(null);
  const [inspectResult, setInspectResult] = useState<MenuActionResult | null>(null);
  const [inspectError, setInspectError] = useState<string | null>(null);
  const [copiedInspect, setCopiedInspect] = useState(false);
  const [syncingModels, setSyncingModels] = useState(false);
  const [syncError, setSyncError] = useState<string | null>(null);
  const [syncedChoices, setSyncedChoices] = useState<Record<string, OptionChoice[]>>({});

  // Keep the loader callable without retriggering the fetch effect on re-renders.
  const loadCommandRef = useRef(onLoadSessionCommand);
  loadCommandRef.current = onLoadSessionCommand;
  const commandFetchedKeyRef = useRef<string | null>(null);
  const sessionOptionsKey = JSON.stringify(session.options ?? {});

  useEffect(() => {
    if (open) {
      setActiveTab(initialTab);
      setOptionError(null);
      setInspectError(null);
    }
  }, [open, initialTab]);

  useEffect(() => {
    if (!open || activeTab !== 'debug') return;
    const fetchKey = `${session.id}:${sessionOptionsKey}`;
    if (commandFetchedKeyRef.current === fetchKey) return;
    commandFetchedKeyRef.current = fetchKey;
    let cancelled = false;
    setCommandLoading(true);
    setCommandError(null);
    loadCommandRef.current()
      .then((info) => {
        if (!cancelled) setCommandInfo(info);
      })
      .catch((err) => {
        if (!cancelled) {
          setCommandError(
            err instanceof Error ? err.message : 'Impossible de charger la commande',
          );
        }
      })
      .finally(() => {
        if (!cancelled) setCommandLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, [open, activeTab, session.id, sessionOptionsKey]);

  useEffect(() => {
    if (!open) return;
    function onKeyDown(e: KeyboardEvent) {
      if (e.key === 'Escape') onClose();
    }
    window.addEventListener('keydown', onKeyDown);
    return () => window.removeEventListener('keydown', onKeyDown);
  }, [open, onClose]);

  const options = manifest?.providerMenus?.options ?? [];
  const inspectActions = manifest?.providerMenus?.inspectActions ?? [];
  const modelSyncOptionId = manifest?.modelSync?.optionId;

  const activeOptionChips = useMemo(() => {
    if (!commandInfo) return [];
    return options
      .map((opt) => {
        const val = commandInfo.options?.[opt.id];
        if (!val || val === 'default' || (opt.defaultValue && val === opt.defaultValue)) {
          return null;
        }
        const choice = opt.choices.find((c) => c.value === val);
        return { flag: opt.flag, valueLabel: choice?.label ?? val };
      })
      .filter((x): x is { flag: string; valueLabel: string } => x !== null);
  }, [commandInfo, options]);

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

  async function handleSyncModels() {
    if (!manifest || !onRefreshModels) return;
    setSyncingModels(true);
    setSyncError(null);
    try {
      const res = await onRefreshModels(manifest.agentId, session.machineName);
      setSyncedChoices((cur) => ({ ...cur, [res.optionId]: res.models }));
    } catch (err) {
      setSyncError(
        err instanceof Error ? err.message : 'Impossible de synchroniser les modèles du CLI',
      );
    } finally {
      setSyncingModels(false);
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

  const tabs: { id: ProviderMenuTab; label: string; icon: typeof Bug }[] = [
    { id: 'options', label: 'Paramètres', icon: SlidersHorizontal },
    { id: 'debug', label: 'Debug', icon: Bug },
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

              {syncError && (
                <div className="rounded-lg border border-red-200 bg-red-50 px-3 py-2 text-xs text-red-700 dark:border-red-900/60 dark:bg-red-950/40 dark:text-red-300">
                  {syncError}
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
                  const choices = syncedChoices[opt.id] ?? opt.choices;
                  const canSyncModels = Boolean(
                    onRefreshModels && modelSyncOptionId === opt.id,
                  );
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
                        <div className="flex shrink-0 items-center gap-1.5">
                          {canSyncModels && (
                            <button
                              type="button"
                              onClick={() => void handleSyncModels()}
                              disabled={syncingModels}
                              aria-label={`Synchroniser les modèles depuis ${manifest?.name ?? 'le CLI'}`}
                              title="Synchroniser les modèles depuis le CLI installé sur la machine"
                              className="flex h-6 w-6 items-center justify-center rounded-md text-zinc-400 transition hover:bg-zinc-200/60 hover:text-zinc-700 disabled:opacity-50 dark:hover:bg-zinc-800 dark:hover:text-zinc-200"
                            >
                              <RefreshCw
                                className={`h-3.5 w-3.5 ${syncingModels ? 'animate-spin' : ''}`}
                              />
                            </button>
                          )}
                          <span className="rounded bg-zinc-100 px-1.5 py-0.5 font-mono text-[10px] text-zinc-500 dark:bg-zinc-800 dark:text-zinc-400">
                            {opt.flag}
                          </span>
                        </div>
                      </div>

                      <div className="mt-3 flex flex-wrap gap-1.5">
                        {choices.map((choice) => {
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

          {activeTab === 'debug' && (
            <div className="space-y-3">
              {commandLoading && (
                <div className="flex items-center gap-2 py-6 text-xs text-zinc-500 dark:text-zinc-400">
                  <Loader2 className="h-3.5 w-3.5 animate-spin" />
                  <span>Chargement de la commande…</span>
                </div>
              )}

              {commandError && (
                <div className="rounded-lg border border-red-200 bg-red-50 px-3 py-2 text-xs text-red-700 dark:border-red-900/60 dark:bg-red-950/40 dark:text-red-300">
                  {commandError}
                </div>
              )}

              {commandInfo && (
                <>
                  <p className="text-[11px] leading-snug text-zinc-500 dark:text-zinc-400">
                    Commande envoyée au runner en mode headless.{' '}
                    <code className="font-mono">{'{prompt}'}</code> est remplacé par votre
                    message au moment de l&apos;envoi.
                  </p>

                  <div className="overflow-hidden rounded-xl border border-zinc-800 bg-[#111113] text-zinc-100">
                    <div className="flex items-center justify-between border-b border-zinc-800 bg-[#18181B] px-3 py-2">
                      <span className="text-xs font-medium text-zinc-200">Ligne de commande</span>
                      <button
                        type="button"
                        onClick={() =>
                          void navigator.clipboard
                            ?.writeText(
                              [
                                commandInfo.launchCommand,
                                ...(commandInfo.nextTurnArgs ?? []),
                              ].join(' '),
                            )
                            .then(() => {
                              setCopiedCommand(true);
                              window.setTimeout(() => setCopiedCommand(false), 1800);
                            })
                        }
                        className="inline-flex items-center gap-1 rounded-md px-2 py-0.5 text-[11px] font-medium text-zinc-300 transition hover:bg-zinc-800 hover:text-white"
                      >
                        {copiedCommand ? (
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
                    <div className="space-y-2 p-3 font-mono text-[11px] leading-relaxed">
                      <div className="break-words">
                        <span className="text-emerald-400">
                          {commandInfo.launchCommand}
                        </span>{' '}
                        {(commandInfo.nextTurnArgs ?? []).map((arg, idx) => (
                          <span key={`${arg}-${idx}`}>
                            {arg === '{prompt}' ? (
                              <span className="rounded bg-amber-500/20 px-1 text-amber-300">
                                {'{prompt}'}
                              </span>
                            ) : (
                              <span className="text-zinc-100">{arg}</span>
                            )}{' '}
                          </span>
                        ))}
                      </div>
                      {commandInfo.chatMode === 'headless' &&
                        (commandInfo.nextTurnArgs?.length ?? 0) >
                          (commandInfo.firstTurnArgs?.length ?? 0) && (
                          <div className="border-t border-zinc-800 pt-2 text-zinc-400">
                            <span className="text-[10px] uppercase tracking-wider">
                              Premier tour (sans reprise)
                            </span>
                            <div className="mt-1 break-words text-zinc-200">
                              <span className="text-emerald-400">
                                {commandInfo.launchCommand}
                              </span>{' '}
                              {commandInfo.firstTurnArgs?.map((arg, idx) => (
                                <span key={`${arg}-${idx}`}>
                                  {arg === '{prompt}' ? (
                                    <span className="rounded bg-amber-500/20 px-1 text-amber-300">
                                      {'{prompt}'}
                                    </span>
                                  ) : (
                                    <span>{arg}</span>
                                  )}{' '}
                                </span>
                              ))}
                            </div>
                          </div>
                        )}
                    </div>
                  </div>

                  <div className="space-y-1.5 rounded-xl border border-zinc-200/90 bg-white p-3 text-xs dark:border-zinc-800/90 dark:bg-zinc-900/60">
                    <div className="flex items-center justify-between gap-3">
                      <span className="text-zinc-500 dark:text-zinc-400">Mode</span>
                      <span className="rounded bg-zinc-100 px-1.5 py-0.5 font-mono text-[10px] text-zinc-600 dark:bg-zinc-800 dark:text-zinc-300">
                        {commandInfo.chatMode || 'headless'}
                      </span>
                    </div>
                    <div className="flex items-center justify-between gap-3">
                      <span className="text-zinc-500 dark:text-zinc-400">Machine</span>
                      <span className="font-medium text-zinc-900 dark:text-zinc-100">
                        {commandInfo.machineName}
                      </span>
                    </div>
                    <div className="flex items-center justify-between gap-3">
                      <span className="shrink-0 text-zinc-500 dark:text-zinc-400">
                        Répertoire
                      </span>
                      <span
                        className="truncate font-mono text-[11px] text-zinc-800 dark:text-zinc-200"
                        title={commandInfo.cwd}
                      >
                        {commandInfo.cwd}
                      </span>
                    </div>
                    {activeOptionChips.length > 0 && (
                      <div className="border-t border-zinc-200/70 pt-2 dark:border-zinc-800/70">
                        <span className="text-[10px] font-medium uppercase tracking-wider text-zinc-400">
                          Options actives
                        </span>
                        <div className="mt-1.5 flex flex-wrap gap-1.5">
                          {activeOptionChips.map((chip) => (
                            <span
                              key={chip.flag}
                              className="inline-flex items-center gap-1 rounded-full border border-indigo-200/90 bg-indigo-50/70 px-2 py-0.5 text-[11px] font-medium text-indigo-700 dark:border-indigo-500/30 dark:bg-indigo-500/15 dark:text-indigo-300"
                            >
                              <span className="font-mono">{chip.flag}</span>
                              <span>{chip.valueLabel}</span>
                            </span>
                          ))}
                        </div>
                      </div>
                    )}
                  </div>
                </>
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
