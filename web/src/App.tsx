import { useMemo, useState } from 'react';
import { AppProvider, useApp } from './context/AppContext';
import { AppShell, type NavTab } from './components/AppShell';
import { LoginView } from './components/LoginView';
import { ProjectsView } from './components/ProjectsView';
import { ProjectDetailView } from './components/ProjectDetailView';
import { SessionsView } from './components/SessionsView';
import { OneShotView } from './components/OneShotView';
import { SettingsView } from './components/SettingsView';
import { ChatView } from './components/ChatView';
import { CreateProjectModal } from './components/CreateProjectModal';
import { NewSessionModal } from './components/NewSessionModal';
import type { Project, SessionMeta, ThemeMode } from './api/types';

function nextTheme(current: ThemeMode): ThemeMode {
  if (current === 'system') return 'light';
  if (current === 'light') return 'dark';
  return 'system';
}

function AuthenticatedApp() {
  const {
    machines,
    projects,
    agents,
    sessions,
    themeMode,
    setThemeMode,
    refreshAll,
    loading,
  } = useApp();

  const [activeTab, setActiveTab] = useState<NavTab>('projects');
  const [selectedProjectId, setSelectedProjectId] = useState<string | null>(null);
  const [activeSessionId, setActiveSessionId] = useState<string | null>(null);
  const [oneShotProjectId, setOneShotProjectId] = useState<string | undefined>(undefined);

  const [createProjectOpen, setCreateProjectOpen] = useState(false);
  const [createProjectMachine, setCreateProjectMachine] = useState<string | undefined>(undefined);
  const [newSessionOpen, setNewSessionOpen] = useState(false);
  const [newSessionProjectId, setNewSessionProjectId] = useState<string | undefined>(undefined);

  const runningSessions = useMemo(
    () => sessions.filter((s) => s.status === 'running'),
    [sessions],
  );

  const selectedProject = useMemo<Project | undefined>(
    () => projects.find((p) => p.id === selectedProjectId),
    [projects, selectedProjectId],
  );

  const activeSession = useMemo<SessionMeta | undefined>(
    () => sessions.find((s) => s.id === activeSessionId),
    [sessions, activeSessionId],
  );

  const activeSessionProjectName = useMemo(() => {
    if (!activeSession) return undefined;
    return projects.find((p) => p.id === activeSession.projectId)?.name;
  }, [activeSession, projects]);

  function handleSelectTab(tab: NavTab) {
    setActiveTab(tab);
    setActiveSessionId(null);
    if (tab !== 'projects') {
      setSelectedProjectId(null);
    }
  }

  function handleOpenSession(session: SessionMeta) {
    setActiveSessionId(session.id);
  }

  function handleOpenCreateProject(defaultMachine?: string) {
    setCreateProjectMachine(defaultMachine);
    setCreateProjectOpen(true);
  }

  function handleOpenNewSession(projectId?: string) {
    setNewSessionProjectId(projectId);
    setNewSessionOpen(true);
  }

  function handleOpenOneShot(projectId: string) {
    setOneShotProjectId(projectId);
    setActiveSessionId(null);
    setSelectedProjectId(null);
    setActiveTab('oneshot');
  }

  const headerTitle = activeSession
    ? activeSessionProjectName ?? activeSession.agentId
    : selectedProject
      ? selectedProject.name
      : activeTab === 'projects'
        ? 'Projets'
        : activeTab === 'sessions'
          ? 'Sessions'
          : activeTab === 'oneshot'
            ? 'One-Shot'
            : 'Réglages';

  return (
    <>
      <AppShell
        activeTab={activeTab}
        onSelectTab={handleSelectTab}
        runningSessions={runningSessions}
        projects={projects}
        agents={agents}
        machines={machines}
        activeSessionId={activeSessionId}
        onOpenSession={handleOpenSession}
        onNewSession={() => handleOpenNewSession(selectedProject?.id)}
        themeMode={themeMode}
        onSetTheme={setThemeMode}
        onCycleTheme={() => setThemeMode(nextTheme(themeMode))}
        onRefresh={() => void refreshAll()}
        refreshing={loading}
        headerTitle={headerTitle}
        fullBleed={Boolean(activeSession)}
      >
        {activeSession ? (
          <ChatView
            session={activeSession}
            projectName={activeSessionProjectName}
            onBack={() => setActiveSessionId(null)}
            onSessionClosed={() => setActiveSessionId(null)}
            onOpenSession={handleOpenSession}
          />
        ) : activeTab === 'projects' ? (
          selectedProject ? (
            <ProjectDetailView
              project={selectedProject}
              onBack={() => setSelectedProjectId(null)}
              onNewSession={handleOpenNewSession}
              onOpenOneShot={handleOpenOneShot}
              onOpenSession={handleOpenSession}
              onDeleted={() => setSelectedProjectId(null)}
            />
          ) : (
            <ProjectsView
              onSelectProject={(proj) => setSelectedProjectId(proj.id)}
              onOpenCreateProject={handleOpenCreateProject}
              onOpenSession={handleOpenSession}
            />
          )
        ) : activeTab === 'sessions' ? (
          <SessionsView
            onOpenSession={handleOpenSession}
            onNewSession={() => handleOpenNewSession()}
          />
        ) : activeTab === 'oneshot' ? (
          <OneShotView initialProjectId={oneShotProjectId} />
        ) : (
          <SettingsView />
        )}
      </AppShell>

      <CreateProjectModal
        open={createProjectOpen}
        defaultMachine={createProjectMachine}
        onClose={() => setCreateProjectOpen(false)}
        onCreated={(proj) => setSelectedProjectId(proj.id)}
      />

      <NewSessionModal
        open={newSessionOpen}
        defaultProjectId={newSessionProjectId}
        onClose={() => setNewSessionOpen(false)}
        onCreated={(sess) => setActiveSessionId(sess.id)}
      />
    </>
  );
}

function AppRouter() {
  const { isAuthenticated } = useApp();
  return isAuthenticated ? <AuthenticatedApp /> : <LoginView />;
}

export function App() {
  return (
    <AppProvider>
      <AppRouter />
    </AppProvider>
  );
}

export default App;
