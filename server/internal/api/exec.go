package api

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/agailloty/agentpilot/server/internal/hub"
	"github.com/agailloty/agentpilot/server/internal/protocol"
	"github.com/agailloty/agentpilot/server/internal/store"
)

const (
	defaultExecTimeoutSec = 60
	maxExecTimeoutSec     = 600
	execGracePeriod       = 10 * time.Second
)

type execProjectRequest struct {
	Command    string `json:"command"`
	TimeoutSec int    `json:"timeoutSec"`
}

type execProjectResponse struct {
	ExitCode   int    `json:"exitCode"`
	Output     string `json:"output"`
	DurationMs int64  `json:"durationMs"`
}

func (s *Server) handleExecProject(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeError(w, http.StatusBadRequest, "project id is required")
		return
	}

	var req execProjectRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	command := strings.TrimSpace(req.Command)
	if command == "" {
		writeError(w, http.StatusBadRequest, "command is required")
		return
	}
	if req.TimeoutSec < 0 {
		writeError(w, http.StatusBadRequest, "timeoutSec must be non-negative")
		return
	}
	timeoutSec := req.TimeoutSec
	if timeoutSec == 0 {
		timeoutSec = defaultExecTimeoutSec
	}
	if timeoutSec > maxExecTimeoutSec {
		timeoutSec = maxExecTimeoutSec
	}

	proj, err := s.store.GetProject(r.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "project not found")
			return
		}
		s.logger.Error("get project for exec failed", "project_id", id, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to load project")
		return
	}

	if s.hub == nil {
		writeError(w, http.StatusServiceUnavailable, "runner offline")
		return
	}
	if _, ok := s.hub.GetRunner(proj.MachineName); !ok {
		writeError(w, http.StatusServiceUnavailable, "runner offline")
		return
	}

	env, err := protocol.NewEnvelope(protocol.TypeExecSync, "", "", protocol.ExecSyncPayload{
		Cwd:        proj.Path,
		Command:    req.Command,
		TimeoutSec: timeoutSec,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to build exec-sync request")
		return
	}

	reqTimeout := time.Duration(timeoutSec)*time.Second + execGracePeriod
	reqCtx, cancel := context.WithTimeout(r.Context(), reqTimeout)
	reply, err := s.hub.SendRequest(reqCtx, proj.MachineName, env)
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
		msg := errPayload.Message
		if msg == "" {
			msg = "runner failed to execute command"
		}
		writeError(w, http.StatusBadGateway, msg)
		return
	}

	var ack protocol.ExecSyncAckPayload
	if err := reply.DecodePayload(&ack); err != nil {
		writeError(w, http.StatusBadGateway, "invalid exec-sync response from runner")
		return
	}

	writeJSON(w, http.StatusOK, execProjectResponse{
		ExitCode:   ack.ExitCode,
		Output:     ack.Output,
		DurationMs: ack.DurationMs,
	})
}
