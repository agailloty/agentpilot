import { useEffect, useMemo, useState, type FormEvent } from 'react';
import { FolderOpen, GitBranch, Loader2, X } from 'lucide-react';
import { useApp } from '../context/AppContext';
import type { Project } from '../api/types';
import { DirectoryBrowser } from './DirectoryBrowser';

interface CreateProjectModalProps {
  open: boolean;
  defaultMachine?: string;
  onClose: () => void;
  onCreated: (project: Project) => void;
}

function joinPreviewPath(os: string, parentPath: string, name: string): string {
  const cleanParent = parentPath.trim();
  const cleanName = name.trim();
  if (!cleanParent || !cleanName) return cleanParent || '';
  const isWin = os.toLowerCase() === 'windows' || /^[a-zA-Z]:/.test(cleanParent);
  if (isWin) {
    return `${cleanParent.replace(/[\\/]+$/, '')}\\${cleanName}`;
  }
  return `${cleanParent.replace(/\/+$/, '')}/${cleanName}`;
}

export function CreateProjectModal({
  open,
  defaultMachine,
  onClose,
  onCreated,
}: CreateProjectModalProps) {
  const { client, machines, refreshProjects } = useApp();
  const [name, setName] = useState('');
  const [machineName, setMachineName] = useState('');
  const [browserPath, setBrowserPath] = useState('');
  const [parentInput, setParentInput] = useState('');
  const [reloadTrigger, setReloadTrigger] = useState(0);
  const [gitInit, setGitInit] = useState(true);
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const selectedMachine = useMemo(
    () => machines.find((m) => m.name === machineName),
    [machines, machineName],
  );

  useEffect(() => {
    if (!open) return;
    const preferred =
      (defaultMachine && machines.find((m) => m.name === defaultMachine)) ||
      machines.find((m) => m.online) ||
      machines[0];
    const initialMachine = preferred?.name ?? '';
    const initialRoot = preferred?.roots[0] ?? '';
    setName('');
    setMachineName(initialMachine);
    setBrowserPath(initialRoot);
    setParentInput(initialRoot);
    setGitInit(true);
    setError(null);
  }, [open, defaultMachine, machines]);

  if (!open || !client) return null;

  function handleMachineChange(nextName: string) {
    setMachineName(nextName);
    const m = machines.find((item) => item.name === nextName);
    const nextRoot = m?.roots[0] ?? '';
    setBrowserPath(nextRoot);
    setParentInput(nextRoot);
  }

  function handleSelectBrowserPath(nextPath: string) {
    setBrowserPath(nextPath);
    setParentInput(nextPath);
  }

  function handleOpenManualPath() {
    const trimmed = parentInput.trim();
    if (!trimmed) return;
    setBrowserPath(trimmed);
    setReloadTrigger((n) => n + 1);
  }

  async function handleSubmit(e: FormEvent) {
    e.preventDefault();
    if (!client) return;
    if (!name.trim()) {
      setError('Le nom du projet est requis.');
      return;
    }
    if (!machineName) {
      setError('Veuillez choisir une machine cible.');
      return;
    }
    if (!parentInput.trim()) {
      setError('Le dossier parent est requis.');
      return;
    }
    setSubmitting(true);
    setError(null);
    try {
      const created = await client.createProject({
        name: name.trim(),
        machineName,
        parentPath: parentInput.trim(),
        gitInit,
      });
      await refreshProjects();
      onCreated(created);
      onClose();
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Échec de création du projet');
    } finally {
      setSubmitting(false);
    }
  }

  const previewPath = joinPreviewPath(selectedMachine?.os ?? '', parentInput, name);

  return (
    <div
      className="fixed inset-0 z-50 flex items-end justify-center bg-black/50 p-0 backdrop-blur-[2px] sm:items-center sm:p-4"
      role="dialog"
      aria-modal="true"
      aria-labelledby="create-project-title"
    >
      <div className="max-h-[92dvh] w-full max-w-xl overflow-y-auto rounded-t-2xl border border-zinc-200 bg-white p-5 shadow-xl sm:rounded-xl dark:border-zinc-800 dark:bg-zinc-900">
        <div className="mb-4 flex items-center justify-between">
          <div>
            <h2
              id="create-project-title"
              className="text-sm font-semibold text-zinc-900 dark:text-zinc-100"
            >
              Nouveau projet
            </h2>
            <p className="text-xs text-zinc-500 dark:text-zinc-400">
              Crée un dossier distant sur le runner cible et l&apos;enregistre dans AgentPilot.
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
          <div className="grid gap-3 sm:grid-cols-2">
            <div>
              <label
                htmlFor="proj-name"
                className="mb-1 block text-xs font-medium text-zinc-700 dark:text-zinc-300"
              >
                Nom du projet
              </label>
              <input
                id="proj-name"
                type="text"
                value={name}
                onChange={(e) => setName(e.target.value)}
                placeholder="ex: api-billing"
                className="w-full rounded-lg border border-zinc-200 bg-white px-3 py-2 text-sm text-zinc-900 outline-none focus:border-indigo-500 focus:ring-2 focus:ring-indigo-500/20 dark:border-zinc-800 dark:bg-zinc-950 dark:text-zinc-100"
              />
            </div>

            <div>
              <label
                htmlFor="proj-machine"
                className="mb-1 block text-xs font-medium text-zinc-700 dark:text-zinc-300"
              >
                Machine cible
              </label>
              <select
                id="proj-machine"
                value={machineName}
                onChange={(e) => handleMachineChange(e.target.value)}
                className="w-full rounded-lg border border-zinc-200 bg-white px-3 py-2 text-sm text-zinc-900 outline-none focus:border-indigo-500 dark:border-zinc-800 dark:bg-zinc-950 dark:text-zinc-100"
              >
                {machines.map((m) => (
                  <option key={m.name} value={m.name}>
                    {m.name} ({m.online ? 'en ligne' : 'hors ligne'})
                  </option>
                ))}
              </select>
            </div>
          </div>

          <div>
            <div className="mb-1.5 flex items-center justify-between">
              <span className="text-xs font-medium text-zinc-700 dark:text-zinc-300">
                Explorateur distant
              </span>
              {selectedMachine && (
                <span className="font-mono text-[11px] text-zinc-400">
                  {selectedMachine.os || 'os inconnu'}
                </span>
              )}
            </div>
            <DirectoryBrowser
              client={client}
              machine={selectedMachine}
              currentPath={browserPath}
              onSelectPath={handleSelectBrowserPath}
              reloadTrigger={reloadTrigger}
            />
          </div>

          <div>
            <label
              htmlFor="proj-parent"
              className="mb-1 block text-xs font-medium text-zinc-700 dark:text-zinc-300"
            >
              Dossier parent
            </label>
            <div className="flex gap-2">
              <input
                id="proj-parent"
                type="text"
                value={parentInput}
                onChange={(e) => setParentInput(e.target.value)}
                placeholder="/home/user/repos ou C:\Users\user\repos"
                className="min-w-0 flex-1 rounded-lg border border-zinc-200 bg-white px-3 py-2 font-mono text-xs text-zinc-900 outline-none focus:border-indigo-500 dark:border-zinc-800 dark:bg-zinc-950 dark:text-zinc-100"
              />
              <button
                type="button"
                onClick={handleOpenManualPath}
                className="inline-flex shrink-0 items-center gap-1.5 rounded-lg border border-zinc-200 bg-white px-3 py-2 text-xs font-medium text-zinc-700 hover:bg-zinc-50 dark:border-zinc-800 dark:bg-zinc-950 dark:text-zinc-300 dark:hover:bg-zinc-900"
              >
                <FolderOpen className="h-3.5 w-3.5" />
                <span>Ouvrir</span>
              </button>
            </div>
          </div>

          {previewPath && (
            <div className="rounded-lg border border-zinc-200 bg-zinc-50 px-3 py-2 dark:border-zinc-800 dark:bg-zinc-950">
              <div className="text-[10px] font-medium uppercase tracking-wider text-zinc-400">
                Chemin final
              </div>
              <div className="mt-0.5 break-all font-mono text-xs text-zinc-800 dark:text-zinc-200">
                {previewPath}
              </div>
            </div>
          )}

          <label className="flex cursor-pointer items-center justify-between rounded-lg border border-zinc-200 px-3 py-2.5 dark:border-zinc-800">
            <span className="flex items-center gap-2 text-xs font-medium text-zinc-700 dark:text-zinc-300">
              <GitBranch className="h-3.5 w-3.5 text-zinc-400" />
              <span>Initialiser un dépôt Git (git init)</span>
            </span>
            <input
              type="checkbox"
              checked={gitInit}
              onChange={(e) => setGitInit(e.target.checked)}
              className="h-4 w-4 rounded border-zinc-300 accent-indigo-600"
            />
          </label>

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
              disabled={submitting || !selectedMachine?.online}
              className="inline-flex items-center gap-2 rounded-lg bg-indigo-600 px-4 py-2 text-xs font-medium text-white transition hover:bg-indigo-700 disabled:opacity-50 dark:bg-indigo-500 dark:hover:bg-indigo-400"
            >
              {submitting && <Loader2 className="h-3.5 w-3.5 animate-spin" />}
              <span>Créer le projet</span>
            </button>
          </div>
        </form>
      </div>
    </div>
  );
}
