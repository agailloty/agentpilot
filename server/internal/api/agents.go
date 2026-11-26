package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/agailloty/agentpilot/server/internal/manifests"
	"github.com/agailloty/agentpilot/server/internal/protocol"
	"github.com/agailloty/agentpilot/server/internal/store"
)

const toolsProbeTimeout = 2 * time.Second

type agentInstallation struct {
	MachineName string `json:"machineName"`
	Online      bool   `json:"online"`
	Installed   bool   `json:"installed"`
	Path        string `json:"path,omitempty"`
}

type agentResponse struct {
	AgentID        string                       `json:"agentId"`
	Name           string                       `json:"name"`
	LaunchCommand  string                       `json:"launchCommand"`
	Args           []string                     `json:"args"`
	ChatMode       string                       `json:"chatMode"`
	ProviderMenus  *manifests.ProviderMenus     `json:"providerMenus,omitempty"`
	Commands       []manifests.Command          `json:"commands"`
	Installed      bool                         `json:"installed"`
	ExecutablePath string                       `json:"executablePath,omitempty"`
	Installations  map[string]agentInstallation `json:"installations"`
}

func (s *Server) handleListAgents(w http.ResponseWriter, r *http.Request) {
	stored, err := s.store.ListManifests(r.Context())
	if err != nil {
		s.logger.Error("list manifests failed", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to list agents")
		return
	}

	decoded := make([]manifests.Manifest, 0, len(stored))
	for _, sm := range stored {
		m, err := manifests.FromStoreManifest(sm)
		if err != nil {
			s.logger.Error("decode stored manifest failed", "agent_id", sm.AgentID, "error", err)
			writeError(w, http.StatusInternalServerError, "failed to decode agent manifest")
			return
		}
		decoded = append(decoded, m)
	}

	dbMachines, err := s.store.ListMachines(r.Context())
	if err != nil {
		s.logger.Error("list machines failed", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to list agents")
		return
	}

	knownMachines := make(map[string]bool, len(dbMachines))
	for _, m := range dbMachines {
		knownMachines[m.Name] = false
	}
	var onlineMachines []string
	if s.hub != nil {
		for _, runner := range s.hub.ListRunners() {
			knownMachines[runner.MachineName] = true
			onlineMachines = append(onlineMachines, runner.MachineName)
		}
	}

	machineNames := make([]string, 0, len(knownMachines))
	for name := range knownMachines {
		machineNames = append(machineNames, name)
	}
	sort.Strings(machineNames)

	allCommands := collectManifestCandidates(decoded)
	probeResults := s.probeOnlineRunners(r.Context(), onlineMachines, allCommands)
	targetMachine := strings.TrimSpace(r.URL.Query().Get("machine"))

	out := make([]agentResponse, 0, len(decoded))
	for _, m := range decoded {
		args := m.Args
		if args == nil {
			args = []string{}
		}
		cmds := m.Commands
		if cmds == nil {
			cmds = []manifests.Command{}
		}

		installations := make(map[string]agentInstallation, len(machineNames))
		for _, machineName := range machineNames {
			online := knownMachines[machineName]
			inst := agentInstallation{
				MachineName: machineName,
				Online:      online,
			}
			if online {
				if st, ok := pickInstalledCandidate(m.Candidates(), probeResults[machineName]); ok {
					inst.Installed = true
					inst.Path = st.Path
				}
			}
			installations[machineName] = inst
		}

		installed, execPath := resolveTopLevelInstallation(targetMachine, machineNames, installations)
		out = append(out, agentResponse{
			AgentID:        m.AgentID,
			Name:           m.Name,
			LaunchCommand:  m.LaunchCommand,
			Args:           args,
			ChatMode:       m.ChatMode,
			ProviderMenus:  m.ProviderMenus,
			Commands:       cmds,
			Installed:      installed,
			ExecutablePath: execPath,
			Installations:  installations,
		})
	}

	writeJSON(w, http.StatusOK, out)
}

func collectManifestCandidates(list []manifests.Manifest) []string {
	seen := make(map[string]struct{})
	out := make([]string, 0)
	for _, m := range list {
		for _, cmd := range m.Candidates() {
			if _, ok := seen[cmd]; ok {
				continue
			}
			seen[cmd] = struct{}{}
			out = append(out, cmd)
		}
	}
	return out
}

func (s *Server) probeOnlineRunners(ctx context.Context, onlineMachines, commands []string) map[string]map[string]protocol.ToolStatus {
	results := make(map[string]map[string]protocol.ToolStatus, len(onlineMachines))
	if s.hub == nil || len(onlineMachines) == 0 || len(commands) == 0 {
		return results
	}

	var (
		mu sync.Mutex
		wg sync.WaitGroup
	)
	for _, machineName := range onlineMachines {
		wg.Add(1)
		go func(name string) {
			defer wg.Done()
			tools, err := s.probeRunnerTools(ctx, name, commands)
			if err != nil {
				s.logger.Debug("tools-probe failed", "machine", name, "error", err)
				return
			}
			mu.Lock()
			results[name] = tools
			mu.Unlock()
		}(machineName)
	}
	wg.Wait()
	return results
}

func (s *Server) probeRunnerTools(ctx context.Context, machineName string, commands []string) (map[string]protocol.ToolStatus, error) {
	if s.hub == nil {
		return nil, errors.New("runner hub unavailable")
	}
	if len(commands) == 0 {
		return map[string]protocol.ToolStatus{}, nil
	}

	env, err := protocol.NewEnvelope(protocol.TypeToolsProbe, "", "", protocol.ToolsProbePayload{
		Commands: commands,
	})
	if err != nil {
		return nil, err
	}

	probeCtx, cancel := context.WithTimeout(ctx, toolsProbeTimeout)
	defer cancel()

	reply, err := s.hub.SendRequest(probeCtx, machineName, env)
	if err != nil {
		return nil, err
	}
	if reply.Type == protocol.TypeError {
		var errPayload protocol.ErrorPayload
		_ = reply.DecodePayload(&errPayload)
		msg := errPayload.Message
		if msg == "" {
			msg = "runner tools-probe failed"
		}
		return nil, fmt.Errorf("%s", msg)
	}

	var ack protocol.ToolsProbeAckPayload
	if err := reply.DecodePayload(&ack); err != nil {
		return nil, err
	}
	return ack.Tools, nil
}

func pickInstalledCandidate(candidates []string, toolMap map[string]protocol.ToolStatus) (protocol.ToolStatus, bool) {
	if len(toolMap) == 0 {
		return protocol.ToolStatus{}, false
	}
	for _, cand := range candidates {
		if st, ok := toolMap[cand]; ok && st.Installed {
			return st, true
		}
	}
	return protocol.ToolStatus{}, false
}

func resolveTopLevelInstallation(targetMachine string, sortedMachines []string, installations map[string]agentInstallation) (bool, string) {
	if targetMachine != "" {
		if inst, ok := installations[targetMachine]; ok && inst.Installed {
			return true, inst.Path
		}
		return false, ""
	}
	for _, machineName := range sortedMachines {
		inst := installations[machineName]
		if inst.Online && inst.Installed {
			return true, inst.Path
		}
	}
	return false, ""
}

func (s *Server) handleGetAgentCommands(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeError(w, http.StatusBadRequest, "agent id is required")
		return
	}

	stored, err := s.store.GetManifest(r.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "agent not found")
			return
		}
		s.logger.Error("get manifest failed", "agent_id", id, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to load agent")
		return
	}

	m, err := manifests.FromStoreManifest(stored)
	if err != nil {
		s.logger.Error("decode stored manifest failed", "agent_id", id, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to decode agent manifest")
		return
	}

	cmds := m.Commands
	if cmds == nil {
		cmds = []manifests.Command{}
	}
	writeJSON(w, http.StatusOK, cmds)
}
