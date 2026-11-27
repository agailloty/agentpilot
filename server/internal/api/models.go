package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/agailloty/agentpilot/server/internal/hub"
	"github.com/agailloty/agentpilot/server/internal/manifests"
	"github.com/agailloty/agentpilot/server/internal/modelsync"
	"github.com/agailloty/agentpilot/server/internal/protocol"
	"github.com/agailloty/agentpilot/server/internal/store"
)

const defaultModelsSyncTimeoutSec = 25

type modelsRefreshResponse struct {
	AgentID     string                   `json:"agentId"`
	OptionID    string                   `json:"optionId"`
	Parser      string                   `json:"parser"`
	Mode        string                   `json:"mode"`
	ExitCode    int                      `json:"exitCode"`
	Count       int                      `json:"count"`
	Models      []manifests.OptionChoice `json:"models"`
	RefreshedAt time.Time                `json:"refreshedAt"`
}

// handleRefreshAgentModels runs the manifest's modelSync discovery on a runner
// machine, parses the CLI output, and persists the updated model choices into
// the stored manifest so every client sees the models the CLI really offers.
func (s *Server) handleRefreshAgentModels(w http.ResponseWriter, r *http.Request) {
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
	manifest, err := manifests.FromStoreManifest(stored)
	if err != nil {
		s.logger.Error("decode manifest failed", "agent_id", id, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to decode agent manifest")
		return
	}

	spec := manifest.ModelSync
	if spec == nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("agent %q has no modelSync configuration", id))
		return
	}

	machineName, err := s.pickModelsSyncMachine(r, id)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, err.Error())
		return
	}

	// Prefer the probed installed candidate path so the runner executes the
	// exact binary the UI reports as installed.
	command := manifest.LaunchCommand
	if probe, err := s.probeRunnerTools(r.Context(), machineName, manifest.Candidates()); err == nil {
		if st, ok := pickInstalledCandidate(manifest.Candidates(), probe); ok && st.Path != "" {
			command = st.Path
		}
	}

	timeoutSec := spec.TimeoutSec
	if timeoutSec <= 0 {
		timeoutSec = defaultModelsSyncTimeoutSec
	}

	type attempt struct {
		args []string
		send string
	}
	attempts := make([]attempt, 0, 2)
	if len(spec.Args) > 0 {
		attempts = append(attempts, attempt{args: spec.Args})
	}
	if send := strings.TrimSpace(spec.Send); send != "" {
		attempts = append(attempts, attempt{send: send})
	}

	var (
		bestOutput string
		bestAck    protocol.ModelsSyncAckPayload
		parsed     []modelsync.Model
		lastErr    error
	)
	for _, att := range attempts {
		env, err := protocol.NewEnvelope(protocol.TypeModelsSync, "", "", protocol.ModelsSyncPayload{
			Command:    command,
			Args:       att.args,
			Send:       att.send,
			TimeoutSec: timeoutSec,
		})
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to build models-sync request")
			return
		}
		reqCtx, cancel := context.WithTimeout(r.Context(), time.Duration(timeoutSec+10)*time.Second)
		reply, err := s.hub.SendRequest(reqCtx, machineName, env)
		cancel()
		if err != nil {
			if errors.Is(err, hub.ErrRunnerNotFound) || errors.Is(err, hub.ErrRunnerDisconnected) {
				writeError(w, http.StatusServiceUnavailable, "runner offline")
				return
			}
			writeError(w, http.StatusBadGateway, err.Error())
			return
		}
		if reply.Type == protocol.TypeError {
			var errPayload protocol.ErrorPayload
			_ = reply.DecodePayload(&errPayload)
			lastErr = fmt.Errorf("%s", errPayload.Message)
			continue
		}
		var ack protocol.ModelsSyncAckPayload
		if err := reply.DecodePayload(&ack); err != nil {
			lastErr = err
			continue
		}
		parser := spec.Parser
		if att.send != "" && strings.TrimSpace(spec.SendParser) != "" {
			parser = spec.SendParser
		}
		models, err := modelsync.Parse(parser, spec.Flag, spec.Pattern, ack.Output)
		if err != nil {
			lastErr = err
			continue
		}
		if len(models) > 0 {
			bestOutput = ack.Output
			bestAck = ack
			parsed = models
			break
		}
		lastErr = errors.New("no models parsed from CLI output")
		bestAck = ack
	}

	if len(parsed) == 0 {
		msg := "no models parsed from CLI output"
		if lastErr != nil {
			msg = fmt.Sprintf("%s: %v", msg, lastErr)
		}
		writeError(w, http.StatusUnprocessableEntity, msg)
		return
	}

	updated, _, err := modelsync.ApplyToManifest(manifest, bestOutput)
	if err != nil {
		s.logger.Error("apply models failed", "agent_id", id, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to apply discovered models")
		return
	}
	if err := updated.Validate(); err != nil {
		s.logger.Error("updated manifest invalid", "agent_id", id, "error", err)
		writeError(w, http.StatusInternalServerError, "updated manifest failed validation")
		return
	}
	raw, err := json.Marshal(updated)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to encode updated manifest")
		return
	}
	if _, err := s.store.UpsertManifest(r.Context(), store.AgentManifest{
		AgentID:       updated.AgentID,
		Name:          updated.Name,
		LaunchCommand: updated.LaunchCommand,
		ManifestJSON:  string(raw),
	}); err != nil {
		s.logger.Error("upsert manifest failed", "agent_id", id, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to persist updated manifest")
		return
	}

	models := make([]manifests.OptionChoice, 0, len(parsed))
	if opt := updated.FindOption(spec.OptionID); opt != nil {
		models = opt.Choices
	}

	writeJSON(w, http.StatusOK, modelsRefreshResponse{
		AgentID:     updated.AgentID,
		OptionID:    spec.OptionID,
		Parser:      spec.Parser,
		Mode:        bestAck.Mode,
		ExitCode:    bestAck.ExitCode,
		Count:       len(parsed),
		Models:      models,
		RefreshedAt: time.Now().UTC(),
	})
}

// pickModelsSyncMachine selects the target runner: explicit ?machine= query
// parameter, otherwise the first online runner (sorted by name).
func (s *Server) pickModelsSyncMachine(r *http.Request, agentID string) (string, error) {
	if s.hub == nil {
		return "", errors.New("runner hub unavailable")
	}
	if machine := strings.TrimSpace(r.URL.Query().Get("machine")); machine != "" {
		if _, online := s.hub.GetRunner(machine); !online {
			return "", fmt.Errorf("runner %q is offline", machine)
		}
		return machine, nil
	}
	runners := s.hub.ListRunners()
	if len(runners) == 0 {
		return "", fmt.Errorf("no runner online to query agent %q models", agentID)
	}
	sort.Slice(runners, func(i, j int) bool { return runners[i].MachineName < runners[j].MachineName })
	return runners[0].MachineName, nil
}
