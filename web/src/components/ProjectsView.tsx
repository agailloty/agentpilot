import { useMemo, useState } from 'react';
import { ChevronRight, FolderKanban, MessageSquare, Plus } from 'lucide-react';
import { useApp } from '../context/AppContext';
import type { Project, SessionMeta } from '../api/types';

interface ProjectsViewProps {
  onSelectProject: (project: Project) => void;
  onOpenCreateProject: (defaultMachine?: string) => void;
  onOpenSession: (session: SessionMeta) => void;
}

export function ProjectsView({
  onSelectProject,
  onOpenCreateProject,
  onOpenSession,
}: ProjectsViewProps) {
  const { machines, projects, sessions, loading, error } = useApp();
  const [machineFilter, setMachineFilter] = useState<string>('all');

  const machineMap = useMemo(
    () => new Map(machines.map((m) => [m.name, m])),
    [machines],
  );

  const projectMap = useMemo(
    () => new Map(projects.map((p) => [p.id, p])),
    [projects],
  );

  const runningSessions = useMemo(
    () =>
      sessions.filter(
        (s) =>
          s.status === 'running' &&
          (machineFilter === 'all' || s.machineName === machineFilter),
      ),
    [sessions, machineFilter],
  );

  const filteredProjects = useMemo(
    () =>
      projects.filter((p) =>
        machineFilter === 'all' ? true : p.machineName === machineFilter,
      ),
    [projects, machineFilter],
  );

  return (
    <div className="space-y-6">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div>
          <h1 className="text-xl font-semibold tracking-tight text-zinc-950 dark:text-zinc-50">
            Projets
          </h1>
          <p className="mt-0.5 text-xs text-zinc-500 dark:text-zinc-400">
            Gérez vos dépôts distants et accédez aux sessions actives.
          </p>
        </div>
        <button
          type="button"
          onClick={() =>
            onOpenCreateProject(machineFilter === 'all' ? undefined : machineFilter)
          }
          className="inline-flex min-h-[38px] items-center gap-1.5 rounded-lg bg-zinc-900 px-3.5 py-2 text-xs font-medium text-white shadow-xs transition hover:bg-zinc-800 dark:bg-zinc-100 dark:text-zinc-950 dark:hover:bg-zinc-200"
        >
          <Plus className="h-3.5 w-3.5" />
          <span>Nouveau projet</span>
        </button>
      </div>

      {/* Machine Filter Chips */}
      {machines.length > 0 && (
        <div className="flex flex-wrap items-center gap-1.5">
          <button
            type="button"
            onClick={() => setMachineFilter('all')}
            className={`rounded-full border px-3 py-1 text-xs font-medium transition ${
              machineFilter === 'all'
                ? 'border-indigo-500 bg-indigo-50 text-indigo-700 dark:border-indigo-400 dark:bg-indigo-500/15 dark:text-indigo-300'
                : 'border-zinc-200 bg-white text-zinc-600 hover:bg-zinc-50 dark:border-zinc-800 dark:bg-zinc-900 dark:text-zinc-400 dark:hover:bg-zinc-800'
            }`}
          >
            Toutes ({projects.length})
          </button>
          {machines.map((m) => {
            const active = machineFilter === m.name;
            return (
              <button
                key={m.name}
                type="button"
                onClick={() => setMachineFilter(m.name)}
                className={`inline-flex items-center gap-1.5 rounded-full border px-3 py-1 text-xs font-medium transition ${
                  active
                    ? 'border-indigo-500 bg-indigo-50 text-indigo-700 dark:border-indigo-400 dark:bg-indigo-500/15 dark:text-indigo-300'
                    : 'border-zinc-200 bg-white text-zinc-600 hover:bg-zinc-50 dark:border-zinc-800 dark:bg-zinc-900 dark:text-zinc-400 dark:hover:bg-zinc-800'
                }`}
              >
                <span
                  className={`h-1.5 w-1.5 rounded-full ${
                    m.online ? 'bg-emerald-500' : 'bg-zinc-400'
                  }`}
                />
                <span>{m.name}</span>
              </button>
            );
          })}
        </div>
      )}

      {error && (
        <div className="rounded-lg border border-red-200 bg-red-50 px-3.5 py-2.5 text-xs text-red-700 dark:border-red-900/60 dark:bg-red-950/40 dark:text-red-300">
          {error}
        </div>
      )}

      {/* Active Running Sessions */}
      {runningSessions.length > 0 && (
        <section className="space-y-2.5">
          <div className="flex items-center justify-between">
            <h2 className="text-xs font-semibold uppercase tracking-wider text-zinc-500 dark:text-zinc-400">
              En cours
            </h2>
            <span className="font-mono text-xs text-zinc-400">{runningSessions.length}</span>
          </div>
          <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
            {runningSessions.map((s) => {
              const proj = projectMap.get(s.projectId);
              return (
                <button
                  key={s.id}
                  type="button"
                  onClick={() => onOpenSession(s)}
                  className="group flex items-center justify-between gap-3 rounded-xl border border-zinc-200/90 bg-white p-4 text-left shadow-[0_1px_2px_rgba(0,0,0,0.03)] transition hover:border-zinc-300 hover:shadow-xs dark:border-zinc-800/90 dark:bg-zinc-900/60 dark:hover:border-zinc-700"
                >
                  <div className="min-w-0">
                    <div className="flex items-center gap-2">
                      <span
                        className={`h-2 w-2 shrink-0 rounded-full ${
                          s.waitingApproval ? 'bg-amber-500' : 'bg-emerald-500'
                        }`}
                      />
                      <span className="truncate text-sm font-medium text-zinc-900 dark:text-zinc-100">
                        {proj?.name ?? s.projectId}
                      </span>
                      <span className="rounded bg-zinc-100 px-1.5 py-0.5 font-mono text-[10px] text-zinc-600 dark:bg-zinc-800 dark:text-zinc-300">
                        {s.agentId}
                      </span>
                    </div>
                    <p className="mt-1 truncate font-mono text-[11px] text-zinc-500 dark:text-zinc-400">
                      {s.machineName} · {s.waitingApproval ? 'Approbation requise' : 'Actif'}
                    </p>
                  </div>
                  <MessageSquare className="h-4 w-4 shrink-0 text-zinc-400" />
                </button>
              );
            })}
          </div>
        </section>
      )}

      {/* Projects List */}
      <section className="space-y-2.5">
        <div className="flex items-center justify-between">
          <h2 className="text-xs font-semibold uppercase tracking-wider text-zinc-500 dark:text-zinc-400">
            Dépôts
          </h2>
          <span className="font-mono text-xs text-zinc-400">{filteredProjects.length}</span>
        </div>

        {loading && projects.length === 0 ? (
          <div className="rounded-xl border border-zinc-200 bg-white p-8 text-center text-xs text-zinc-500 dark:border-zinc-800 dark:bg-zinc-900/50">
            Chargement des projets…
          </div>
        ) : filteredProjects.length === 0 ? (
          <div className="rounded-xl border border-dashed border-zinc-200 bg-white p-8 text-center dark:border-zinc-800 dark:bg-zinc-900/40">
            <FolderKanban className="mx-auto h-7 w-7 text-zinc-300 dark:text-zinc-700" />
            <p className="mt-2 text-sm font-medium text-zinc-700 dark:text-zinc-300">
              Aucun projet enregistré
            </p>
            <p className="mt-1 text-xs text-zinc-500 dark:text-zinc-400">
              Créez un projet sur une machine connectée pour lancer un agent CLI.
            </p>
          </div>
        ) : (
          <div className="divide-y divide-zinc-200 overflow-hidden rounded-xl border border-zinc-200 bg-white dark:divide-zinc-800 dark:border-zinc-800 dark:bg-zinc-900/70">
            {filteredProjects.map((project) => {
              const machine = machineMap.get(project.machineName);
              const online = machine?.online ?? false;
              const activeCount = sessions.filter(
                (s) => s.projectId === project.id && s.status === 'running',
              ).length;

              return (
                <button
                  key={project.id}
                  type="button"
                  onClick={() => onSelectProject(project)}
                  className="flex w-full items-center justify-between gap-3 px-4 py-3.5 text-left transition hover:bg-zinc-50 dark:hover:bg-zinc-900"
                >
                  <div className="min-w-0 flex-1">
                    <div className="flex items-center gap-2">
                      <span className="truncate text-sm font-medium text-zinc-900 dark:text-zinc-100">
                        {project.name}
                      </span>
                      <span className="inline-flex items-center gap-1 rounded-full border border-zinc-200 px-2 py-0.5 text-[11px] text-zinc-600 dark:border-zinc-800 dark:text-zinc-400">
                        <span
                          className={`h-1.5 w-1.5 rounded-full ${
                            online ? 'bg-emerald-500' : 'bg-zinc-400'
                          }`}
                        />
                        {project.machineName}
                      </span>
                      {activeCount > 0 && (
                        <span className="rounded-full bg-indigo-50 px-2 py-0.5 font-mono text-[10px] font-medium text-indigo-700 dark:bg-indigo-500/15 dark:text-indigo-300">
                          {activeCount} actif{activeCount > 1 ? 's' : ''}
                        </span>
                      )}
                    </div>
                    <p className="mt-1 truncate font-mono text-xs text-zinc-500 dark:text-zinc-400">
                      {project.path}
                    </p>
                  </div>
                  <ChevronRight className="h-4 w-4 shrink-0 text-zinc-400" />
                </button>
              );
            })}
          </div>
        )}
      </section>
    </div>
  );
}
