import type { ReactNode } from 'react';
import {
  FolderKanban,
  MessageSquare,
  Monitor,
  Moon,
  Plus,
  RefreshCw,
  Settings,
  Sparkles,
  Sun,
  Terminal,
} from 'lucide-react';
import type { AgentManifest, Machine, Project, SessionMeta, ThemeMode } from '../api/types';

export type NavTab = 'projects' | 'sessions' | 'oneshot' | 'settings';

interface AppShellProps {
  activeTab: NavTab;
  onSelectTab: (tab: NavTab) => void;
  runningSessions: SessionMeta[];
  projects?: Project[];
  agents?: AgentManifest[];
  machines?: Machine[];
  activeSessionId?: string | null;
  onOpenSession: (session: SessionMeta) => void;
  onNewSession?: () => void;
  themeMode: ThemeMode;
  onSetTheme?: (mode: ThemeMode) => void;
  onCycleTheme: () => void;
  onRefresh: () => void;
  refreshing: boolean;
  headerTitle?: string;
  headerActions?: ReactNode;
  fullBleed?: boolean;
  children: ReactNode;
}

const NAV_ITEMS: { id: NavTab; label: string; icon: typeof FolderKanban }[] = [
  { id: 'projects', label: 'Projets', icon: FolderKanban },
  { id: 'sessions', label: 'Sessions', icon: MessageSquare },
  { id: 'oneshot', label: 'One-Shot', icon: Terminal },
  { id: 'settings', label: 'Réglages', icon: Settings },
];

const THEME_MODES: { id: ThemeMode; label: string; icon: typeof Sun }[] = [
  { id: 'light', label: 'Clair', icon: Sun },
  { id: 'system', label: 'Système', icon: Monitor },
  { id: 'dark', label: 'Sombre', icon: Moon },
];

function ThemeIcon({ mode }: { mode: ThemeMode }) {
  if (mode === 'dark') return <Moon className="h-4 w-4" />;
  if (mode === 'light') return <Sun className="h-4 w-4" />;
  return <Monitor className="h-4 w-4" />;
}

function formatShortTime(iso: string): string {
  try {
    return new Date(iso).toLocaleTimeString('fr-FR', {
      hour: '2-digit',
      minute: '2-digit',
    });
  } catch {
    return '';
  }
}

export function AppShell({
  activeTab,
  onSelectTab,
  runningSessions,
  projects = [],
  agents = [],
  machines = [],
  activeSessionId,
  onOpenSession,
  onNewSession,
  themeMode,
  onSetTheme,
  onCycleTheme,
  onRefresh,
  refreshing,
  headerTitle,
  headerActions,
  fullBleed = false,
  children,
}: AppShellProps) {
  const projectMap = new Map(projects.map((p) => [p.id, p]));
  const agentMap = new Map(agents.map((a) => [a.agentId, a]));
  const onlineMachinesCount = machines.filter((m) => m.online).length;

  return (
    <div className="flex h-dvh w-full overflow-hidden bg-white text-zinc-900 dark:bg-[#09090B] dark:text-zinc-100">
      {/* Desktop Sidebar */}
      <aside className="hidden md:flex md:w-[260px] md:shrink-0 md:flex-col md:border-r md:border-zinc-200/80 md:bg-[#F7F7F8] dark:md:border-zinc-800/80 dark:md:bg-[#0E0E11]">
        {/* Brand Header */}
        <div className="flex h-14 shrink-0 items-center justify-between px-4">
          <button
            type="button"
            onClick={() => onSelectTab('projects')}
            className="flex items-center gap-2.5 rounded-lg py-1 pr-2 text-left transition hover:opacity-85"
          >
            <span className="flex h-7 w-7 items-center justify-center rounded-lg bg-zinc-900 text-white shadow-xs dark:bg-zinc-100 dark:text-zinc-950">
              <Sparkles className="h-3.5 w-3.5" />
            </span>
            <div className="leading-tight">
              <span className="block text-[13px] font-semibold tracking-tight text-zinc-900 dark:text-zinc-100">
                AgentPilot
              </span>
              <span className="block text-[10px] font-medium text-zinc-400 dark:text-zinc-500">
                Workspace
              </span>
            </div>
          </button>
          <button
            type="button"
            onClick={onRefresh}
            title="Actualiser"
            aria-label="Actualiser"
            className="flex h-7 w-7 items-center justify-center rounded-md text-zinc-400 transition-colors hover:bg-zinc-200/70 hover:text-zinc-800 dark:text-zinc-500 dark:hover:bg-zinc-800 dark:hover:text-zinc-200"
          >
            <RefreshCw className={`h-3.5 w-3.5 ${refreshing ? 'animate-spin' : ''}`} />
          </button>
        </div>

        {/* Primary Action */}
        {onNewSession && (
          <div className="px-3 pb-2">
            <button
              type="button"
              onClick={onNewSession}
              className="flex w-full items-center justify-center gap-2 rounded-lg border border-zinc-200/90 bg-white px-3 py-2 text-xs font-medium text-zinc-800 shadow-[0_1px_2px_rgba(0,0,0,0.04)] transition hover:border-zinc-300 hover:bg-zinc-50 dark:border-zinc-800 dark:bg-zinc-900 dark:text-zinc-100 dark:hover:border-zinc-700 dark:hover:bg-zinc-800/80"
            >
              <Plus className="h-3.5 w-3.5 text-indigo-600 dark:text-indigo-400" />
              <span>Nouvelle session</span>
            </button>
          </div>
        )}

        {/* Navigation + Running Sessions */}
        <nav className="flex-1 overflow-y-auto px-3 py-2" aria-label="Navigation principale">
          <div className="space-y-0.5">
            {NAV_ITEMS.map((item) => {
              const Icon = item.icon;
              const active = activeTab === item.id && !activeSessionId;
              return (
                <button
                  key={item.id}
                  type="button"
                  onClick={() => onSelectTab(item.id)}
                  className={`flex w-full items-center gap-2.5 rounded-lg px-2.5 py-2 text-[13px] font-medium transition-all ${
                    active
                      ? 'bg-white text-zinc-950 shadow-[0_1px_2px_rgba(0,0,0,0.05)] ring-1 ring-zinc-200/80 dark:bg-zinc-800/90 dark:text-zinc-50 dark:ring-zinc-700/70'
                      : 'text-zinc-600 hover:bg-zinc-200/50 hover:text-zinc-900 dark:text-zinc-400 dark:hover:bg-zinc-800/50 dark:hover:text-zinc-100'
                  }`}
                >
                  <Icon
                    className={`h-4 w-4 shrink-0 ${
                      active
                        ? 'text-indigo-600 dark:text-indigo-400'
                        : 'text-zinc-400 dark:text-zinc-500'
                    }`}
                  />
                  <span className="truncate">{item.label}</span>
                  {item.id === 'sessions' && runningSessions.length > 0 && (
                    <span className="ml-auto rounded-full bg-emerald-500/12 px-1.5 py-0.5 font-mono text-[10px] font-medium text-emerald-700 dark:bg-emerald-500/15 dark:text-emerald-400">
                      {runningSessions.length}
                    </span>
                  )}
                </button>
              );
            })}
          </div>

          {runningSessions.length > 0 && (
            <div className="mt-6">
              <div className="mb-2 flex items-center justify-between px-2.5">
                <span className="text-[11px] font-semibold uppercase tracking-wider text-zinc-400 dark:text-zinc-500">
                  Sessions actives
                </span>
                <span className="font-mono text-[10px] text-zinc-400 dark:text-zinc-500">
                  {runningSessions.length}
                </span>
              </div>
              <div className="space-y-1">
                {runningSessions.slice(0, 8).map((s) => {
                  const selected = activeSessionId === s.id;
                  const proj = projectMap.get(s.projectId);
                  const agent = agentMap.get(s.agentId);
                  const title = proj?.name ?? s.cwd.split(/[\\/]/).filter(Boolean).pop() ?? s.agentId;
                  const subtitle = `${agent?.name ?? s.agentId} · ${formatShortTime(s.createdAt)}`;
                  return (
                    <button
                      key={s.id}
                      type="button"
                      onClick={() => onOpenSession(s)}
                      className={`group flex w-full items-start gap-2.5 rounded-lg px-2.5 py-2 text-left transition-all ${
                        selected
                          ? 'bg-white text-zinc-950 shadow-[0_1px_2px_rgba(0,0,0,0.05)] ring-1 ring-zinc-200/80 dark:bg-zinc-800/90 dark:text-zinc-50 dark:ring-zinc-700/70'
                          : 'text-zinc-600 hover:bg-zinc-200/50 hover:text-zinc-900 dark:text-zinc-400 dark:hover:bg-zinc-800/50 dark:hover:text-zinc-100'
                      }`}
                    >
                      <span
                        className={`mt-1.5 h-1.5 w-1.5 shrink-0 rounded-full ${
                          s.waitingApproval
                            ? 'bg-amber-500'
                            : 'bg-emerald-500'
                        }`}
                      />
                      <div className="min-w-0 flex-1">
                        <div className="truncate text-[12px] font-medium leading-snug">
                          {title}
                        </div>
                        <div className="mt-0.5 truncate font-mono text-[10px] text-zinc-400 dark:text-zinc-500">
                          {subtitle}
                        </div>
                      </div>
                    </button>
                  );
                })}
              </div>
            </div>
          )}
        </nav>

        {/* Sidebar Footer: Machine Status + Segmented Theme Switch */}
        <div className="shrink-0 space-y-2.5 border-t border-zinc-200/80 p-3 dark:border-zinc-800/80">
          {machines.length > 0 && (
            <div className="flex items-center justify-between px-1.5 text-[11px] text-zinc-500 dark:text-zinc-400">
              <span className="flex items-center gap-1.5">
                <span
                  className={`h-1.5 w-1.5 rounded-full ${
                    onlineMachinesCount > 0 ? 'bg-emerald-500' : 'bg-zinc-400'
                  }`}
                />
                <span>
                  {onlineMachinesCount > 0
                    ? `${onlineMachinesCount} machine${onlineMachinesCount > 1 ? 's' : ''} en ligne`
                    : 'Hors ligne'}
                </span>
              </span>
              <span className="font-mono text-[10px] text-zinc-400 dark:text-zinc-500">
                {machines[0]?.name}
              </span>
            </div>
          )}

          {onSetTheme ? (
            <div
              role="group"
              aria-label="Sélecteur de thème"
              className="grid grid-cols-3 gap-1 rounded-lg bg-zinc-200/60 p-0.5 dark:bg-zinc-900"
            >
              {THEME_MODES.map((opt) => {
                const Icon = opt.icon;
                const active = themeMode === opt.id;
                return (
                  <button
                    key={opt.id}
                    type="button"
                    onClick={() => onSetTheme(opt.id)}
                    title={opt.label}
                    aria-label={opt.label}
                    className={`flex items-center justify-center gap-1 rounded-md py-1.5 text-[11px] font-medium transition ${
                      active
                        ? 'bg-white text-zinc-900 shadow-xs dark:bg-zinc-800 dark:text-zinc-100'
                        : 'text-zinc-500 hover:text-zinc-800 dark:text-zinc-400 dark:hover:text-zinc-200'
                    }`}
                  >
                    <Icon className="h-3.5 w-3.5" />
                  </button>
                );
              })}
            </div>
          ) : (
            <button
              type="button"
              onClick={onCycleTheme}
              className="flex w-full items-center justify-between rounded-lg border border-zinc-200 bg-white px-2.5 py-1.5 text-xs font-medium text-zinc-600 transition hover:text-zinc-900 dark:border-zinc-800 dark:bg-zinc-900 dark:text-zinc-400 dark:hover:text-zinc-100"
            >
              <span className="flex items-center gap-2">
                <ThemeIcon mode={themeMode} />
                <span>Thème</span>
              </span>
            </button>
          )}
        </div>
      </aside>

      {/* Main Content Area */}
      <div className="flex min-w-0 flex-1 flex-col overflow-hidden bg-white dark:bg-[#09090B]">
        {/* Mobile Sticky Header (hidden on desktop, and hidden in fullBleed chat on mobile since ChatView has its own header) */}
        {!fullBleed && (
          <header className="sticky top-0 z-20 flex h-13 shrink-0 items-center justify-between border-b border-zinc-200/80 bg-white/90 px-4 backdrop-blur md:hidden dark:border-zinc-800/80 dark:bg-[#09090B]/90">
            <div className="flex min-w-0 items-center gap-2">
              <span className="flex h-6 w-6 shrink-0 items-center justify-center rounded-md bg-zinc-900 text-white dark:bg-zinc-100 dark:text-zinc-950">
                <Sparkles className="h-3 w-3" />
              </span>
              <span className="truncate text-sm font-semibold tracking-tight">
                {headerTitle ?? 'AgentPilot'}
              </span>
            </div>
            <div className="flex items-center gap-1">
              {headerActions}
              <button
                type="button"
                onClick={onCycleTheme}
                aria-label="Changer le thème"
                className="flex h-9 w-9 items-center justify-center rounded-lg text-zinc-500 hover:bg-zinc-100 hover:text-zinc-900 dark:text-zinc-400 dark:hover:bg-zinc-900 dark:hover:text-zinc-100"
              >
                <ThemeIcon mode={themeMode} />
              </button>
              <button
                type="button"
                onClick={onRefresh}
                aria-label="Actualiser"
                className="flex h-9 w-9 items-center justify-center rounded-lg text-zinc-500 hover:bg-zinc-100 hover:text-zinc-900 dark:text-zinc-400 dark:hover:bg-zinc-900 dark:hover:text-zinc-100"
              >
                <RefreshCw className={`h-4 w-4 ${refreshing ? 'animate-spin' : ''}`} />
              </button>
            </div>
          </header>
        )}

        {fullBleed ? (
          <main className="flex min-h-0 min-w-0 flex-1 flex-col overflow-hidden">
            {children}
          </main>
        ) : (
          <main className="min-h-0 flex-1 overflow-y-auto bg-zinc-50/50 pb-20 md:pb-8 dark:bg-[#09090B]">
            <div className="mx-auto w-full max-w-6xl px-4 py-6 md:px-10 md:py-8">
              {children}
            </div>
          </main>
        )}

        {/* Mobile Bottom Tab Bar (hidden when inside active fullBleed chat so composer sits at bottom cleanly) */}
        {!fullBleed && (
          <nav
            aria-label="Navigation mobile"
            className="safe-bottom fixed inset-x-0 bottom-0 z-20 grid grid-cols-4 border-t border-zinc-200/80 bg-white/95 backdrop-blur md:hidden dark:border-zinc-800/80 dark:bg-[#09090B]/95"
          >
            {NAV_ITEMS.map((item) => {
              const Icon = item.icon;
              const active = activeTab === item.id && !activeSessionId;
              return (
                <button
                  key={item.id}
                  type="button"
                  onClick={() => onSelectTab(item.id)}
                  className={`flex min-h-[52px] flex-col items-center justify-center gap-1 px-1 py-1.5 text-[11px] font-medium transition-colors ${
                    active
                      ? 'text-indigo-600 dark:text-indigo-400'
                      : 'text-zinc-500 hover:text-zinc-900 dark:text-zinc-400 dark:hover:text-zinc-100'
                  }`}
                >
                  <Icon className="h-4 w-4" />
                  <span>{item.label}</span>
                </button>
              );
            })}
          </nav>
        )}
      </div>
    </div>
  );
}
