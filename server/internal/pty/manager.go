package pty

import (
	"errors"
	"fmt"
	"sort"
	"sync"
)

var (
	// ErrSessionExists is returned when spawning a session with an already active ID.
	ErrSessionExists = errors.New("pty: session already exists")
	// ErrSessionNotFound is returned when looking up or closing an unknown session ID.
	ErrSessionNotFound = errors.New("pty: session not found")
)

// Manager tracks active PTY sessions by session ID in a thread-safe manner.
type Manager struct {
	mu       sync.RWMutex
	sessions map[string]Session
}

// NewManager constructs an empty PTY session Manager.
func NewManager() *Manager {
	return &Manager{
		sessions: make(map[string]Session),
	}
}

// Spawn creates and registers a new PTY session under sessionID.
func (m *Manager) Spawn(sessionID string, opts SpawnOptions) (Session, error) {
	m.mu.RLock()
	_, exists := m.sessions[sessionID]
	m.mu.RUnlock()
	if exists {
		return nil, fmt.Errorf("%w: %s", ErrSessionExists, sessionID)
	}

	sess, err := spawnSession(sessionID, opts)
	if err != nil {
		return nil, err
	}

	m.mu.Lock()
	if _, exists := m.sessions[sessionID]; exists {
		m.mu.Unlock()
		_ = sess.Close()
		return nil, fmt.Errorf("%w: %s", ErrSessionExists, sessionID)
	}
	m.sessions[sessionID] = sess
	m.mu.Unlock()

	return sess, nil
}

// Get returns the active session for sessionID, if present.
func (m *Manager) Get(sessionID string) (Session, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	sess, ok := m.sessions[sessionID]
	return sess, ok
}

// Close terminates and unregisters the session identified by sessionID.
func (m *Manager) Close(sessionID string) error {
	m.mu.Lock()
	sess, ok := m.sessions[sessionID]
	if ok {
		delete(m.sessions, sessionID)
	}
	m.mu.Unlock()

	if !ok {
		return fmt.Errorf("%w: %s", ErrSessionNotFound, sessionID)
	}
	return sess.Close()
}

// CloseAll terminates and unregisters all tracked sessions.
func (m *Manager) CloseAll() error {
	m.mu.Lock()
	sessions := make([]Session, 0, len(m.sessions))
	for id, sess := range m.sessions {
		sessions = append(sessions, sess)
		delete(m.sessions, id)
	}
	m.mu.Unlock()

	var errs []error
	for _, sess := range sessions {
		if err := sess.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// ActiveIDs returns the sorted list of currently registered session IDs.
func (m *Manager) ActiveIDs() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	ids := make([]string, 0, len(m.sessions))
	for id := range m.sessions {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}
