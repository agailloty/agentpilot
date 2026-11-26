import { useCallback, useEffect, useState, type FormEvent } from 'react';
import {
  Bell,
  Check,
  CheckCircle2,
  Copy,
  KeyRound,
  Loader2,
  LogOut,
  Monitor,
  Moon,
  Plus,
  Server,
  Sun,
  Trash2,
  Wrench,
  X,
  XCircle,
} from 'lucide-react';
import { useApp } from '../context/AppContext';
import type { CreatedToken, NotificationConfig, ThemeMode, TokenRecord } from '../api/types';

export function SettingsView() {
  const {
    client,
    baseUrl,
    themeMode,
    setThemeMode,
    logout,
    machines,
    agents,
    refreshAgents,
  } = useApp();

  const [notifConfig, setNotifConfig] = useState<NotificationConfig | null>(null);
  const [tokens, setTokens] = useState<TokenRecord[]>([]);
  const [loadingExtra, setLoadingExtra] = useState(false);

  const [newTokenName, setNewTokenName] = useState('');
  const [newTokenScope, setNewTokenScope] = useState<'mobile' | 'runner'>('mobile');
  const [creatingToken, setCreatingToken] = useState(false);
  const [tokenError, setTokenError] = useState<string | null>(null);
  const [createdToken, setCreatedToken] = useState<CreatedToken | null>(null);
  const [copiedSecret, setCopiedSecret] = useState(false);
  const [revokingId, setRevokingId] = useState<string | null>(null);

  const loadSettingsData = useCallback(async () => {
    if (!client) return;
    setLoadingExtra(true);
    try {
      const [notif, tokList] = await Promise.all([
        client.fetchNotificationConfig(),
        client.fetchTokens(),
      ]);
      setNotifConfig(notif);
      setTokens(tokList);
    } catch {
      // Ignore transient errors; sections show fallback states.
    } finally {
      setLoadingExtra(false);
    }
  }, [client]);

  useEffect(() => {
    void loadSettingsData();
  }, [loadSettingsData]);

  async function handleCreateToken(e: FormEvent) {
    e.preventDefault();
    if (!client) return;
    if (!newTokenName.trim()) {
      setTokenError('Le nom du token est requis.');
      return;
    }
    setCreatingToken(true);
    setTokenError(null);
    try {
      const created = await client.createToken(newTokenName.trim(), newTokenScope);
      setCreatedToken(created);
      setNewTokenName('');
      await loadSettingsData();
    } catch (err) {
      setTokenError(err instanceof Error ? err.message : 'Impossible de créer le token');
    } finally {
      setCreatingToken(false);
    }
  }

  async function handleRevokeToken(id: string) {
    if (!client) return;
    setRevokingId(id);
    setTokenError(null);
    try {
      await client.revokeToken(id);
      await loadSettingsData();
    } catch (err) {
      setTokenError(err instanceof Error ? err.message : 'Impossible de révoquer ce token');
    } finally {
      setRevokingId(null);
    }
  }

  async function handleCopyCreatedToken() {
    if (!createdToken) return;
    try {
      await navigator.clipboard.writeText(createdToken.token);
      setCopiedSecret(true);
      window.setTimeout(() => setCopiedSecret(false), 1800);
    } catch {
      // Clipboard unavailable.
    }
  }

  const themeOptions: { id: ThemeMode; label: string; icon: typeof Sun }[] = [
    { id: 'system', label: 'Système', icon: Monitor },
    { id: 'light', label: 'Clair', icon: Sun },
    { id: 'dark', label: 'Sombre', icon: Moon },
  ];

  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-xl font-semibold tracking-tight text-zinc-950 dark:text-zinc-50">
          Réglages
        </h1>
        <p className="mt-0.5 text-xs text-zinc-500 dark:text-zinc-400">
          Apparence, connexion serveur, machines, outils CLI détectés et gestion des tokens.
        </p>
      </div>

      {/* Appearance */}
      <section className="rounded-xl border border-zinc-200 bg-white p-4 dark:border-zinc-800 dark:bg-zinc-900/70">
        <h2 className="text-sm font-semibold text-zinc-900 dark:text-zinc-100">Apparence</h2>
        <p className="mt-0.5 text-xs text-zinc-500 dark:text-zinc-400">
          Choisissez le thème de l&apos;interface.
        </p>
        <div className="mt-3 grid grid-cols-3 gap-2">
          {themeOptions.map((opt) => {
            const Icon = opt.icon;
            const active = themeMode === opt.id;
            return (
              <button
                key={opt.id}
                type="button"
                onClick={() => setThemeMode(opt.id)}
                className={`flex min-h-[40px] items-center justify-center gap-2 rounded-lg border px-3 py-2 text-xs font-medium transition ${
                  active
                    ? 'border-indigo-500 bg-indigo-50 text-indigo-700 dark:border-indigo-400 dark:bg-indigo-500/15 dark:text-indigo-300'
                    : 'border-zinc-200 bg-white text-zinc-600 hover:bg-zinc-50 dark:border-zinc-800 dark:bg-zinc-950 dark:text-zinc-400 dark:hover:bg-zinc-900'
                }`}
              >
                <Icon className="h-3.5 w-3.5" />
                <span>{opt.label}</span>
              </button>
            );
          })}
        </div>
      </section>

      {/* Connection & Logout */}
      <section className="flex flex-wrap items-center justify-between gap-3 rounded-xl border border-zinc-200 bg-white p-4 dark:border-zinc-800 dark:bg-zinc-900/70">
        <div>
          <h2 className="flex items-center gap-1.5 text-sm font-semibold text-zinc-900 dark:text-zinc-100">
            <Server className="h-4 w-4 text-zinc-400" />
            <span>Serveur & Session</span>
          </h2>
          <p className="mt-1 font-mono text-xs text-zinc-500 dark:text-zinc-400">
            {baseUrl || 'Même origine (hébergé par le backend Go)'}
          </p>
        </div>
        <button
          type="button"
          onClick={logout}
          className="inline-flex min-h-[38px] items-center gap-1.5 rounded-lg border border-red-200 bg-white px-3.5 py-2 text-xs font-medium text-red-600 transition hover:bg-red-50 dark:border-red-900/50 dark:bg-zinc-950 dark:text-red-400 dark:hover:bg-red-950/30"
        >
          <LogOut className="h-3.5 w-3.5" />
          <span>Déconnexion</span>
        </button>
      </section>

      {/* Machines */}
      <section className="rounded-xl border border-zinc-200 bg-white p-4 dark:border-zinc-800 dark:bg-zinc-900/70">
        <div className="mb-3 flex items-center justify-between">
          <h2 className="text-sm font-semibold text-zinc-900 dark:text-zinc-100">Machines</h2>
          <span className="font-mono text-xs text-zinc-400">{machines.length}</span>
        </div>
        {machines.length === 0 ? (
          <p className="text-xs text-zinc-500 dark:text-zinc-400">Aucune machine enregistrée.</p>
        ) : (
          <div className="divide-y divide-zinc-200 dark:divide-zinc-800">
            {machines.map((m) => (
              <div key={m.name} className="flex flex-wrap items-center justify-between gap-2 py-2.5">
                <div>
                  <div className="flex items-center gap-2">
                    <span
                      className={`h-2 w-2 rounded-full ${
                        m.online ? 'bg-emerald-500' : 'bg-zinc-400'
                      }`}
                    />
                    <span className="text-sm font-medium text-zinc-900 dark:text-zinc-100">
                      {m.name}
                    </span>
                    <span className="rounded bg-zinc-100 px-1.5 py-0.5 font-mono text-[10px] text-zinc-500 dark:bg-zinc-800 dark:text-zinc-400">
                      {m.os || 'unknown'}
                    </span>
                  </div>
                  {m.roots.length > 0 && (
                    <p className="mt-1 font-mono text-[11px] text-zinc-500 dark:text-zinc-400">
                      Racines : {m.roots.join(', ')}
                    </p>
                  )}
                </div>
                <span className="font-mono text-[11px] text-zinc-400">
                  {m.online ? 'En ligne' : 'Hors ligne'}
                </span>
              </div>
            ))}
          </div>
        )}
      </section>

      {/* Outils CLI */}
      <section className="rounded-xl border border-zinc-200 bg-white p-4 dark:border-zinc-800 dark:bg-zinc-900/70">
        <div className="mb-3 flex items-center justify-between">
          <h2 className="flex items-center gap-1.5 text-sm font-semibold text-zinc-900 dark:text-zinc-100">
            <Wrench className="h-4 w-4 text-zinc-400" />
            <span>Outils CLI</span>
          </h2>
          <button
            type="button"
            onClick={() => void refreshAgents()}
            className="text-xs font-medium text-indigo-600 hover:underline dark:text-indigo-400"
          >
            Re-sonder
          </button>
        </div>

        <div className="space-y-3">
          {agents.map((agent) => {
            const perMachine = Object.values(agent.installations ?? {});
            return (
              <div
                key={agent.agentId}
                className="rounded-lg border border-zinc-200 p-3 dark:border-zinc-800"
              >
                <div className="flex items-center justify-between gap-2">
                  <div>
                    <span className="text-sm font-medium text-zinc-900 dark:text-zinc-100">
                      {agent.name}
                    </span>
                    <span className="ml-2 font-mono text-[11px] text-zinc-400">
                      {agent.launchCommand}
                    </span>
                  </div>
                  <span className="rounded bg-zinc-100 px-1.5 py-0.5 font-mono text-[10px] text-zinc-500 dark:bg-zinc-800 dark:text-zinc-400">
                    {agent.chatMode || 'headless'}
                  </span>
                </div>

                {perMachine.length > 0 && (
                  <div className="mt-2.5 space-y-1.5 border-t border-zinc-200/70 pt-2.5 dark:border-zinc-800/70">
                    {perMachine.map((inst) => (
                      <div
                        key={inst.machineName}
                        className="flex flex-wrap items-center justify-between gap-2 text-xs"
                      >
                        <div className="min-w-0">
                          <span className="font-medium text-zinc-700 dark:text-zinc-300">
                            {inst.machineName}
                          </span>
                          {inst.installed && inst.path && (
                            <span className="ml-2 break-all font-mono text-[11px] text-zinc-500 dark:text-zinc-400">
                              {inst.path}
                            </span>
                          )}
                        </div>
                        {inst.installed ? (
                          <span className="inline-flex items-center gap-1 text-[11px] font-medium text-emerald-600 dark:text-emerald-400">
                            <CheckCircle2 className="h-3.5 w-3.5" />
                            Installé
                          </span>
                        ) : (
                          <span className="inline-flex items-center gap-1 text-[11px] text-zinc-400">
                            <XCircle className="h-3.5 w-3.5" />
                            Non installé
                          </span>
                        )}
                      </div>
                    ))}
                  </div>
                )}
              </div>
            );
          })}
        </div>
      </section>

      {/* Notifications */}
      <section className="rounded-xl border border-zinc-200 bg-white p-4 dark:border-zinc-800 dark:bg-zinc-900/70">
        <div className="flex items-center justify-between">
          <h2 className="flex items-center gap-1.5 text-sm font-semibold text-zinc-900 dark:text-zinc-100">
            <Bell className="h-4 w-4 text-zinc-400" />
            <span>Notifications (ntfy)</span>
          </h2>
          {notifConfig && (
            <span
              className={`rounded-full px-2 py-0.5 text-[11px] font-medium ${
                notifConfig.enabled
                  ? 'bg-emerald-50 text-emerald-700 dark:bg-emerald-500/15 dark:text-emerald-300'
                  : 'bg-zinc-100 text-zinc-500 dark:bg-zinc-800 dark:text-zinc-400'
              }`}
            >
              {notifConfig.enabled ? 'Activées' : 'Désactivées'}
            </span>
          )}
        </div>
        {notifConfig?.enabled ? (
          <div className="mt-2 space-y-1 font-mono text-xs text-zinc-600 dark:text-zinc-400">
            <div>URL : {notifConfig.ntfyUrl}</div>
            <div>Topic : {notifConfig.ntfyTopic}</div>
          </div>
        ) : (
          <p className="mt-1.5 text-xs text-zinc-500 dark:text-zinc-400">
            Configurez <code className="font-mono">AGENTPILOT_NTFY_URL</code> et{' '}
            <code className="font-mono">AGENTPILOT_NTFY_TOPIC</code> sur le serveur pour recevoir
            les alertes d&apos;approbation.
          </p>
        )}
      </section>

      {/* Tokens */}
      <section className="rounded-xl border border-zinc-200 bg-white p-4 dark:border-zinc-800 dark:bg-zinc-900/70">
        <div className="mb-3 flex items-center justify-between">
          <h2 className="flex items-center gap-1.5 text-sm font-semibold text-zinc-900 dark:text-zinc-100">
            <KeyRound className="h-4 w-4 text-zinc-400" />
            <span>Tokens d&apos;accès</span>
          </h2>
          {loadingExtra && <Loader2 className="h-3.5 w-3.5 animate-spin text-zinc-400" />}
        </div>

        <form onSubmit={handleCreateToken} className="mb-4 flex flex-col gap-2 sm:flex-row">
          <input
            type="text"
            value={newTokenName}
            onChange={(e) => setNewTokenName(e.target.value)}
            placeholder="Nom du token (ex: pixel-perso, runner-win)"
            className="min-w-0 flex-1 rounded-lg border border-zinc-200 bg-white px-3 py-2 text-xs text-zinc-900 outline-none focus:border-indigo-500 dark:border-zinc-800 dark:bg-zinc-950 dark:text-zinc-100"
          />
          <select
            value={newTokenScope}
            onChange={(e) => setNewTokenScope(e.target.value as 'mobile' | 'runner')}
            className="rounded-lg border border-zinc-200 bg-white px-3 py-2 text-xs text-zinc-900 outline-none focus:border-indigo-500 dark:border-zinc-800 dark:bg-zinc-950 dark:text-zinc-100"
          >
            <option value="mobile">mobile</option>
            <option value="runner">runner</option>
          </select>
          <button
            type="submit"
            disabled={creatingToken}
            className="inline-flex min-h-[36px] items-center justify-center gap-1.5 rounded-lg bg-indigo-600 px-3.5 py-2 text-xs font-medium text-white transition hover:bg-indigo-700 disabled:opacity-50 dark:bg-indigo-500 dark:hover:bg-indigo-400"
          >
            {creatingToken ? (
              <Loader2 className="h-3.5 w-3.5 animate-spin" />
            ) : (
              <Plus className="h-3.5 w-3.5" />
            )}
            <span>Créer</span>
          </button>
        </form>

        {tokenError && (
          <div className="mb-3 rounded-lg border border-red-200 bg-red-50 px-3 py-2 text-xs text-red-700 dark:border-red-900/60 dark:bg-red-950/40 dark:text-red-300">
            {tokenError}
          </div>
        )}

        <div className="divide-y divide-zinc-200 dark:divide-zinc-800">
          {tokens.map((tok) => {
            const revoked = Boolean(tok.revokedAt);
            return (
              <div key={tok.id} className="flex items-center justify-between gap-3 py-2.5">
                <div className="min-w-0">
                  <div className="flex items-center gap-2">
                    <span
                      className={`truncate text-xs font-medium ${
                        revoked
                          ? 'text-zinc-400 line-through'
                          : 'text-zinc-900 dark:text-zinc-100'
                      }`}
                    >
                      {tok.name}
                    </span>
                    <span className="rounded bg-zinc-100 px-1.5 py-0.5 font-mono text-[10px] text-zinc-600 dark:bg-zinc-800 dark:text-zinc-300">
                      {tok.scope}
                    </span>
                    {revoked && (
                      <span className="text-[10px] text-zinc-400">révoqué</span>
                    )}
                  </div>
                  <p className="mt-0.5 font-mono text-[11px] text-zinc-400">
                    Créé le {new Date(tok.createdAt).toLocaleDateString('fr-FR')}
                  </p>
                </div>

                {!revoked && (
                  <button
                    type="button"
                    onClick={() => void handleRevokeToken(tok.id)}
                    disabled={revokingId === tok.id}
                    className="inline-flex items-center gap-1 rounded-md border border-zinc-200 px-2.5 py-1 text-[11px] font-medium text-red-600 hover:bg-red-50 disabled:opacity-50 dark:border-zinc-800 dark:text-red-400 dark:hover:bg-red-950/30"
                  >
                    {revokingId === tok.id ? (
                      <Loader2 className="h-3 w-3 animate-spin" />
                    ) : (
                      <Trash2 className="h-3 w-3" />
                    )}
                    <span>Révoquer</span>
                  </button>
                )}
              </div>
            );
          })}
        </div>
      </section>

      {/* One-Time Token Secret Dialog */}
      {createdToken && (
        <div
          className="fixed inset-0 z-50 flex items-center justify-center bg-black/50 p-4 backdrop-blur-[2px]"
          role="dialog"
          aria-modal="true"
        >
          <div className="w-full max-w-md rounded-xl border border-zinc-200 bg-white p-5 shadow-xl dark:border-zinc-800 dark:bg-zinc-900">
            <div className="flex items-center justify-between">
              <h3 className="text-sm font-semibold text-zinc-900 dark:text-zinc-100">
                Token créé : {createdToken.name}
              </h3>
              <button
                type="button"
                onClick={() => setCreatedToken(null)}
                className="flex h-8 w-8 items-center justify-center rounded-md text-zinc-400 hover:bg-zinc-100 dark:hover:bg-zinc-800"
              >
                <X className="h-4 w-4" />
              </button>
            </div>
            <p className="mt-1.5 text-xs text-amber-700 dark:text-amber-300">
              Copiez ce secret maintenant : il ne sera plus jamais affiché.
            </p>
            <div className="mt-3 rounded-lg border border-zinc-200 bg-zinc-50 p-3 font-mono text-xs break-all text-zinc-900 dark:border-zinc-800 dark:bg-zinc-950 dark:text-zinc-100">
              {createdToken.token}
            </div>
            <div className="mt-4 flex justify-end gap-2">
              <button
                type="button"
                onClick={handleCopyCreatedToken}
                className="inline-flex items-center gap-1.5 rounded-lg bg-indigo-600 px-3.5 py-2 text-xs font-medium text-white hover:bg-indigo-700 dark:bg-indigo-500"
              >
                {copiedSecret ? <Check className="h-3.5 w-3.5" /> : <Copy className="h-3.5 w-3.5" />}
                <span>{copiedSecret ? 'Copié' : 'Copier le token'}</span>
              </button>
              <button
                type="button"
                onClick={() => setCreatedToken(null)}
                className="rounded-lg border border-zinc-200 px-3.5 py-2 text-xs font-medium text-zinc-600 hover:bg-zinc-50 dark:border-zinc-800 dark:text-zinc-300 dark:hover:bg-zinc-800"
              >
                Fermer
              </button>
            </div>
          </div>
        </div>
      )}
    </div>
  );
}
