package api

import (
	"context"
	"errors"
	"net/http"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/agailloty/agentpilot/server/internal/hub"
	"github.com/agailloty/agentpilot/server/internal/protocol"
	"github.com/agailloty/agentpilot/server/internal/store"
)

const runnerRequestTimeout = 30 * time.Second

type machineResponse struct {
	ID         string    `json:"id"`
	Name       string    `json:"name"`
	OS         string    `json:"os"`
	Roots      []string  `json:"roots"`
	Online     bool      `json:"online"`
	LastSeenAt time.Time `json:"lastSeenAt"`
}

type machineFSResponse struct {
	MachineName string             `json:"machineName"`
	OS          string             `json:"os"`
	CurrentPath string             `json:"currentPath"`
	ParentPath  string             `json:"parentPath,omitempty"`
	Roots       []string           `json:"roots"`
	Entries     []protocol.FSEntry `json:"entries"`
}

type projectResponse struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	MachineName string    `json:"machineName"`
	Path        string    `json:"path"`
	Archived    bool      `json:"archived"`
	CreatedAt   time.Time `json:"createdAt"`
}

type createProjectRequest struct {
	Name        string `json:"name"`
	MachineName string `json:"machineName"`
	ParentPath  string `json:"parentPath"`
	GitInit     bool   `json:"gitInit"`
}

func (s *Server) handleListMachines(w http.ResponseWriter, r *http.Request) {
	dbMachines, err := s.store.ListMachines(r.Context())
	if err != nil {
		s.logger.Error("list machines failed", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to list machines")
		return
	}

	liveByName := make(map[string]hub.RunnerInfo)
	if s.hub != nil {
		for _, runner := range s.hub.ListRunners() {
			liveByName[runner.MachineName] = runner
		}
	}

	out := make([]machineResponse, 0, len(dbMachines)+len(liveByName))
	for _, m := range dbMachines {
		item := machineResponse{
			ID:         m.ID,
			Name:       m.Name,
			OS:         m.OS,
			Roots:      nonNilStrings(m.Roots),
			Online:     false,
			LastSeenAt: m.LastSeenAt,
		}
		if runner, ok := liveByName[m.Name]; ok {
			item.Online = true
			item.Roots = nonNilStrings(runner.Roots)
			if runner.OS != "" {
				item.OS = runner.OS
			}
			delete(liveByName, m.Name)
		}
		out = append(out, item)
	}

	for _, runner := range liveByName {
		out = append(out, machineResponse{
			Name:       runner.MachineName,
			OS:         runner.OS,
			Roots:      nonNilStrings(runner.Roots),
			Online:     true,
			LastSeenAt: runner.ConnectedAt,
		})
	}

	sort.Slice(out, func(i, j int) bool {
		return out[i].Name < out[j].Name
	})

	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleListMachineFS(w http.ResponseWriter, r *http.Request) {
	machineName := strings.TrimSpace(r.PathValue("name"))
	if machineName == "" {
		writeError(w, http.StatusBadRequest, "machine name is required")
		return
	}

	rawPath := strings.TrimSpace(r.URL.Query().Get("path"))
	if rawPath != "" && rawPath != "__drives__" && hasTraversalSegment(rawPath) {
		writeError(w, http.StatusBadRequest, "invalid path")
		return
	}

	if s.hub == nil {
		writeError(w, http.StatusServiceUnavailable, "runner offline")
		return
	}
	runnerInfo, ok := s.hub.GetRunner(machineName)
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "runner offline")
		return
	}

	env, err := protocol.NewEnvelope(protocol.TypeFSList, "", "", protocol.FSListPayload{
		Root: rawPath,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to build fs-list request")
		return
	}

	reqCtx, cancel := context.WithTimeout(r.Context(), runnerRequestTimeout)
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
		msg := errPayload.Message
		if msg == "" {
			msg = "runner failed to list directory"
		}
		writeError(w, http.StatusBadGateway, msg)
		return
	}

	var ack protocol.FSListAckPayload
	if err := reply.DecodePayload(&ack); err != nil {
		writeError(w, http.StatusBadGateway, "invalid runner fs-list response")
		return
	}

	entries := ack.Entries
	if entries == nil {
		entries = []protocol.FSEntry{}
	}

	writeJSON(w, http.StatusOK, machineFSResponse{
		MachineName: machineName,
		OS:          runnerInfo.OS,
		CurrentPath: ack.CurrentPath,
		ParentPath:  ack.ParentPath,
		Roots:       nonNilStrings(runnerInfo.Roots),
		Entries:     entries,
	})
}

func (s *Server) handleListProjects(w http.ResponseWriter, r *http.Request) {
	machine := strings.TrimSpace(r.URL.Query().Get("machine"))
	projects, err := s.store.ListProjects(r.Context(), machine)
	if err != nil {
		s.logger.Error("list projects failed", "machine", machine, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to list projects")
		return
	}

	out := make([]projectResponse, 0, len(projects))
	for _, p := range projects {
		out = append(out, toProjectResponse(p))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleCreateProject(w http.ResponseWriter, r *http.Request) {
	var req createProjectRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	name := strings.TrimSpace(req.Name)
	machineName := strings.TrimSpace(req.MachineName)
	parentPath := strings.TrimSpace(req.ParentPath)

	if name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	if machineName == "" {
		writeError(w, http.StatusBadRequest, "machineName is required")
		return
	}
	if parentPath == "" {
		writeError(w, http.StatusBadRequest, "parentPath is required")
		return
	}
	if !isValidProjectName(name) {
		writeError(w, http.StatusBadRequest, "invalid project name")
		return
	}
	if hasTraversalSegment(parentPath) {
		writeError(w, http.StatusBadRequest, "invalid parentPath")
		return
	}

	if s.hub == nil {
		writeError(w, http.StatusServiceUnavailable, "runner offline")
		return
	}
	runnerInfo, ok := s.hub.GetRunner(machineName)
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "runner offline")
		return
	}

	targetPath := joinRunnerPath(runnerInfo.OS, parentPath, name)
	env, err := protocol.NewEnvelope(protocol.TypeFSMkdir, "", "", protocol.FSMkdirPayload{
		Path:    targetPath,
		GitInit: req.GitInit,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to build mkdir request")
		return
	}

	reqCtx, cancel := context.WithTimeout(r.Context(), runnerRequestTimeout)
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
		msg := errPayload.Message
		if msg == "" {
			msg = "runner failed to create project directory"
		}
		writeError(w, http.StatusBadGateway, msg)
		return
	}

	finalPath := targetPath
	if len(reply.Payload) > 0 {
		var ack protocol.FSMkdirAckPayload
		if err := reply.DecodePayload(&ack); err == nil && strings.TrimSpace(ack.Path) != "" {
			finalPath = ack.Path
		}
	}

	created, err := s.store.CreateProject(r.Context(), store.Project{
		Name:        name,
		MachineName: machineName,
		Path:        finalPath,
	})
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			writeError(w, http.StatusConflict, "project already exists at target path")
			return
		}
		s.logger.Error("create project failed", "machine", machineName, "path", finalPath, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to save project")
		return
	}

	writeJSON(w, http.StatusCreated, toProjectResponse(created))
}

func (s *Server) handleDeleteProject(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeError(w, http.StatusBadRequest, "project id is required")
		return
	}

	deleteFiles := false
	if raw := strings.TrimSpace(r.URL.Query().Get("deleteFiles")); raw != "" {
		v, err := strconv.ParseBool(raw)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid deleteFiles query parameter")
			return
		}
		deleteFiles = v
	}

	proj, err := s.store.GetProject(r.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "project not found")
			return
		}
		s.logger.Error("get project failed", "id", id, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to load project")
		return
	}

	if deleteFiles {
		if strings.TrimSpace(proj.Path) == "" {
			writeError(w, http.StatusBadRequest, "project path is empty")
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

		env, err := protocol.NewEnvelope(protocol.TypeFSDelete, "", "", protocol.FSDeletePayload{
			Path: proj.Path,
		})
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to build delete request")
			return
		}

		reqCtx, cancel := context.WithTimeout(r.Context(), runnerRequestTimeout)
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
				msg = "runner failed to delete project directory"
			}
			writeError(w, http.StatusBadGateway, msg)
			return
		}
	}

	if err := s.store.DeleteProject(r.Context(), id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "project not found")
			return
		}
		s.logger.Error("delete project failed", "id", id, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to delete project")
		return
	}

	writeJSON(w, http.StatusOK, map[string]bool{"deleted": true})
}

func toProjectResponse(p store.Project) projectResponse {
	return projectResponse{
		ID:          p.ID,
		Name:        p.Name,
		MachineName: p.MachineName,
		Path:        p.Path,
		Archived:    p.Archived,
		CreatedAt:   p.CreatedAt,
	}
}

func nonNilStrings(in []string) []string {
	if in == nil {
		return []string{}
	}
	return append([]string(nil), in...)
}

func isValidProjectName(name string) bool {
	if name == "" || name == "." || name == ".." {
		return false
	}
	if strings.ContainsAny(name, "/\\\x00") {
		return false
	}
	return true
}

func hasTraversalSegment(rawPath string) bool {
	if strings.ContainsRune(rawPath, '\x00') {
		return true
	}
	normalized := strings.ReplaceAll(rawPath, "\\", "/")
	for _, seg := range strings.Split(normalized, "/") {
		if seg == ".." {
			return true
		}
	}
	return false
}

func joinRunnerPath(runnerOS, parentPath, name string) string {
	if strings.EqualFold(runnerOS, "windows") {
		trimmed := strings.TrimRight(parentPath, "\\/")
		if trimmed == "" {
			return "\\" + name
		}
		return trimmed + "\\" + name
	}
	return path.Join(parentPath, name)
}
