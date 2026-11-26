import { useState, type FormEvent } from 'react';
import { CheckCircle2, ChevronDown, ChevronRight, KeyRound, Loader2, Server } from 'lucide-react';
import { AgentPilotClient } from '../api/client';
import { useApp } from '../context/AppContext';

export function LoginView() {
  const { baseUrl: savedBaseUrl, error: appError, login } = useApp();
  const [token, setToken] = useState('');
  const [baseUrl, setBaseUrl] = useState(savedBaseUrl);
  const [showAdvanced, setShowAdvanced] = useState(Boolean(savedBaseUrl));
  const [testing, setTesting] = useState(false);
  const [statusMessage, setStatusMessage] = useState<{
    kind: 'ok' | 'error';
    text: string;
  } | null>(null);

  async function handleTestConnection() {
    if (!token.trim()) {
      setStatusMessage({ kind: 'error', text: 'Veuillez saisir un Bearer token.' });
      return;
    }
    setTesting(true);
    setStatusMessage(null);
    try {
      const probe = new AgentPilotClient(token, baseUrl);
      const machines = await probe.fetchMachines();
      const online = machines.filter((m) => m.online).length;
      setStatusMessage({
        kind: 'ok',
        text: `Connexion réussie · ${machines.length} machine(s) (${online} en ligne)`,
      });
    } catch (err) {
      setStatusMessage({
        kind: 'error',
        text: err instanceof Error ? err.message : 'Échec de connexion au serveur',
      });
    } finally {
      setTesting(false);
    }
  }

  async function handleSubmit(e: FormEvent) {
    e.preventDefault();
    if (!token.trim()) {
      setStatusMessage({ kind: 'error', text: 'Veuillez saisir un Bearer token.' });
      return;
    }
    setTesting(true);
    setStatusMessage(null);
    try {
      const probe = new AgentPilotClient(token, baseUrl);
      await probe.fetchMachines();
      login(token, baseUrl);
    } catch (err) {
      setStatusMessage({
        kind: 'error',
        text: err instanceof Error ? err.message : 'Authentification refusée',
      });
    } finally {
      setTesting(false);
    }
  }

  return (
    <div className="flex min-h-dvh items-center justify-center bg-zinc-50 px-4 py-10 dark:bg-zinc-950">
      <div className="w-full max-w-md rounded-xl border border-zinc-200 bg-white p-6 shadow-xs dark:border-zinc-800 dark:bg-zinc-900/70">
        <div className="mb-6 flex items-center gap-3">
          <div className="flex h-9 w-9 items-center justify-center rounded-lg bg-indigo-600 text-sm font-semibold text-white dark:bg-indigo-500">
            AP
          </div>
          <div>
            <h1 className="text-base font-semibold tracking-tight text-zinc-900 dark:text-zinc-100">
              AgentPilot
            </h1>
            <p className="text-xs text-zinc-500 dark:text-zinc-400">
              Accès authentifié à vos agents CLI homelab
            </p>
          </div>
        </div>

        {appError && (
          <div className="mb-4 rounded-lg border border-red-200 bg-red-50 px-3 py-2 text-xs text-red-700 dark:border-red-900/60 dark:bg-red-950/40 dark:text-red-300">
            {appError}
          </div>
        )}

        <form onSubmit={handleSubmit} className="space-y-4">
          <div>
            <label
              htmlFor="ap-token"
              className="mb-1.5 flex items-center gap-1.5 text-xs font-medium text-zinc-700 dark:text-zinc-300"
            >
              <KeyRound className="h-3.5 w-3.5 text-zinc-400" />
              <span>Bearer token (scope mobile)</span>
            </label>
            <input
              id="ap-token"
              type="password"
              autoComplete="current-password"
              value={token}
              onChange={(e) => setToken(e.target.value)}
              placeholder="Collez votre token mobile…"
              className="w-full rounded-lg border border-zinc-200 bg-white px-3 py-2 font-mono text-sm text-zinc-900 outline-none transition focus:border-indigo-500 focus:ring-2 focus:ring-indigo-500/20 dark:border-zinc-800 dark:bg-zinc-950 dark:text-zinc-100 dark:focus:border-indigo-400"
            />
          </div>

          <div className="rounded-lg border border-zinc-200/80 dark:border-zinc-800/80">
            <button
              type="button"
              onClick={() => setShowAdvanced((v) => !v)}
              className="flex w-full items-center justify-between px-3 py-2 text-xs font-medium text-zinc-600 hover:text-zinc-900 dark:text-zinc-400 dark:hover:text-zinc-200"
            >
              <span className="flex items-center gap-1.5">
                <Server className="h-3.5 w-3.5" />
                <span>URL du serveur</span>
              </span>
              <span className="flex items-center gap-1 font-mono text-[11px] text-zinc-400">
                {baseUrl ? baseUrl : 'même origine'}
                {showAdvanced ? (
                  <ChevronDown className="h-3.5 w-3.5" />
                ) : (
                  <ChevronRight className="h-3.5 w-3.5" />
                )}
              </span>
            </button>
            {showAdvanced && (
              <div className="border-t border-zinc-200/80 p-3 dark:border-zinc-800/80">
                <input
                  id="ap-server-url"
                  type="url"
                  value={baseUrl}
                  onChange={(e) => setBaseUrl(e.target.value)}
                  placeholder="Laisser vide pour même origine (ex: http://100.x.y.z:8088)"
                  className="w-full rounded-md border border-zinc-200 bg-zinc-50 px-2.5 py-1.5 font-mono text-xs text-zinc-900 outline-none focus:border-indigo-500 dark:border-zinc-800 dark:bg-zinc-950 dark:text-zinc-100"
                />
                <p className="mt-1.5 text-[11px] text-zinc-500 dark:text-zinc-400">
                  Par défaut, la SPA appelle le serveur AgentPilot qui l&apos;héberge.
                </p>
              </div>
            )}
          </div>

          {statusMessage && (
            <div
              className={`flex items-center gap-2 rounded-lg border px-3 py-2 text-xs ${
                statusMessage.kind === 'ok'
                  ? 'border-emerald-200 bg-emerald-50 text-emerald-700 dark:border-emerald-900/60 dark:bg-emerald-950/40 dark:text-emerald-300'
                  : 'border-red-200 bg-red-50 text-red-700 dark:border-red-900/60 dark:bg-red-950/40 dark:text-red-300'
              }`}
            >
              {statusMessage.kind === 'ok' && <CheckCircle2 className="h-3.5 w-3.5 shrink-0" />}
              <span>{statusMessage.text}</span>
            </div>
          )}

          <div className="flex flex-col gap-2 pt-1 sm:flex-row">
            <button
              type="button"
              onClick={handleTestConnection}
              disabled={testing}
              className="flex min-h-[42px] flex-1 items-center justify-center gap-2 rounded-lg border border-zinc-200 bg-white px-3 py-2 text-xs font-medium text-zinc-700 transition hover:bg-zinc-50 disabled:opacity-50 dark:border-zinc-800 dark:bg-zinc-950 dark:text-zinc-300 dark:hover:bg-zinc-900"
            >
              {testing && <Loader2 className="h-3.5 w-3.5 animate-spin" />}
              <span>Tester la connexion</span>
            </button>
            <button
              type="submit"
              disabled={testing}
              className="flex min-h-[42px] flex-1 items-center justify-center gap-2 rounded-lg bg-indigo-600 px-3 py-2 text-xs font-medium text-white transition hover:bg-indigo-700 disabled:opacity-50 dark:bg-indigo-500 dark:hover:bg-indigo-400"
            >
              {testing && <Loader2 className="h-3.5 w-3.5 animate-spin" />}
              <span>Se connecter</span>
            </button>
          </div>
        </form>
      </div>
    </div>
  );
}
