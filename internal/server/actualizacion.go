package server

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/jfrmorales/deckman/internal/i18n"
	"github.com/jfrmorales/deckman/internal/update"
)

// UpdateState resume para la interfaz el estado de comprobacion e instalacion
// de actualizaciones. Las URLs directas de descarga no se exponen al navegador.
type UpdateState struct {
	Status       string `json:"status"` // "idle", "checking", "available", "up_to_date", "applying", "restarting", "error"
	Current      string `json:"current,omitempty"`
	Latest       string `json:"latest,omitempty"`
	Available    bool   `json:"available"`
	CanApply     bool   `json:"canApply"`
	ManualReason string `json:"manualReason,omitempty"`
	ReleaseURL   string `json:"releaseUrl,omitempty"`
	AssetName    string `json:"assetName,omitempty"`
	AssetSize    int64  `json:"assetSize,omitempty"`
	Signed       bool   `json:"signed"`
	IsFlatpak    bool   `json:"isFlatpak"`
	Notes        string `json:"notes,omitempty"`
	CheckedAt    int64  `json:"checkedAt,omitempty"`
	Phase        string `json:"phase,omitempty"`
	BytesDone    int64  `json:"bytesDone,omitempty"`
	BytesTotal   int64  `json:"bytesTotal,omitempty"`
	Error        string `json:"error,omitempty"`
}

func (s *Server) getUpdater() *update.Updater {
	s.updMu.Lock()
	defer s.updMu.Unlock()
	if s.updater == nil {
		s.updater = update.New(s.Version)
	} else if s.updater.CurrentVersion == "" {
		s.updater.CurrentVersion = s.Version
	}
	return s.updater
}

func (s *Server) updateSnapshot() UpdateState {
	s.updMu.Lock()
	st := s.updState
	s.updMu.Unlock()
	if st.Status == "" {
		st.Status = "idle"
	}
	if st.Current == "" {
		st.Current = s.Version
	}
	return st
}

func (s *Server) updateBusy() bool {
	s.updMu.Lock()
	defer s.updMu.Unlock()
	return s.updState.Status == "applying" || s.updState.Status == "restarting"
}

func stateFromUpdateInfo(info *update.Info) UpdateState {
	status := "up_to_date"
	if info.Available {
		status = "available"
	}
	return UpdateState{
		Status:       status,
		Current:      info.Current,
		Latest:       info.Latest,
		Available:    info.Available,
		CanApply:     info.CanApply,
		ManualReason: info.ManualReason,
		ReleaseURL:   info.ReleaseURL,
		AssetName:    info.AssetName,
		AssetSize:    info.AssetSize,
		Signed:       info.Signed,
		IsFlatpak:    info.IsFlatpak,
		Notes:        info.Notes,
		CheckedAt:    info.CheckedAt,
	}
}

func (s *Server) broadcastUpdate(st UpdateState) {
	s.broadcast(map[string]any{"type": "update", "update": st})
}

// StartAutoUpdateCheck lanza en segundo plano una comprobacion discreta al
// arrancar, salvo que este desactivada en Ajustes o sea una compilacion dev.
// Se llama solo desde cmd/deckman para que las pruebas no salgan a la red.
func (s *Server) StartAutoUpdateCheck() {
	s.mu.RLock()
	disabled := s.cfg.DisableUpdateCheck
	s.mu.RUnlock()
	if disabled || update.IsDevVersion(s.Version) {
		return
	}

	u := s.getUpdater()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()

		info, err := u.Check(ctx)
		if err != nil {
			s.logf("actualizacion automatica: %v", err)
			return
		}

		st := stateFromUpdateInfo(info)
		s.updMu.Lock()
		// Si mientras tanto el usuario pidio comprobar o instalar manualmente,
		// no pisamos su estado.
		if s.updState.Status != "" && s.updState.Status != "idle" {
			s.updMu.Unlock()
			return
		}
		s.updInfo = info
		s.updState = st
		s.updMu.Unlock()

		if info.Available {
			s.broadcastUpdate(st)
		}
	}()
}

func (s *Server) handleUpdateCheck(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "solo POST", http.StatusMethodNotAllowed)
		return
	}

	s.updMu.Lock()
	switch s.updState.Status {
	case "applying", "restarting":
		s.updMu.Unlock()
		writeErr(w, i18n.Errorf("hay una actualizacion en curso"), http.StatusConflict)
		return
	case "checking":
		s.updMu.Unlock()
		writeErr(w, i18n.Errorf("ya se estan buscando actualizaciones"), http.StatusConflict)
		return
	}
	prevState := s.updState
	s.updState.Status = "checking"
	s.updState.Error = ""
	if s.updState.Current == "" {
		s.updState.Current = s.Version
	}
	checkingSnap := s.updState
	s.updMu.Unlock()
	s.broadcastUpdate(checkingSnap)

	u := s.getUpdater()
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()

	info, err := u.Check(ctx)
	if err != nil {
		s.updMu.Lock()
		st := prevState
		st.Status = "error"
		if st.Current == "" {
			st.Current = s.Version
		}
		st.Error = i18n.Traducir(err, idioma())
		s.updState = st
		s.updMu.Unlock()
		s.broadcastUpdate(st)
		writeErr(w, err, http.StatusBadGateway)
		return
	}

	st := stateFromUpdateInfo(info)
	s.updMu.Lock()
	s.updInfo = info
	s.updState = st
	s.updMu.Unlock()
	s.broadcastUpdate(st)
	writeJSON(w, st)
}

func (s *Server) handleUpdateApply(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "solo POST", http.StatusMethodNotAllowed)
		return
	}
	if s.jobBusy() {
		writeErr(w, i18n.Errorf("hay una operacion en curso; cancelala o espera a que termine"), http.StatusConflict)
		return
	}

	s.updMu.Lock()
	if s.updState.Status == "applying" || s.updState.Status == "restarting" {
		s.updMu.Unlock()
		writeErr(w, i18n.Errorf("hay una actualizacion en curso"), http.StatusConflict)
		return
	}
	if s.updInfo == nil || !s.updInfo.Available || !s.updInfo.CanApply {
		s.updMu.Unlock()
		writeErr(w, i18n.Errorf("no hay ninguna actualizacion lista para instalar"), http.StatusBadRequest)
		return
	}

	info := *s.updInfo
	lang := idioma()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	s.updCancel = cancel
	st := stateFromUpdateInfo(&info)
	st.Status = "applying"
	st.Phase = "verificando"
	st.BytesTotal = info.AssetSize
	s.updState = st
	s.updMu.Unlock()

	s.broadcastUpdate(st)
	writeJSON(w, map[string]any{"ok": true})

	u := s.getUpdater()
	go func() {
		defer cancel()
		s.runUpdateApply(ctx, u, info, lang)
	}()
}

func (s *Server) runUpdateApply(ctx context.Context, u *update.Updater, info update.Info, lang string) {
	report := func(p update.Progress) {
		s.updMu.Lock()
		s.updState.Phase = p.Phase
		s.updState.BytesDone = p.BytesDone
		if p.BytesTotal > 0 {
			s.updState.BytesTotal = p.BytesTotal
		}
		// Al llegar a la sustitucion en disco o Flatpak ya no se puede cancelar
		// sin dejar la instalacion a medias.
		if p.Phase == "instalando" {
			s.updCancel = nil
		}
		snap := s.updState
		s.updMu.Unlock()
		s.broadcastUpdate(snap)
	}

	err := u.Apply(ctx, &info, report)
	if err != nil {
		s.updMu.Lock()
		s.updCancel = nil
		st := stateFromUpdateInfo(&info)
		if !errors.Is(err, context.Canceled) {
			st.Status = "error"
			st.Error = i18n.Traducir(err, lang)
		}
		s.updState = st
		s.updMu.Unlock()
		s.broadcastUpdate(st)
		return
	}

	s.updMu.Lock()
	s.updCancel = nil
	s.updState.Status = "restarting"
	s.updState.Phase = "reiniciando"
	s.updState.Error = ""
	snap := s.updState
	delay := s.updRestartDelay
	s.updMu.Unlock()

	s.broadcastUpdate(snap)
	if delay > 0 {
		time.Sleep(delay)
	}
	s.requestRestart()
}
