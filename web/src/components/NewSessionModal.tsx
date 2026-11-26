import { useEffect, useMemo, useState, type FormEvent } from 'react';
import { CheckCircle2, Loader2, X, XCircle } from 'lucide-react';
import { useApp } from '../context/AppContext';
import type { AgentManifest, SessionMeta } from '../api/types';

interface NewSessionModalProps {
  open: boolean;
  defaultProjectId?: string;
  onClose: () => void;
  onCreated: (session: SessionMeta) => void;
}

function getAgentInstallState(agent: AgentManifest, machineName?: string) {
  if (machineName && agent.installations?.[machineName]) {
    const inst = agent.installations[machineName];
    return {
      installed: inst.installed,
      online: inst.online,
      path: inst.path ?? '',
    };
  }
  return {
    installed: agent.installed,
    online: true,
    path: agent.executablePath ?? '',
  };
}

export function NewSessionModal({
  open,
  defaultProjectId,
  onClose,
  onCreated,
}: NewSessionModalProps) {
  const { client, projects, agents, machines, refreshSessions } = useApp();
  const [projectId, setProjectId] = useState('');
  const [agentId, setAgentId] = useState('');
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const selectedProject = useMemo(
    () => projects.find((p) => p.id === projectId),
    [projects, projectId],
  );

  const targetMachine = useMemo(
    () => machines.find((m) => m.name === selectedProject?.machineName),
    [machines, selectedProject?.machineName],
  );

  useEffect(() => {
    if (!open) return;
    const initialProj =
      (defaultProjectId && projects.find((p) => p.id === defaultProjectId)) || projects[0];
    setProjectId(initialProj?.id ?? '');
    setError(null);
  }, [open, defaultProjectId, projects]);

  useEffect(() => {
    if (!open) return;
    const machineName = selectedProject?.machineName;
    const firstInstalled = agents.find((a) => getAgentInstallState(a, machineName).installed);
    setAgentId(firstInstalled?.agentId ?? '');
  }, [open, selectedProject?.machineName, agents]);

  if (!open || !client) return null;

  async function handleSubmit(e: FormEvent) {
    e.preventDefault();
    if (!client) return;
    if (!projectId || !agentId) {
      setError('Sélectionnez un projet et un agent installé.');
      return;
    }
    setSubmitting(true);
    setError(null);
    try {
      const created = await client.createSession({ projectId, agentId });
      await refreshSessions();
      onCreated(created);
      onClose();
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Impossible de démarrer la session');
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <div
      className="fixed inset-0 z-50 flex items-end justify-center bg-black/50 p-0 backdrop-blur-[2px] sm:items-center sm:p-4"
      role="dialog"
      aria-modal="true"
      aria-labelledby="new-session-title"
    >
      <div className="max-h-[90dvh] w-full max-w-lg overflow-y-auto rounded-t-2xl border border-zinc-200 bg-white p-5 shadow-xl sm:rounded-xl dark:border-zinc-800 dark:bg-zinc-900">
        <div className="mb-4 flex items-center justify-between">
          <div>
            <h2
              id="new-session-title"
              className="text-sm font-semibold text-zinc-900 dark:text-zinc-100"
            >
              Nouvelle session
            </h2>
            <p className="text-xs text-zinc-500 dark:text-zinc-400">
              Lance un agent CLI installé sur la machine du projet.
            </p>
          </div>
          <button
            type="button"
            onClick={onClose}
            aria-label="Fermer"
            className="flex h-8 w-8 items-center justify-center rounded-md text-zinc-400 hover:bg-zinc-100 hover:text-zinc-700 dark:hover:bg-zinc-800 dark:hover:text-zinc-200"
          >
            <X className="h-4 w-4" />
          </button>
        </div>

        <form onSubmit={handleSubmit} className="space-y-4">
          <div>
            <label
              htmlFor="session-project"
              className="mb-1 block text-xs font-medium text-zinc-700 dark:text-zinc-300"
            >
              Projet
            </label>
            <select
              id="session-project"
              value={projectId}
              onChange={(e) => setProjectId(e.target.value)}
              className="w-full rounded-lg border border-zinc-200 bg-white px-3 py-2 text-sm text-zinc-900 outline-none focus:border-indigo-500 dark:border-zinc-800 dark:bg-zinc-950 dark:text-zinc-100"
            >
              {projects.length === 0 && <option value="">Aucun projet disponible</option>}
              {projects.map((p) => (
                <option key={p.id} value={p.id}>
                  {p.name} · {p.machineName}
                </option>
              ))}
            </select>
            {selectedProject && (
              <p className="mt-1 truncate font-mono text-[11px] text-zinc-500 dark:text-zinc-400">
                {selectedProject.path}
              </p>
            )}
          </div>

          <div>
            <div className="mb-1.5 flex items-center justify-between">
              <span className="text-xs font-medium text-zinc-700 dark:text-zinc-300">
                Agent CLI
              </span>
              {selectedProject && (
                <span className="font-mono text-[11px] text-zinc-400">
                  {selectedProject.machineName}
                  {targetMachine ? (targetMachine.online ? ' · en ligne' : ' · hors ligne') : ''}
                </span>
              )}
            </div>

            <div className="space-y-2">
              {agents.map((agent) => {
                const state = getAgentInstallState(agent, selectedProject?.machineName);
                const selected = agentId === agent.agentId;
                return (
                  <button
                    key={agent.agentId}
                    type="button"
                    disabled={!state.installed}
                    onClick={() => setAgentId(agent.agentId)}
                    className={`flex w-full items-start justify-between gap-3 rounded-lg border p-3 text-left transition ${
                      !state.installed
                        ? 'cursor-not-allowed border-zinc-200/70 bg-zinc-50/70 opacity-60 dark:border-zinc-800/70 dark:bg-zinc-950/50'
                        : selected
                          ? 'border-indigo-500 bg-indigo-50/60 dark:border-indigo-400 dark:bg-indigo-500/10'
                          : 'border-zinc-200 bg-white hover:border-zinc-300 dark:border-zinc-800 dark:bg-zinc-950 dark:hover:border-zinc-700'
                    }`}
                  >
                    <div className="min-w-0">
                      <div className="flex items-center gap-2">
                        <span className="text-sm font-medium text-zinc-900 dark:text-zinc-100">
                          {agent.name}
                        </span>
                        <span className="rounded bg-zinc-100 px-1.5 py-0.5 font-mono text-[10px] text-zinc-500 dark:bg-zinc-800 dark:text-zinc-400">
                          {agent.chatMode || 'headless'}
                        </span>
                      </div>
                      {state.installed && state.path ? (
                        <div className="mt-1 truncate font-mono text-[11px] text-zinc-500 dark:text-zinc-400">
                          {state.path}
                        </div>
                      ) : (
                        <div className="mt-1 font-mono text-[11px] text-zinc-400 dark:text-zinc-500">
                          {agent.launchCommand}
                        </div>
                      )}
                    </div>

                    <div className="shrink-0">
                      {state.installed ? (
                        <span className="inline-flex items-center gap-1 rounded-full bg-emerald-50 px-2 py-0.5 text-[11px] font-medium text-emerald-700 dark:bg-emerald-500/15 dark:text-emerald-300">
                          <CheckCircle2 className="h-3 w-3" />
                          Installé
                        </span>
                      ) : (
                        <span className="inline-flex items-center gap-1 rounded-full bg-zinc-100 px-2 py-0.5 text-[11px] font-medium text-zinc-500 dark:bg-zinc-800 dark:text-zinc-400">
                          <XCircle className="h-3 w-3" />
                          Non installé
                        </span>
                      )}
                    </div>
                  </button>
                );
              })}
            </div>
          </div>

          {error && (
            <div className="rounded-lg border border-red-200 bg-red-50 px-3 py-2 text-xs text-red-700 dark:border-red-900/60 dark:bg-red-950/40 dark:text-red-300">
              {error}
            </div>
          )}

          <div className="flex justify-end gap-2 pt-1">
            <button
              type="button"
              onClick={onClose}
              className="rounded-lg border border-zinc-200 px-3.5 py-2 text-xs font-medium text-zinc-600 hover:bg-zinc-50 dark:border-zinc-800 dark:text-zinc-300 dark:hover:bg-zinc-800"
            >
              Annuler
            </button>
            <button
              type="submit"
              disabled={submitting || !projectId || !agentId}
              className="inline-flex items-center gap-2 rounded-lg bg-indigo-600 px-4 py-2 text-xs font-medium text-white transition hover:bg-indigo-700 disabled:opacity-50 dark:bg-indigo-500 dark:hover:bg-indigo-400"
            >
              {submitting && <Loader2 className="h-3.5 w-3.5 animate-spin" />}
              <span>Démarrer la session</span>
            </button>
          </div>
        </form>
      </div>
    </div>
  );
}
