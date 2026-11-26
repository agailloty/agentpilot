import { useCallback, useEffect, useMemo, useState } from 'react';
import {
  ArrowUp,
  ChevronRight,
  Folder,
  HardDrive,
  Loader2,
  RefreshCw,
  Search,
  WifiOff,
} from 'lucide-react';
import type { AgentPilotClient } from '../api/client';
import type { FSListing, Machine } from '../api/types';

interface DirectoryBrowserProps {
  client: AgentPilotClient;
  machine: Machine | undefined;
  currentPath: string;
  onSelectPath: (nextPath: string) => void;
  reloadTrigger?: number;
}

interface BreadcrumbSegment {
  label: string;
  path: string;
}

function buildBreadcrumbs(rawPath: string, os: string): BreadcrumbSegment[] {
  const trimmed = rawPath.trim();
  if (!trimmed || trimmed === '__drives__') return [];

  const isWin = os.toLowerCase() === 'windows' || /^[a-zA-Z]:/.test(trimmed);
  if (isWin) {
    const normalized = trimmed.replace(/\//g, '\\').replace(/\\+$/, '');
    const parts = normalized.split('\\').filter(Boolean);
    const segments: BreadcrumbSegment[] = [];
    parts.forEach((part, idx) => {
      if (idx === 0 && /^[a-zA-Z]:$/.test(part)) {
        segments.push({ label: `${part}\\`, path: `${part}\\` });
      } else {
        const prev = segments[idx - 1]?.path ?? '';
        const joined = prev.endsWith('\\') ? `${prev}${part}` : `${prev}\\${part}`;
        segments.push({ label: part, path: joined });
      }
    });
    return segments;
  }

  const normalized = trimmed.replace(/\/+$/, '') || '/';
  if (normalized === '/') {
    return [{ label: '/', path: '/' }];
  }
  const parts = normalized.split('/').filter(Boolean);
  const segments: BreadcrumbSegment[] = [{ label: '/', path: '/' }];
  let acc = '';
  for (const part of parts) {
    acc += `/${part}`;
    segments.push({ label: part, path: acc });
  }
  return segments;
}

export function DirectoryBrowser({
  client,
  machine,
  currentPath,
  onSelectPath,
  reloadTrigger = 0,
}: DirectoryBrowserProps) {
  const [listing, setListing] = useState<FSListing | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [filter, setFilter] = useState('');

  const loadDirectory = useCallback(
    async (targetPath: string) => {
      if (!machine || !machine.online) {
        setListing(null);
        setError(null);
        return;
      }
      setLoading(true);
      setError(null);
      try {
        const res = await client.listMachineDirectories(machine.name, targetPath);
        setListing(res);
        if (res.currentPath && res.currentPath !== targetPath && res.currentPath !== '__drives__') {
          onSelectPath(res.currentPath);
        }
      } catch (err) {
        setError(err instanceof Error ? err.message : 'Impossible de lister ce dossier');
      } finally {
        setLoading(false);
      }
    },
    [client, machine, onSelectPath],
  );

  useEffect(() => {
    void loadDirectory(currentPath);
  }, [loadDirectory, currentPath, reloadTrigger]);

  const effectiveOS = listing?.os || machine?.os || '';
  const isWindows = effectiveOS.toLowerCase() === 'windows';
  const roots = listing?.roots?.length ? listing.roots : (machine?.roots ?? []);

  const breadcrumbs = useMemo(
    () => buildBreadcrumbs(listing?.currentPath || currentPath, effectiveOS),
    [listing?.currentPath, currentPath, effectiveOS],
  );

  const filteredEntries = useMemo(() => {
    const entries = listing?.entries ?? [];
    const q = filter.trim().toLowerCase();
    if (!q) return entries;
    return entries.filter((e) => e.name.toLowerCase().includes(q));
  }, [listing?.entries, filter]);

  if (!machine) {
    return (
      <div className="rounded-lg border border-dashed border-zinc-200 p-4 text-center text-xs text-zinc-500 dark:border-zinc-800 dark:text-zinc-400">
        Sélectionnez une machine cible pour parcourir ses dossiers.
      </div>
    );
  }

  if (!machine.online) {
    return (
      <div className="flex items-center gap-2.5 rounded-lg border border-amber-200 bg-amber-50 p-3.5 text-xs text-amber-800 dark:border-amber-900/50 dark:bg-amber-950/30 dark:text-amber-300">
        <WifiOff className="h-4 w-4 shrink-0" />
        <span>
          Le runner <span className="font-mono font-medium">{machine.name}</span> est hors ligne.
          Démarrez le runner pour explorer les dossiers distants.
        </span>
      </div>
    );
  }

  return (
    <div className="overflow-hidden rounded-lg border border-zinc-200 bg-zinc-50/60 dark:border-zinc-800 dark:bg-zinc-950/60">
      {/* Roots + Windows Drives */}
      <div className="flex flex-wrap items-center gap-1.5 border-b border-zinc-200 bg-white px-2.5 py-2 dark:border-zinc-800 dark:bg-zinc-900/60">
        {isWindows && (
          <button
            type="button"
            onClick={() => {
              setFilter('');
              void loadDirectory('__drives__');
            }}
            className={`inline-flex items-center gap-1 rounded-md border px-2 py-1 text-[11px] font-medium transition ${
              listing?.currentPath === '__drives__'
                ? 'border-indigo-500 bg-indigo-50 text-indigo-700 dark:border-indigo-400 dark:bg-indigo-500/15 dark:text-indigo-300'
                : 'border-zinc-200 bg-white text-zinc-600 hover:bg-zinc-50 dark:border-zinc-800 dark:bg-zinc-950 dark:text-zinc-300 dark:hover:bg-zinc-900'
            }`}
          >
            <HardDrive className="h-3 w-3" />
            <span>Disques</span>
          </button>
        )}
        {roots.map((root) => {
          const active = listing?.currentPath === root;
          return (
            <button
              key={root}
              type="button"
              onClick={() => {
                setFilter('');
                onSelectPath(root);
              }}
              className={`max-w-[200px] truncate rounded-md border px-2 py-1 font-mono text-[11px] transition ${
                active
                  ? 'border-indigo-500 bg-indigo-50 text-indigo-700 dark:border-indigo-400 dark:bg-indigo-500/15 dark:text-indigo-300'
                  : 'border-zinc-200 bg-white text-zinc-600 hover:bg-zinc-50 dark:border-zinc-800 dark:bg-zinc-950 dark:text-zinc-300 dark:hover:bg-zinc-900'
              }`}
              title={root}
            >
              {root}
            </button>
          );
        })}
      </div>

      {/* Toolbar: Parent + Breadcrumbs + Refresh */}
      <div className="flex items-center gap-1.5 border-b border-zinc-200 px-2.5 py-1.5 dark:border-zinc-800">
        <button
          type="button"
          onClick={() => {
            if (listing?.parentPath) {
              setFilter('');
              if (listing.parentPath === '__drives__') {
                void loadDirectory('__drives__');
              } else {
                onSelectPath(listing.parentPath);
              }
            }
          }}
          disabled={!listing?.parentPath || loading}
          title="Dossier parent"
          className="flex h-7 w-7 shrink-0 items-center justify-center rounded-md border border-zinc-200 bg-white text-zinc-600 transition hover:bg-zinc-100 disabled:opacity-40 dark:border-zinc-800 dark:bg-zinc-900 dark:text-zinc-300 dark:hover:bg-zinc-800"
        >
          <ArrowUp className="h-3.5 w-3.5" />
        </button>

        <div className="flex min-w-0 flex-1 items-center gap-0.5 overflow-x-auto py-0.5 font-mono text-[11px]">
          {listing?.currentPath === '__drives__' ? (
            <span className="px-1 font-medium text-zinc-700 dark:text-zinc-200">
              Disques Windows
            </span>
          ) : breadcrumbs.length > 0 ? (
            breadcrumbs.map((seg, idx) => (
              <span key={seg.path} className="flex shrink-0 items-center gap-0.5">
                {idx > 0 && <ChevronRight className="h-3 w-3 text-zinc-400" />}
                <button
                  type="button"
                  onClick={() => {
                    setFilter('');
                    onSelectPath(seg.path);
                  }}
                  className="rounded px-1 py-0.5 text-zinc-700 hover:bg-zinc-200/70 dark:text-zinc-300 dark:hover:bg-zinc-800"
                >
                  {seg.label}
                </button>
              </span>
            ))
          ) : (
            <span className="px-1 text-zinc-400">Racine</span>
          )}
        </div>

        <button
          type="button"
          onClick={() => void loadDirectory(listing?.currentPath || currentPath)}
          disabled={loading}
          title="Actualiser le dossier"
          className="flex h-7 w-7 shrink-0 items-center justify-center rounded-md text-zinc-500 hover:bg-zinc-200/70 hover:text-zinc-900 dark:text-zinc-400 dark:hover:bg-zinc-800 dark:hover:text-zinc-100"
        >
          <RefreshCw className={`h-3.5 w-3.5 ${loading ? 'animate-spin' : ''}`} />
        </button>
      </div>

      {/* Instant Filter */}
      <div className="relative border-b border-zinc-200 px-2.5 py-1.5 dark:border-zinc-800">
        <Search className="pointer-events-none absolute top-1/2 left-4 h-3.5 w-3.5 -translate-y-1/2 text-zinc-400" />
        <input
          type="text"
          value={filter}
          onChange={(e) => setFilter(e.target.value)}
          placeholder="Filtrer les sous-dossiers…"
          className="w-full rounded-md border border-zinc-200 bg-white py-1 pr-2.5 pl-7 text-xs text-zinc-900 outline-none focus:border-indigo-500 dark:border-zinc-800 dark:bg-zinc-900 dark:text-zinc-100"
        />
      </div>

      {/* Entries */}
      <div className="max-h-48 overflow-y-auto">
        {loading && !listing ? (
          <div className="flex items-center justify-center gap-2 py-6 text-xs text-zinc-500">
            <Loader2 className="h-3.5 w-3.5 animate-spin" />
            <span>Chargement des dossiers…</span>
          </div>
        ) : error ? (
          <div className="p-3 text-xs text-red-600 dark:text-red-400">{error}</div>
        ) : filteredEntries.length === 0 ? (
          <div className="py-6 text-center text-xs text-zinc-400 dark:text-zinc-500">
            {filter ? 'Aucun sous-dossier correspondant' : 'Aucun sous-dossier ici'}
          </div>
        ) : (
          <ul className="divide-y divide-zinc-200/70 dark:divide-zinc-800/70">
            {filteredEntries.map((entry) => (
              <li key={entry.path}>
                <button
                  type="button"
                  onClick={() => {
                    setFilter('');
                    onSelectPath(entry.path);
                  }}
                  className="flex w-full items-center gap-2 px-3 py-2 text-left text-xs text-zinc-800 transition hover:bg-white dark:text-zinc-200 dark:hover:bg-zinc-900"
                >
                  <Folder className="h-3.5 w-3.5 shrink-0 text-indigo-500 dark:text-indigo-400" />
                  <span className="truncate font-mono">{entry.name}</span>
                </button>
              </li>
            ))}
          </ul>
        )}
      </div>
    </div>
  );
}
