import { useEffect, useMemo, useState } from 'react';
import { Loader2, MessageSquare, Plus, Search } from 'lucide-react';
import { useApp } from '../context/AppContext';
import type { SessionMeta } from '../api/types';

interface SessionsViewProps {
  onOpenSession: (session: SessionMeta) => void;
  onNewSession: () => void;
}

type StatusFilter = 'all' | 'running' | 'closed';

function bucketLabel(isoDate: string): "Aujourd'hui" | 'Hier' | 'Plus ancien' {
  const dt = new Date(isoDate);
  const now = new Date();
  const startOfToday = new Date(now.getFullYear(), now.getMonth(), now.getDate()).getTime();
  const startOfYesterday = startOfToday - 24 * 60 * 60 * 1000;
  const ts = dt.getTime();
  if (ts >= startOfToday) return "Aujourd'hui";
  if (ts >= startOfYesterday) return 'Hier';
  return 'Plus ancien';
}

export function SessionsView({ onOpenSession, onNewSession }: SessionsViewProps) {
  const { client, projects, sessions: globalSessions } = useApp();
  const [query, setQuery] = useState('');
  const [debouncedQuery, setDebouncedQuery] = useState('');
  const [statusFilter, setStatusFilter] = useState<StatusFilter>('all');
  const [results, setResults] = useState<SessionMeta[]>(globalSessions);
  const [searching, setSearching] = useState(false);

  useEffect(() => {
    const timer = window.setTimeout(() => {
      setDebouncedQuery(query.trim());
    }, 250);
    return () => window.clearTimeout(timer);
  }, [query]);

  useEffect(() => {
    if (!client) return;
    if (!debouncedQuery && statusFilter === 'all') {
      setResults(globalSessions);
      return;
    }
    let cancelled = false;
    setSearching(true);
    client
      .fetchSessions({
        q: debouncedQuery || undefined,
        status: statusFilter === 'all' ? undefined : statusFilter,
      })
      .then((list) => {
        if (!cancelled) setResults(list);
      })
      .catch(() => {
        // Keep previous list on transient error.
      })
      .finally(() => {
        if (!cancelled) setSearching(false);
      });
    return () => {
      cancelled = true;
    };
  }, [client, debouncedQuery, statusFilter, globalSessions]);

  const projectMap = useMemo(
    () => new Map(projects.map((p) => [p.id, p])),
    [projects],
  );

  const grouped = useMemo(() => {
    const order: ("Aujourd'hui" | 'Hier' | 'Plus ancien')[] = [
      "Aujourd'hui",
      'Hier',
      'Plus ancien',
    ];
    const buckets: Record<string, SessionMeta[]> = {
      "Aujourd'hui": [],
      Hier: [],
      'Plus ancien': [],
    };
    const sorted = [...results].sort(
      (a, b) => new Date(b.createdAt).getTime() - new Date(a.createdAt).getTime(),
    );
    for (const s of sorted) {
      buckets[bucketLabel(s.createdAt)].push(s);
    }
    return order
      .map((label) => ({ label, items: buckets[label] }))
      .filter((g) => g.items.length > 0);
  }, [results]);

  return (
    <div className="space-y-5">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <h1 className="text-xl font-semibold tracking-tight text-zinc-950 dark:text-zinc-50">
          Sessions
        </h1>
        <button
          type="button"
          onClick={onNewSession}
          aria-label="Nouvelle session"
          className="inline-flex h-10 w-10 items-center justify-center rounded-full bg-zinc-900 text-white shadow-xs transition hover:bg-zinc-800 sm:h-auto sm:w-auto sm:rounded-lg sm:px-3.5 sm:py-2 dark:bg-zinc-100 dark:text-zinc-950 dark:hover:bg-zinc-200"
        >
          <Plus className="h-4 w-4" />
          <span className="hidden text-xs font-medium sm:inline">Nouvelle session</span>
        </button>
      </div>

      {/* Search + Segmented Status Filter */}
      <div className="flex flex-col gap-3 sm:flex-row sm:items-center">
        <div className="relative flex-1">
          <Search className="pointer-events-none absolute top-1/2 left-3 h-4 w-4 -translate-y-1/2 text-zinc-400" />
          <input
            type="text"
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            placeholder="Rechercher par agent, projet, chemin…"
            className="w-full rounded-lg border border-zinc-200 bg-white py-2 pr-8 pl-9 text-sm text-zinc-900 outline-none focus:border-indigo-500 focus:ring-2 focus:ring-indigo-500/20 dark:border-zinc-800 dark:bg-zinc-900 dark:text-zinc-100"
          />
          {searching && (
            <Loader2 className="absolute top-1/2 right-3 h-3.5 w-3.5 -translate-y-1/2 animate-spin text-zinc-400" />
          )}
        </div>

        <div className="inline-flex rounded-lg border border-zinc-200 bg-white p-0.5 dark:border-zinc-800 dark:bg-zinc-900">
          {(
            [
              { id: 'all', label: 'Toutes' },
              { id: 'running', label: 'En cours' },
              { id: 'closed', label: 'Terminées' },
            ] as const
          ).map((opt) => (
            <button
              key={opt.id}
              type="button"
              onClick={() => setStatusFilter(opt.id)}
              className={`rounded-md px-3 py-1.5 text-xs font-medium transition ${
                statusFilter === opt.id
                  ? 'bg-zinc-900 text-white dark:bg-zinc-100 dark:text-zinc-900'
                  : 'text-zinc-600 hover:text-zinc-900 dark:text-zinc-400 dark:hover:text-zinc-100'
              }`}
            >
              {opt.label}
            </button>
          ))}
        </div>
      </div>

      {grouped.length === 0 ? (
        <div className="rounded-xl border border-dashed border-zinc-200 bg-white p-8 text-center dark:border-zinc-800 dark:bg-zinc-900/40">
          <MessageSquare className="mx-auto h-7 w-7 text-zinc-300 dark:text-zinc-700" />
          <p className="mt-2 text-sm font-medium text-zinc-700 dark:text-zinc-300">
            Aucune session trouvée
          </p>
          <p className="mt-1 text-xs text-zinc-500 dark:text-zinc-400">
            Modifiez vos filtres ou démarrez une nouvelle session agent.
          </p>
        </div>
      ) : (
        <div className="space-y-5">
          {grouped.map((group) => (
            <section key={group.label} className="space-y-2">
              <h2 className="text-xs font-semibold uppercase tracking-wider text-zinc-500 dark:text-zinc-400">
                {group.label}
              </h2>
              <div className="divide-y divide-zinc-200 overflow-hidden rounded-xl border border-zinc-200 bg-white dark:divide-zinc-800 dark:border-zinc-800 dark:bg-zinc-900/70">
                {group.items.map((s) => {
                  const proj = projectMap.get(s.projectId);
                  return (
                    <button
                      key={s.id}
                      type="button"
                      onClick={() => onOpenSession(s)}
                      className="flex w-full items-center justify-between gap-3 px-4 py-3 text-left transition hover:bg-zinc-50 dark:hover:bg-zinc-900"
                    >
                      <div className="min-w-0 flex-1">
                        <div className="flex flex-wrap items-center gap-2">
                          <span
                            className={`h-2 w-2 shrink-0 rounded-full ${
                              s.status === 'running'
                                ? s.waitingApproval
                                  ? 'bg-amber-500'
                                  : 'bg-emerald-500'
                                : 'bg-zinc-400'
                            }`}
                          />
                          <span className="truncate text-sm font-medium text-zinc-900 dark:text-zinc-100">
                            {proj?.name ?? s.projectId}
                          </span>
                          <span className="rounded bg-zinc-100 px-1.5 py-0.5 font-mono text-[10px] text-zinc-600 dark:bg-zinc-800 dark:text-zinc-300">
                            {s.agentId}
                          </span>
                          <span className="hidden font-mono text-[11px] text-zinc-400 sm:inline">
                            {s.machineName}
                          </span>
                        </div>
                        <p className="mt-1 hidden truncate font-mono text-[11px] text-zinc-500 sm:block dark:text-zinc-400">
                          {s.cwd}
                        </p>
                      </div>
                      <span className="shrink-0 font-mono text-[11px] text-zinc-400">
                        {new Date(s.createdAt).toLocaleTimeString('fr-FR', {
                          hour: '2-digit',
                          minute: '2-digit',
                        })}
                      </span>
                    </button>
                  );
                })}
              </div>
            </section>
          ))}
        </div>
      )}
    </div>
  );
}
