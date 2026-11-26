import { useEffect, useMemo, useState, type FormEvent } from 'react';
import { Check, Copy, Loader2, Play, RotateCcw, Terminal } from 'lucide-react';
import { useApp } from '../context/AppContext';
import type { ExecResult } from '../api/types';

interface OneShotViewProps {
  initialProjectId?: string;
}

interface RunHistoryItem {
  id: string;
  projectId: string;
  projectName: string;
  machineName: string;
  command: string;
  timeoutSec: number;
  result: ExecResult;
  ranAt: string;
}

const TIMEOUT_OPTIONS = [30, 60, 180] as const;

export function OneShotView({ initialProjectId }: OneShotViewProps) {
  const { client, machines, projects } = useApp();
  const [machineFilter, setMachineFilter] = useState<string>('all');
  const [projectId, setProjectId] = useState<string>(initialProjectId ?? '');
  const [command, setCommand] = useState('');
  const [timeoutSec, setTimeoutSec] = useState<number>(60);
  const [running, setRunning] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [activeRun, setActiveRun] = useState<RunHistoryItem | null>(null);
  const [history, setHistory] = useState<RunHistoryItem[]>([]);
  const [copied, setCopied] = useState(false);

  const filteredProjects = useMemo(
    () =>
      projects.filter((p) =>
        machineFilter === 'all' ? true : p.machineName === machineFilter,
      ),
    [projects, machineFilter],
  );

  useEffect(() => {
    if (initialProjectId) {
      setProjectId(initialProjectId);
      const p = projects.find((item) => item.id === initialProjectId);
      if (p) setMachineFilter(p.machineName);
    }
  }, [initialProjectId, projects]);

  useEffect(() => {
    if (filteredProjects.length > 0 && !filteredProjects.some((p) => p.id === projectId)) {
      setProjectId(filteredProjects[0].id);
    }
  }, [filteredProjects, projectId]);

  const selectedProject = useMemo(
    () => projects.find((p) => p.id === projectId),
    [projects, projectId],
  );

  async function execute(targetProjectId: string, cmd: string, timeout: number) {
    if (!client) return;
    const cleanCmd = cmd.trim();
    if (!targetProjectId || !cleanCmd) {
      setError('Sélectionnez un projet et saisissez une commande.');
      return;
    }
    const proj = projects.find((p) => p.id === targetProjectId);
    setRunning(true);
    setError(null);
    try {
      const result = await client.execCommand(targetProjectId, cleanCmd, timeout);
      const entry: RunHistoryItem = {
        id: `${Date.now()}-${Math.random().toString(36).slice(2, 7)}`,
        projectId: targetProjectId,
        projectName: proj?.name ?? targetProjectId,
        machineName: proj?.machineName ?? '',
        command: cleanCmd,
        timeoutSec: timeout,
        result,
        ranAt: new Date().toISOString(),
      };
      setActiveRun(entry);
      setHistory((prev) => [entry, ...prev].slice(0, 15));
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Échec d’exécution');
    } finally {
      setRunning(false);
    }
  }

  function handleSubmit(e: FormEvent) {
    e.preventDefault();
    void execute(projectId, command, timeoutSec);
  }

  async function handleCopyOutput() {
    if (!activeRun) return;
    try {
      await navigator.clipboard.writeText(activeRun.result.output);
      setCopied(true);
      window.setTimeout(() => setCopied(false), 1500);
    } catch {
      // Clipboard unavailable.
    }
  }

  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-xl font-semibold tracking-tight text-zinc-950 dark:text-zinc-50">
          Commande ponctuelle (One-Shot)
        </h1>
        <p className="mt-0.5 text-xs text-zinc-500 dark:text-zinc-400">
          Exécutez une commande synchrone dans le dossier d&apos;un projet distant.
        </p>
      </div>

      <form
        onSubmit={handleSubmit}
        className="space-y-4 rounded-xl border border-zinc-200 bg-white p-4 dark:border-zinc-800 dark:bg-zinc-900/70"
      >
        <div className="grid gap-3 sm:grid-cols-2">
          <div>
            <label
              htmlFor="oneshot-machine"
              className="mb-1 block text-xs font-medium text-zinc-700 dark:text-zinc-300"
            >
              Machine
            </label>
            <select
              id="oneshot-machine"
              value={machineFilter}
              onChange={(e) => setMachineFilter(e.target.value)}
              className="w-full rounded-lg border border-zinc-200 bg-white px-3 py-2 text-sm text-zinc-900 outline-none focus:border-indigo-500 dark:border-zinc-800 dark:bg-zinc-950 dark:text-zinc-100"
            >
              <option value="all">Toutes les machines</option>
              {machines.map((m) => (
                <option key={m.name} value={m.name}>
                  {m.name} ({m.online ? 'en ligne' : 'hors ligne'})
                </option>
              ))}
            </select>
          </div>

          <div>
            <label
              htmlFor="oneshot-project"
              className="mb-1 block text-xs font-medium text-zinc-700 dark:text-zinc-300"
            >
              Projet
            </label>
            <select
              id="oneshot-project"
              value={projectId}
              onChange={(e) => setProjectId(e.target.value)}
              className="w-full rounded-lg border border-zinc-200 bg-white px-3 py-2 text-sm text-zinc-900 outline-none focus:border-indigo-500 dark:border-zinc-800 dark:bg-zinc-950 dark:text-zinc-100"
            >
              {filteredProjects.length === 0 && <option value="">Aucun projet</option>}
              {filteredProjects.map((p) => (
                <option key={p.id} value={p.id}>
                  {p.name} ({p.machineName})
                </option>
              ))}
            </select>
          </div>
        </div>

        {selectedProject && (
          <div className="truncate font-mono text-[11px] text-zinc-500 dark:text-zinc-400">
            cwd: {selectedProject.path}
          </div>
        )}

        <div>
          <label
            htmlFor="oneshot-cmd"
            className="mb-1 block text-xs font-medium text-zinc-700 dark:text-zinc-300"
          >
            Commande
          </label>
          <input
            id="oneshot-cmd"
            type="text"
            value={command}
            onChange={(e) => setCommand(e.target.value)}
            placeholder="ex: git status -sb"
            className="w-full rounded-lg border border-zinc-200 bg-white px-3 py-2 font-mono text-sm text-zinc-900 outline-none focus:border-indigo-500 focus:ring-2 focus:ring-indigo-500/20 dark:border-zinc-800 dark:bg-zinc-950 dark:text-zinc-100"
          />
        </div>

        <div className="flex flex-wrap items-center justify-between gap-3">
          <div className="flex items-center gap-1.5">
            <span className="text-xs text-zinc-500 dark:text-zinc-400">Timeout :</span>
            {TIMEOUT_OPTIONS.map((sec) => (
              <button
                key={sec}
                type="button"
                onClick={() => setTimeoutSec(sec)}
                className={`rounded-md border px-2.5 py-1 font-mono text-xs transition ${
                  timeoutSec === sec
                    ? 'border-indigo-500 bg-indigo-50 text-indigo-700 dark:border-indigo-400 dark:bg-indigo-500/15 dark:text-indigo-300'
                    : 'border-zinc-200 bg-white text-zinc-600 hover:bg-zinc-50 dark:border-zinc-800 dark:bg-zinc-950 dark:text-zinc-400'
                }`}
              >
                {sec}s
              </button>
            ))}
          </div>

          <button
            type="submit"
            disabled={running || !projectId || !command.trim()}
            className="inline-flex min-h-[38px] items-center gap-1.5 rounded-lg bg-indigo-600 px-4 py-2 text-xs font-medium text-white transition hover:bg-indigo-700 disabled:opacity-50 dark:bg-indigo-500 dark:hover:bg-indigo-400"
          >
            {running ? (
              <Loader2 className="h-3.5 w-3.5 animate-spin" />
            ) : (
              <Play className="h-3.5 w-3.5" />
            )}
            <span>Exécuter</span>
          </button>
        </div>

        {error && (
          <div className="rounded-lg border border-red-200 bg-red-50 px-3 py-2 text-xs text-red-700 dark:border-red-900/60 dark:bg-red-950/40 dark:text-red-300">
            {error}
          </div>
        )}
      </form>

      {/* Output Console */}
      {activeRun && (
        <section className="overflow-hidden rounded-xl border border-zinc-800 bg-[#0A0A0A] text-zinc-100">
          <div className="flex flex-wrap items-center justify-between gap-2 border-b border-zinc-800 px-4 py-2.5">
            <div className="flex min-w-0 items-center gap-2">
              <Terminal className="h-3.5 w-3.5 shrink-0 text-zinc-400" />
              <span className="truncate font-mono text-xs text-zinc-200">
                $ {activeRun.command}
              </span>
            </div>
            <div className="flex items-center gap-2">
              <span
                className={`rounded-full px-2 py-0.5 font-mono text-[11px] font-medium ${
                  activeRun.result.exitCode === 0
                    ? 'bg-emerald-500/15 text-emerald-300'
                    : 'bg-red-500/15 text-red-300'
                }`}
              >
                Exit {activeRun.result.exitCode} · {activeRun.result.durationMs} ms
              </span>
              <button
                type="button"
                onClick={handleCopyOutput}
                className="inline-flex items-center gap-1 rounded-md border border-zinc-700 px-2 py-1 text-[11px] text-zinc-300 hover:bg-zinc-800"
              >
                {copied ? <Check className="h-3 w-3 text-emerald-400" /> : <Copy className="h-3 w-3" />}
                <span>{copied ? 'Copié' : 'Copier'}</span>
              </button>
            </div>
          </div>
          <pre className="max-h-96 overflow-auto p-4 font-mono text-xs leading-relaxed whitespace-pre-wrap text-zinc-200">
            {activeRun.result.output || '(aucune sortie)'}
          </pre>
        </section>
      )}

      {/* Run History */}
      {history.length > 0 && (
        <section className="space-y-2">
          <h2 className="text-xs font-semibold uppercase tracking-wider text-zinc-500 dark:text-zinc-400">
            Historique récent
          </h2>
          <div className="divide-y divide-zinc-200 overflow-hidden rounded-xl border border-zinc-200 bg-white dark:divide-zinc-800 dark:border-zinc-800 dark:bg-zinc-900/70">
            {history.map((item) => (
              <div
                key={item.id}
                className="flex items-center justify-between gap-3 px-4 py-2.5"
              >
                <button
                  type="button"
                  onClick={() => setActiveRun(item)}
                  className="min-w-0 flex-1 text-left"
                >
                  <div className="flex items-center gap-2">
                    <span className="truncate font-mono text-xs font-medium text-zinc-900 dark:text-zinc-100">
                      {item.command}
                    </span>
                    <span
                      className={`font-mono text-[10px] ${
                        item.result.exitCode === 0 ? 'text-emerald-600 dark:text-emerald-400' : 'text-red-600 dark:text-red-400'
                      }`}
                    >
                      Exit {item.result.exitCode}
                    </span>
                  </div>
                  <p className="mt-0.5 text-[11px] text-zinc-500 dark:text-zinc-400">
                    {item.projectName} · {item.result.durationMs} ms
                  </p>
                </button>
                <button
                  type="button"
                  onClick={() => {
                    setProjectId(item.projectId);
                    setCommand(item.command);
                    setTimeoutSec(item.timeoutSec);
                    void execute(item.projectId, item.command, item.timeoutSec);
                  }}
                  disabled={running}
                  title="Relancer"
                  className="inline-flex h-8 w-8 shrink-0 items-center justify-center rounded-md border border-zinc-200 text-zinc-600 hover:bg-zinc-50 dark:border-zinc-800 dark:text-zinc-300 dark:hover:bg-zinc-800"
                >
                  <RotateCcw className="h-3.5 w-3.5" />
                </button>
              </div>
            ))}
          </div>
        </section>
      )}
    </div>
  );
}
