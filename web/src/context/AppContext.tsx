import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useState,
  type ReactNode,
} from 'react';
import { AgentPilotClient, ApiError } from '../api/client';
import type { AgentManifest, Machine, Project, SessionMeta, ThemeMode } from '../api/types';

const STORAGE_TOKEN_KEY = 'agentpilot.token';
const STORAGE_BASE_URL_KEY = 'agentpilot.baseUrl';
const STORAGE_THEME_KEY = 'agentpilot.theme';

interface AppContextValue {
  token: string;
  baseUrl: string;
  isAuthenticated: boolean;
  client: AgentPilotClient | null;
  themeMode: ThemeMode;
  setThemeMode: (mode: ThemeMode) => void;
  login: (token: string, baseUrl?: string) => void;
  logout: () => void;
  machines: Machine[];
  projects: Project[];
  agents: AgentManifest[];
  sessions: SessionMeta[];
  loading: boolean;
  error: string | null;
  refreshAll: () => Promise<void>;
  refreshProjects: () => Promise<void>;
  refreshSessions: () => Promise<void>;
  refreshAgents: () => Promise<void>;
}

const AppContext = createContext<AppContextValue | null>(null);

function readStorage(key: string): string {
  try {
    return window.localStorage.getItem(key) ?? '';
  } catch {
    return '';
  }
}

function writeStorage(key: string, value: string): void {
  try {
    if (value) {
      window.localStorage.setItem(key, value);
    } else {
      window.localStorage.removeItem(key);
    }
  } catch {
    // Ignore storage quota or private mode errors.
  }
}

function readInitialTheme(): ThemeMode {
  const saved = readStorage(STORAGE_THEME_KEY);
  if (saved === 'light' || saved === 'dark' || saved === 'system') {
    return saved;
  }
  return 'system';
}

export function AppProvider({ children }: { children: ReactNode }) {
  const [token, setToken] = useState<string>(() => readStorage(STORAGE_TOKEN_KEY));
  const [baseUrl, setBaseUrl] = useState<string>(() => readStorage(STORAGE_BASE_URL_KEY));
  const [themeMode, setThemeModeState] = useState<ThemeMode>(readInitialTheme);

  const [machines, setMachines] = useState<Machine[]>([]);
  const [projects, setProjects] = useState<Project[]>([]);
  const [agents, setAgents] = useState<AgentManifest[]>([]);
  const [sessions, setSessions] = useState<SessionMeta[]>([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const client = useMemo(
    () => (token.trim() ? new AgentPilotClient(token, baseUrl) : null),
    [token, baseUrl],
  );

  const setThemeMode = useCallback((mode: ThemeMode) => {
    setThemeModeState(mode);
    writeStorage(STORAGE_THEME_KEY, mode);
  }, []);

  useEffect(() => {
    const root = document.documentElement;
    const media = window.matchMedia?.('(prefers-color-scheme: dark)');

    const apply = () => {
      const dark =
        themeMode === 'dark' || (themeMode === 'system' && Boolean(media?.matches));
      root.classList.toggle('dark', dark);
    };

    apply();
    if (!media) return;
    media.addEventListener('change', apply);
    return () => media.removeEventListener('change', apply);
  }, [themeMode]);

  const logout = useCallback(() => {
    setToken('');
    writeStorage(STORAGE_TOKEN_KEY, '');
    setMachines([]);
    setProjects([]);
    setAgents([]);
    setSessions([]);
    setError(null);
  }, []);

  const login = useCallback((nextToken: string, nextBaseUrl = '') => {
    const cleanToken = nextToken.trim();
    const cleanBase = nextBaseUrl.trim().replace(/\/+$/, '');
    setToken(cleanToken);
    setBaseUrl(cleanBase);
    writeStorage(STORAGE_TOKEN_KEY, cleanToken);
    writeStorage(STORAGE_BASE_URL_KEY, cleanBase);
    setError(null);
  }, []);

  const handleAuthError = useCallback(
    (err: unknown) => {
      if (err instanceof ApiError && err.status === 401) {
        setError('Session expirée ou token invalide (401). Veuillez vous reconnecter.');
        logout();
        return true;
      }
      return false;
    },
    [logout],
  );

  const refreshAll = useCallback(async () => {
    if (!client) return;
    setLoading(true);
    setError(null);
    try {
      const [m, p, a, s] = await Promise.all([
        client.fetchMachines(),
        client.fetchProjects(),
        client.fetchAgents(),
        client.fetchSessions(),
      ]);
      setMachines(m);
      setProjects(p);
      setAgents(a);
      setSessions(s);
    } catch (err) {
      if (!handleAuthError(err)) {
        setError(err instanceof Error ? err.message : 'Erreur de chargement');
      }
    } finally {
      setLoading(false);
    }
  }, [client, handleAuthError]);

  const refreshProjects = useCallback(async () => {
    if (!client) return;
    try {
      const p = await client.fetchProjects();
      setProjects(p);
    } catch (err) {
      handleAuthError(err);
    }
  }, [client, handleAuthError]);

  const refreshSessions = useCallback(async () => {
    if (!client) return;
    try {
      const s = await client.fetchSessions();
      setSessions(s);
    } catch (err) {
      handleAuthError(err);
    }
  }, [client, handleAuthError]);

  const refreshAgents = useCallback(async () => {
    if (!client) return;
    try {
      const a = await client.fetchAgents();
      setAgents(a);
    } catch (err) {
      handleAuthError(err);
    }
  }, [client, handleAuthError]);

  useEffect(() => {
    if (client) {
      void refreshAll();
    }
  }, [client, refreshAll]);

  const value = useMemo<AppContextValue>(
    () => ({
      token,
      baseUrl,
      isAuthenticated: Boolean(client),
      client,
      themeMode,
      setThemeMode,
      login,
      logout,
      machines,
      projects,
      agents,
      sessions,
      loading,
      error,
      refreshAll,
      refreshProjects,
      refreshSessions,
      refreshAgents,
    }),
    [
      token,
      baseUrl,
      client,
      themeMode,
      setThemeMode,
      login,
      logout,
      machines,
      projects,
      agents,
      sessions,
      loading,
      error,
      refreshAll,
      refreshProjects,
      refreshSessions,
      refreshAgents,
    ],
  );

  return <AppContext.Provider value={value}>{children}</AppContext.Provider>;
}

export function useApp(): AppContextValue {
  const ctx = useContext(AppContext);
  if (!ctx) {
    throw new Error('useApp must be used within AppProvider');
  }
  return ctx;
}
