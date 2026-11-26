package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func openTestStore(t *testing.T) *Store {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "agentpilot.db")
	s, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open(%q) error = %v", dbPath, err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})
	return s
}

func TestOpenPragmasAndMigrations(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "nested", "agentpilot.db")
	s, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer func() { _ = s.Close() }()

	var journalMode string
	if err := s.DB().QueryRow("PRAGMA journal_mode").Scan(&journalMode); err != nil {
		t.Fatalf("PRAGMA journal_mode error = %v", err)
	}
	if journalMode != "wal" {
		t.Errorf("journal_mode = %q, want wal", journalMode)
	}

	var foreignKeys int
	if err := s.DB().QueryRow("PRAGMA foreign_keys").Scan(&foreignKeys); err != nil {
		t.Fatalf("PRAGMA foreign_keys error = %v", err)
	}
	if foreignKeys != 1 {
		t.Errorf("foreign_keys = %d, want 1", foreignKeys)
	}

	var busyTimeout int
	if err := s.DB().QueryRow("PRAGMA busy_timeout").Scan(&busyTimeout); err != nil {
		t.Fatalf("PRAGMA busy_timeout error = %v", err)
	}
	if busyTimeout != 5000 {
		t.Errorf("busy_timeout = %d, want 5000", busyTimeout)
	}

	// Reopening the same DB must be idempotent with Goose migrations.
	if err := s.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	reopened, err := Open(dbPath)
	if err != nil {
		t.Fatalf("reopen Open() error = %v", err)
	}
	_ = reopened.Close()
}

func TestMachinesCRUD(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)

	m1, err := s.UpsertMachine(ctx, Machine{
		Name:   "pc-bureau",
		OS:     "windows",
		Roots:  []string{`C:\repos`, `D:\work`},
		Online: true,
	})
	if err != nil {
		t.Fatalf("UpsertMachine() error = %v", err)
	}
	if m1.ID == "" {
		t.Fatal("expected generated machine ID")
	}

	// Upsert same machine name updates fields while preserving ID and CreatedAt.
	m2, err := s.UpsertMachine(ctx, Machine{
		Name:   "pc-bureau",
		OS:     "linux",
		Roots:  []string{"/home/dev/repos"},
		Online: false,
	})
	if err != nil {
		t.Fatalf("UpsertMachine(update) error = %v", err)
	}
	if m2.ID != m1.ID {
		t.Errorf("ID changed on upsert: got %q, want %q", m2.ID, m1.ID)
	}
	if m2.OS != "linux" || m2.Online {
		t.Errorf("updated machine = %+v, want os=linux online=false", m2)
	}

	later := time.Now().UTC().Add(time.Minute)
	if err := s.SetMachineOnline(ctx, "pc-bureau", true, later); err != nil {
		t.Fatalf("SetMachineOnline() error = %v", err)
	}
	got, err := s.GetMachine(ctx, m1.ID)
	if err != nil {
		t.Fatalf("GetMachine() error = %v", err)
	}
	if !got.Online {
		t.Error("expected machine to be online after SetMachineOnline")
	}

	list, err := s.ListMachines(ctx)
	if err != nil {
		t.Fatalf("ListMachines() error = %v", err)
	}
	if len(list) != 1 || list[0].Name != "pc-bureau" {
		t.Errorf("ListMachines() = %+v, want 1 item pc-bureau", list)
	}
}

func TestProjectsSessionsAndCascadeDelete(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)

	p1, err := s.CreateProject(ctx, Project{
		Name:        "api",
		MachineName: "box-1",
		Path:        "/srv/api",
	})
	if err != nil {
		t.Fatalf("CreateProject() error = %v", err)
	}
	p2, err := s.CreateProject(ctx, Project{
		Name:        "web",
		MachineName: "box-2",
		Path:        "/srv/web",
	})
	if err != nil {
		t.Fatalf("CreateProject(p2) error = %v", err)
	}

	allProjects, err := s.ListProjects(ctx, "")
	if err != nil {
		t.Fatalf("ListProjects() error = %v", err)
	}
	if len(allProjects) != 2 {
		t.Fatalf("len(ListProjects) = %d, want 2", len(allProjects))
	}

	box1Projects, err := s.ListProjects(ctx, "box-1")
	if err != nil {
		t.Fatalf("ListProjects(box-1) error = %v", err)
	}
	if len(box1Projects) != 1 || box1Projects[0].ID != p1.ID {
		t.Errorf("ListProjects(box-1) = %+v, want only %s", box1Projects, p1.ID)
	}

	// Foreign key enforcement must reject session referencing nonexistent project.
	if _, err := s.CreateSession(ctx, Session{
		ProjectID:   "missing-project",
		MachineName: "box-1",
		AgentID:     "copilot",
		Cwd:         "/srv/api",
	}); err == nil {
		t.Fatal("expected foreign key error for nonexistent project_id")
	}

	sess, err := s.CreateSession(ctx, Session{
		ProjectID:   p1.ID,
		MachineName: "box-1",
		AgentID:     "copilot",
		Cwd:         "/srv/api",
		Status:      "running",
	})
	if err != nil {
		t.Fatalf("CreateSession() error = %v", err)
	}

	code := 0
	if err := s.CloseSession(ctx, sess.ID, "closed", &code, "sessions/s1.gz", 512, time.Now().UTC()); err != nil {
		t.Fatalf("CloseSession() error = %v", err)
	}

	gotSess, err := s.GetSession(ctx, sess.ID)
	if err != nil {
		t.Fatalf("GetSession() error = %v", err)
	}
	if gotSess.Status != "closed" || gotSess.ExitCode == nil || *gotSess.ExitCode != 0 || gotSess.ClosedAt == nil {
		t.Errorf("closed session = %+v, want status=closed exitCode=0 closedAt!=nil", gotSess)
	}
	if gotSess.SnapshotPath != "sessions/s1.gz" || gotSess.SnapshotBytes != 512 {
		t.Errorf("snapshot metadata = (%q, %d), want (sessions/s1.gz, 512)", gotSess.SnapshotPath, gotSess.SnapshotBytes)
	}

	sessions, err := s.ListSessions(ctx, p1.ID)
	if err != nil {
		t.Fatalf("ListSessions() error = %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("len(ListSessions) = %d, want 1", len(sessions))
	}

	// Deleting project must cascade delete its sessions.
	if err := s.DeleteProject(ctx, p1.ID); err != nil {
		t.Fatalf("DeleteProject() error = %v", err)
	}
	if _, err := s.GetSession(ctx, sess.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetSession after cascade = %v, want ErrNotFound", err)
	}
	if _, err := s.GetProject(ctx, p2.ID); err != nil {
		t.Errorf("GetProject(p2) error = %v", err)
	}
}

func TestManifestsCRUD(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)

	m, err := s.UpsertManifest(ctx, AgentManifest{
		AgentID:       "copilot",
		Name:          "GitHub Copilot CLI",
		LaunchCommand: "copilot",
		ManifestJSON:  `{"agentId":"copilot"}`,
	})
	if err != nil {
		t.Fatalf("UpsertManifest() error = %v", err)
	}
	if m.AgentID != "copilot" {
		t.Errorf("AgentID = %q, want copilot", m.AgentID)
	}

	_, err = s.UpsertManifest(ctx, AgentManifest{
		AgentID:       "copilot",
		Name:          "Copilot",
		LaunchCommand: "gh copilot",
		ManifestJSON:  `{"agentId":"copilot","v":2}`,
	})
	if err != nil {
		t.Fatalf("UpsertManifest(update) error = %v", err)
	}

	got, err := s.GetManifest(ctx, "copilot")
	if err != nil {
		t.Fatalf("GetManifest() error = %v", err)
	}
	if got.LaunchCommand != "gh copilot" {
		t.Errorf("LaunchCommand = %q, want gh copilot", got.LaunchCommand)
	}

	list, err := s.ListManifests(ctx)
	if err != nil {
		t.Fatalf("ListManifests() error = %v", err)
	}
	if len(list) != 1 {
		t.Errorf("len(ListManifests) = %d, want 1", len(list))
	}
}

func TestTokensCRUD(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)

	tok, err := s.CreateToken(ctx, Token{
		Name:      "phone",
		Scope:     "mobile",
		TokenHash: "hash-phone-1",
	})
	if err != nil {
		t.Fatalf("CreateToken() error = %v", err)
	}
	if tok.IsRevoked() {
		t.Fatal("new token should not be revoked")
	}

	count, err := s.CountActiveTokens(ctx)
	if err != nil {
		t.Fatalf("CountActiveTokens() error = %v", err)
	}
	if count != 1 {
		t.Errorf("CountActiveTokens() = %d, want 1", count)
	}

	if err := s.RevokeToken(ctx, tok.ID); err != nil {
		t.Fatalf("RevokeToken() error = %v", err)
	}

	got, err := s.GetTokenByHash(ctx, "hash-phone-1")
	if err != nil {
		t.Fatalf("GetTokenByHash() error = %v", err)
	}
	if !got.IsRevoked() {
		t.Error("expected token to be revoked")
	}

	count, err = s.CountActiveTokens(ctx)
	if err != nil {
		t.Fatalf("CountActiveTokens() error = %v", err)
	}
	if count != 0 {
		t.Errorf("CountActiveTokens() after revoke = %d, want 0", count)
	}

	// UpsertToken replaces hash and clears revocation for the same (name, scope).
	rotated, err := s.UpsertToken(ctx, Token{
		Name:      "phone",
		Scope:     "mobile",
		TokenHash: "hash-phone-2",
	})
	if err != nil {
		t.Fatalf("UpsertToken() error = %v", err)
	}
	if rotated.ID != tok.ID || rotated.IsRevoked() {
		t.Errorf("rotated token = %+v, want id=%s active", rotated, tok.ID)
	}

	tokens, err := s.ListTokens(ctx)
	if err != nil {
		t.Fatalf("ListTokens() error = %v", err)
	}
	if len(tokens) != 1 {
		t.Errorf("len(ListTokens) = %d, want 1", len(tokens))
	}
}

func TestChatMessagesAndSessionOptionsCRUD(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)

	proj, err := s.CreateProject(ctx, Project{
		Name:        "chat-proj",
		MachineName: "box-1",
		Path:        "/srv/chat-proj",
	})
	if err != nil {
		t.Fatalf("CreateProject() error = %v", err)
	}

	sess, err := s.CreateSession(ctx, Session{
		ProjectID:   proj.ID,
		MachineName: "box-1",
		AgentID:     "mistral",
		Cwd:         proj.Path,
		Status:      "running",
		Options:     map[string]string{"model": "default", "thinking": "low"},
	})
	if err != nil {
		t.Fatalf("CreateSession() error = %v", err)
	}
	if sess.Options["thinking"] != "low" {
		t.Fatalf("sess.Options = %+v, want thinking=low", sess.Options)
	}

	if err := s.UpdateSessionOptions(ctx, sess.ID, map[string]string{"model": "devstral-2", "thinking": "high"}); err != nil {
		t.Fatalf("UpdateSessionOptions() error = %v", err)
	}
	reloaded, err := s.GetSession(ctx, sess.ID)
	if err != nil {
		t.Fatalf("GetSession() error = %v", err)
	}
	if reloaded.Options["model"] != "devstral-2" || reloaded.Options["thinking"] != "high" {
		t.Fatalf("reloaded.Options = %+v, want devstral-2/high", reloaded.Options)
	}

	u1, err := s.CreateMessage(ctx, ChatMessage{
		SessionID: sess.ID,
		Role:      "user",
		Content:   "Explain architecture",
		Status:    "done",
	})
	if err != nil {
		t.Fatalf("CreateMessage(user) error = %v", err)
	}
	a1, err := s.CreateMessage(ctx, ChatMessage{
		SessionID: sess.ID,
		Role:      "assistant",
		Content:   "",
		Status:    "streaming",
	})
	if err != nil {
		t.Fatalf("CreateMessage(assistant) error = %v", err)
	}

	exitZero := 0
	if err := s.UpdateMessage(ctx, a1.ID, "Structured chat response with needle_xyz", "done", &exitZero, 142); err != nil {
		t.Fatalf("UpdateMessage() error = %v", err)
	}

	count, err := s.CountUserMessages(ctx, sess.ID)
	if err != nil {
		t.Fatalf("CountUserMessages() error = %v", err)
	}
	if count != 1 {
		t.Fatalf("CountUserMessages() = %d, want 1", count)
	}

	msgs, err := s.ListMessages(ctx, sess.ID)
	if err != nil {
		t.Fatalf("ListMessages() error = %v", err)
	}
	if len(msgs) != 2 || msgs[0].ID != u1.ID || msgs[1].ID != a1.ID {
		t.Fatalf("ListMessages() = %+v, want [%s, %s]", msgs, u1.ID, a1.ID)
	}
	if msgs[1].Status != "done" || msgs[1].DurationMs != 142 || msgs[1].ExitCode == nil || *msgs[1].ExitCode != 0 {
		t.Fatalf("unexpected updated assistant message: %+v", msgs[1])
	}

	matched, err := s.SessionMatchesMessageQuery(ctx, sess.ID, "NEEDLE_XYZ")
	if err != nil || !matched {
		t.Fatalf("SessionMatchesMessageQuery(NEEDLE_XYZ) = (%v, %v), want (true, nil)", matched, err)
	}
	notMatched, err := s.SessionMatchesMessageQuery(ctx, sess.ID, "absent-term")
	if err != nil || notMatched {
		t.Fatalf("SessionMatchesMessageQuery(absent-term) = (%v, %v), want (false, nil)", notMatched, err)
	}

	// Deleting project cascades to session and session_messages.
	if err := s.DeleteProject(ctx, proj.ID); err != nil {
		t.Fatalf("DeleteProject() error = %v", err)
	}
	afterDelete, err := s.ListMessages(ctx, sess.ID)
	if err != nil {
		t.Fatalf("ListMessages() after cascade error = %v", err)
	}
	if len(afterDelete) != 0 {
		t.Fatalf("expected 0 messages after cascade delete, got %d", len(afterDelete))
	}
}
