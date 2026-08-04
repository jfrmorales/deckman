package server

import (
	"encoding/json"
	"net/http"
)

func (s *Server) handleRomsList(w http.ResponseWriter, r *http.Request) {
	system := r.URL.Query().Get("system")
	if system == "" {
		s.replyError(w, http.StatusBadRequest, "Falta el sistema")
		return
	}

	c := s.client()
	if c == nil {
		s.replyError(w, http.StatusServiceUnavailable, "Sin conexión")
		return
	}

	roms, err := c.ListROMs(r.Context(), system)
	if err != nil {
		s.replyError(w, http.StatusInternalServerError, err.Error())
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"roms": roms})
}

func (s *Server) handleRomsDelete(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Path string `json:"path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.replyError(w, http.StatusBadRequest, "JSON inválido")
		return
	}

	c := s.client()
	if c == nil {
		s.replyError(w, http.StatusServiceUnavailable, "Sin conexión")
		return
	}

	if err := c.DeleteROM(r.Context(), req.Path); err != nil {
		s.replyError(w, http.StatusInternalServerError, err.Error())
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"ok":true}`))
}

func (s *Server) handleRomsRename(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Path    string `json:"path"`
		NewName string `json:"newName"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.NewName == "" {
		s.replyError(w, http.StatusBadRequest, "Datos inválidos")
		return
	}

	c := s.client()
	if c == nil {
		s.replyError(w, http.StatusServiceUnavailable, "Sin conexión")
		return
	}

	if err := c.RenameROM(r.Context(), req.Path, req.NewName); err != nil {
		s.replyError(w, http.StatusInternalServerError, err.Error())
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"ok":true}`))
}

func (s *Server) handleRomsDownload(w http.ResponseWriter, r *http.Request) {
	var req struct {
		URL    string `json:"url"`
		System string `json:"system"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.URL == "" || req.System == "" {
		s.replyError(w, http.StatusBadRequest, "Datos inválidos")
		return
	}

	c := s.client()
	if c == nil {
		s.replyError(w, http.StatusServiceUnavailable, "Sin conexión")
		return
	}

	if err := c.DownloadROM(r.Context(), req.URL, req.System); err != nil {
		s.replyError(w, http.StatusInternalServerError, err.Error())
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"ok":true}`))
}
