import { useState } from 'react';
import { Check, Copy, FolderKanban, Server, X } from 'lucide-react';
import type { Project } from '../api/types';

interface ProjectInfoSheetProps {
  project: Project;
  machineOnline: boolean;
  activeSessions: number;
  totalSessions: number;
  onClose: () => void;
}

export function ProjectInfoSheet({
  project,
  machineOnline,
  activeSessions,
  totalSessions,
  onClose,
}: ProjectInfoSheetProps) {
  const [copied, setCopied] = useState(false);

  async function handleCopyPath() {
    try {
      await navigator.clipboard.writeText(project.path);
      setCopied(true);
      window.setTimeout(() => setCopied(false), 1800);
    } catch {
      // Clipboard unavailable.
    }
  }

  return (
    <div
      className="fixed inset-0 z-50 flex items-end justify-center bg-black/50 backdrop-blur-[2px]"
      role="dialog"
      aria-modal="true"
      aria-labelledby="project-info-title"
      onClick={onClose}
    >
      <div
        className="w-full max-w-md rounded-t-2xl border border-zinc-200 bg-white p-5 pb-safe shadow-xl dark:border-zinc-800 dark:bg-zinc-900"
        onClick={(e) => e.stopPropagation()}
      >
        <div className="mb-4 flex items-center justify-between">
          <div className="flex min-w-0 items-center gap-2.5">
            <span className="flex h-9 w-9 shrink-0 items-center justify-center rounded-lg bg-zinc-100 text-zinc-600 dark:bg-zinc-800 dark:text-zinc-300">
              <FolderKanban className="h-4.5 w-4.5" />
            </span>
            <h3
              id="project-info-title"
              className="truncate text-sm font-semibold text-zinc-900 dark:text-zinc-100"
            >
              {project.name}
            </h3>
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

        <div className="space-y-2.5 text-xs">
          <div className="flex items-center justify-between gap-3">
            <span className="flex items-center gap-1.5 text-zinc-500 dark:text-zinc-400">
              <Server className="h-3.5 w-3.5" />
              Machine
            </span>
            <span className="flex items-center gap-1.5 font-medium text-zinc-900 dark:text-zinc-100">
              <span
                className={`h-1.5 w-1.5 rounded-full ${
                  machineOnline ? 'bg-emerald-500' : 'bg-zinc-400'
                }`}
              />
              {project.machineName}
            </span>
          </div>

          <div className="flex items-center justify-between gap-3">
            <span className="text-zinc-500 dark:text-zinc-400">Sessions</span>
            <span className="font-medium text-zinc-900 dark:text-zinc-100">
              {activeSessions} active{activeSessions > 1 ? 's' : ''} · {totalSessions} au total
            </span>
          </div>

          <div className="flex items-center justify-between gap-3">
            <span className="text-zinc-500 dark:text-zinc-400">Créé le</span>
            <span className="font-medium text-zinc-900 dark:text-zinc-100">
              {new Date(project.createdAt).toLocaleDateString('fr-FR', {
                day: 'numeric',
                month: 'short',
                year: 'numeric',
              })}
            </span>
          </div>

          <div className="rounded-lg border border-zinc-200 bg-zinc-50 p-3 dark:border-zinc-800 dark:bg-zinc-900">
            <div className="flex items-center justify-between gap-2">
              <span className="text-[10px] font-medium uppercase tracking-wider text-zinc-400">
                Chemin complet
              </span>
              <button
                type="button"
                onClick={() => void handleCopyPath()}
                aria-label="Copier le chemin"
                className="inline-flex items-center gap-1 rounded-md px-2 py-1 text-[11px] font-medium text-zinc-500 transition hover:bg-zinc-200/70 hover:text-zinc-800 dark:text-zinc-400 dark:hover:bg-zinc-800 dark:hover:text-zinc-200"
              >
                {copied ? (
                  <>
                    <Check className="h-3 w-3 text-emerald-500" />
                    <span className="text-emerald-600 dark:text-emerald-400">Copié</span>
                  </>
                ) : (
                  <>
                    <Copy className="h-3 w-3" />
                    Copier
                  </>
                )}
              </button>
            </div>
            <p className="mt-1 break-all font-mono text-[11px] text-zinc-800 dark:text-zinc-200">
              {project.path}
            </p>
          </div>
        </div>
      </div>
    </div>
  );
}
