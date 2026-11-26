import { useMemo, useState } from 'react';
import {
  ArrowLeft,
  Loader2,
  MessageSquare,
  Plus,
  Terminal,
  Trash2,
} from 'lucide-react';
import { useApp } from '../context/AppContext';
import type { Project, SessionMeta } from '../api/types';

interface ProjectDetailViewProps {
  project: Project;
  onBack: () => void;
  onNewSession: (projectId: string) => void;
  onOpenOneShot: (projectId: string) => void;
  onOpenSession: (session: SessionMeta) => void;
  onDeleted: () => void;
}

export function ProjectDetailView({
  project,
  onBack,
  onNewSession,
  onOpenOneShot,
  onOpenSession,
  onDeleted,
}: ProjectDetailViewProps) {
  const { client, machines, sessions, refreshProjects, refreshSessions } = useApp();
  const [showDeleteConfirm, setShowDeleteConfirm] = useState(false);
  const [deleteFiles, setDeleteFiles] = useState(false);
  const [deleting, setDeleting] = useState(false);
  const [deleteError, setDeleteError] = useState<string | null>(null);

  const machine = useMemo(
    () => machines.find((m) => m.name === project.machineName),
    [machines, project.machineName],
  );

  const projectSessions = useMemo(
    () =>
      sessions
        .filter((s) => s.projectId === project.id)
        .sort(
          (a, b) =>
            new Date(b.createdAt).getTime() - new Date(a.createdAt).getTime(),
        ),
    [sessions, project.id],
  );

  async function handleConfirmDelete() {
    if (!client) return;
    setDeleting(true);
    setDeleteError(null);
    try {
      await client.deleteProject(project.id, deleteFiles);
      await Promise.all([refreshProjects(), refreshSessions()]);
      onDeleted();
    } catch (err) {
      setDeleteError(err instanceof Error ? err.message : 'Échec de suppression');
    } finally {
      setDeleting(false);
    }
  }

  return (
    <div className="space-y-6">
      <div className="flex items-center gap-3">
        <button
          type="button"
          onClick={onBack}
          className="inline-flex h-9 items-center gap-1.5 rounded-lg border border-zinc-200 bg-white px-3 text-xs font-medium text-zinc-700 hover:bg-zinc-50 dark:border-zinc-800 dark:bg-zinc-900 dark:text-zinc-300 dark:hover:bg-zinc-800"
        >
          <ArrowLeft className="h-3.5 w-3.5" />
          <span>Projets</span>
        </button>
      </div>

      {/* Header Card */}
      <div className="rounded-xl border border-zinc-200 bg-white p-5 dark:border-zinc-800 dark:bg-zinc-900/70">
        <div className="flex flex-wrap items-start justify-between gap-4">
          <div className="min-w-0">
            <div className="flex flex-wrap items-center gap-2">
              <h1 className="text-lg font-semibold tracking-tight text-zinc-900 dark:text-zinc-100">
                {project.name}
              </h1>
              <span className="inline-flex items-center gap-1.5 rounded-full border border-zinc-200 px-2.5 py-0.5 text-xs font-medium text-zinc-600 dark:border-zinc-800 dark:text-zinc-300">
                <span
                  className={`h-2 w-2 rounded-full ${
                    machine?.online ? 'bg-emerald-500' : 'bg-zinc-400'
                  }`}
                />
                {project.machineName} ({machine?.online ? 'en ligne' : 'hors ligne'})
              </span>
            </div>
            <p className="mt-2 break-all font-mono text-xs text-zinc-500 dark:text-zinc-400">
              {project.path}
            </p>
          </div>

          <div className="flex flex-wrap items-center gap-2">
            <button
              type="button"
              onClick={() => onNewSession(project.id)}
              className="inline-flex min-h-[38px] items-center gap-1.5 rounded-lg bg-indigo-600 px-3.5 py-2 text-xs font-medium text-white transition hover:bg-indigo-700 dark:bg-indigo-500 dark:hover:bg-indigo-400"
            >
              <Plus className="h-3.5 w-3.5" />
              <span>Nouvelle session</span>
            </button>
            <button
              type="button"
              onClick={() => onOpenOneShot(project.id)}
              className="inline-flex min-h-[38px] items-center gap-1.5 rounded-lg border border-zinc-200 bg-white px-3.5 py-2 text-xs font-medium text-zinc-700 transition hover:bg-zinc-50 dark:border-zinc-800 dark:bg-zinc-950 dark:text-zinc-300 dark:hover:bg-zinc-900"
            >
              <Terminal className="h-3.5 w-3.5" />
              <span>Commande ponctuelle</span>
            </button>
            <button
              type="button"
              onClick={() => {
                setDeleteFiles(false);
                setDeleteError(null);
                setShowDeleteConfirm((v) => !v);
              }}
              className="inline-flex min-h-[38px] items-center gap-1.5 rounded-lg border border-red-200 bg-white px-3 py-2 text-xs font-medium text-red-600 transition hover:bg-red-50 dark:border-red-900/50 dark:bg-zinc-950 dark:text-red-400 dark:hover:bg-red-950/30"
            >
              <Trash2 className="h-3.5 w-3.5" />
              <span>Supprimer</span>
            </button>
          </div>
        </div>

        {showDeleteConfirm && (
          <div className="mt-4 rounded-lg border border-red-200 bg-red-50/70 p-4 dark:border-red-900/60 dark:bg-red-950/30">
            <p className="text-xs font-medium text-red-800 dark:text-red-200">
              Confirmer la suppression du projet « {project.name} » ?
            </p>
            <label className="mt-2.5 flex cursor-pointer items-center gap-2 text-xs text-red-700 dark:text-red-300">
              <input
                type="checkbox"
                checked={deleteFiles}
                onChange={(e) => setDeleteFiles(e.target.checked)}
                className="h-4 w-4 rounded accent-red-600"
              />
              <span>
                Supprimer aussi les fichiers sur le disque distant (
                <code className="font-mono">{project.path}</code>)
              </span>
            </label>
            {deleteError && (
              <p className="mt-2 text-xs text-red-600 dark:text-red-400">{deleteError}</p>
            )}
            <div className="mt-3 flex items-center gap-2">
              <button
                type="button"
                onClick={handleConfirmDelete}
                disabled={deleting}
                className="inline-flex items-center gap-1.5 rounded-md bg-red-600 px-3 py-1.5 text-xs font-medium text-white hover:bg-red-700 disabled:opacity-50"
              >
                {deleting && <Loader2 className="h-3.5 w-3.5 animate-spin" />}
                <span>Confirmer la suppression</span>
              </button>
              <button
                type="button"
                onClick={() => setShowDeleteConfirm(false)}
                className="rounded-md border border-zinc-200 bg-white px-3 py-1.5 text-xs font-medium text-zinc-600 hover:bg-zinc-50 dark:border-zinc-800 dark:bg-zinc-900 dark:text-zinc-300"
              >
                Annuler
              </button>
            </div>
          </div>
        )}
      </div>

      {/* Project Sessions */}
      <section className="space-y-2.5">
        <div className="flex items-center justify-between">
          <h2 className="text-xs font-semibold uppercase tracking-wider text-zinc-500 dark:text-zinc-400">
            Sessions du projet
          </h2>
          <span className="font-mono text-xs text-zinc-400">{projectSessions.length}</span>
        </div>

        {projectSessions.length === 0 ? (
          <div className="rounded-xl border border-dashed border-zinc-200 bg-white p-6 text-center text-xs text-zinc-500 dark:border-zinc-800 dark:bg-zinc-900/40 dark:text-zinc-400">
            Aucune session pour ce projet. Lancez une nouvelle session pour démarrer un agent.
          </div>
        ) : (
          <div className="divide-y divide-zinc-200 overflow-hidden rounded-xl border border-zinc-200 bg-white dark:divide-zinc-800 dark:border-zinc-800 dark:bg-zinc-900/70">
            {projectSessions.map((s) => (
              <button
                key={s.id}
                type="button"
                onClick={() => onOpenSession(s)}
                className="flex w-full items-center justify-between gap-3 px-4 py-3 text-left transition hover:bg-zinc-50 dark:hover:bg-zinc-900"
              >
                <div className="min-w-0">
                  <div className="flex items-center gap-2">
                    <span
                      className={`h-2 w-2 rounded-full ${
                        s.status === 'running'
                          ? s.waitingApproval
                            ? 'bg-amber-500'
                            : 'bg-emerald-500'
                          : 'bg-zinc-400'
                      }`}
                    />
                    <span className="text-sm font-medium text-zinc-900 dark:text-zinc-100">
                      {s.agentId}
                    </span>
                    <span className="font-mono text-[11px] text-zinc-400">
                      {s.status === 'running' ? 'en cours' : 'terminée'}
                    </span>
                  </div>
                  <p className="mt-0.5 font-mono text-[11px] text-zinc-500 dark:text-zinc-400">
                    {new Date(s.createdAt).toLocaleString('fr-FR')}
                  </p>
                </div>
                <MessageSquare className="h-4 w-4 shrink-0 text-zinc-400" />
              </button>
            ))}
          </div>
        )}
      </section>
    </div>
  );
}
