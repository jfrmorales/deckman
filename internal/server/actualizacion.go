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
	// "pending_restart": la version nueva ya esta en disco pero el reinicio se
	// aplaza hasta que acaben el trabajo en curso y la cola.
	Status       string `json:"status"` // "idle", "checking", "available", "up_to_date", "applying", "pending_restart", "restarting", "error"
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
	// Pending es el trabajo que un reinicio cortaria o que lo retrasa. Se
	// rellena en cualquier estado para que la confirmacion de actualizar pueda
	// listarlo antes de aceptar.
	Pending []PendingWork `json:"pending,omitempty"`
}

// PendingWork es un trabajo que impide reiniciar ya: el activo o uno de la cola.
type PendingWork struct {
	ID     string `json:"id"`
	Kind   string `json:"kind"`
	Title  string `json:"title"`
	Status string `json:"status"` // "en_curso" | "en_cola"
}

// pendingWork lista el trabajo activo y los movimientos en cola que
// advanceQueue ejecutaria (los de la Deck conectada), en el orden en que
// correran. Se mira lo mismo que mira advanceQueue: si contase otra cosa, el
// reinicio esperaria por operaciones que nadie va a arrancar.
func (s *Server) pendingWork() []PendingWork {
	var out []PendingWork
	s.jobMu.Lock()
	if s.job != nil && !s.job.Prog.Done && s.job.Prog.Error == "" {
		out = append(out, PendingWork{ID: s.job.ID, Kind: s.job.Kind, Title: s.job.Title, Status: "en_curso"})
	}
	s.jobMu.Unlock()

	host := s.deckHost()
	s.mu.RLock()
	ops := s.cfg.Snapshot().Operations
	s.mu.RUnlock()
	// La lista guarda lo mas nuevo primero; la cola es FIFO.
	for i := len(ops) - 1; i >= 0; i-- {
		op := ops[i]
		if op.Status == "en_cola" && op.Kind == "move" && (op.DeckHost == "" || op.DeckHost == host) {
			out = append(out, PendingWork{ID: op.ID, Kind: op.Kind, Title: op.Title, Status: "en_cola"})
		}
	}
	return out
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
	// Fuera de updMu: pendingWork toma otros cerrojos y aqui no hace falta
	// que la foto sea atomica con el estado.
	st.Pending = s.pendingWork()
	return st
}

// updateBusy dice si la actualizacion esta ocupando el proceso (descargando o
// reiniciando). Cuenta para Busy(): con la interfaz cerrada, el proceso no se
// apaga a mitad de instalar. En "pending_restart" NO cuenta: la version nueva
// ya esta en disco, y si el usuario cierra la ventana mientras se espera, el
// proceso se apaga normal al acabar el trabajo y la proxima vez arranca ya con
// ella.
func (s *Server) updateBusy() bool {
	s.updMu.Lock()
	defer s.updMu.Unlock()
	return s.updState.Status == "applying" || s.updState.Status == "restarting"
}

// updateRestarting dice si el reinicio ya esta en marcha. Es lo unico que
// impide arrancar trabajos: mientras se descarga o se espera, la cola sigue.
func (s *Server) updateRestarting() bool {
	s.updMu.Lock()
	defer s.updMu.Unlock()
	return s.updState.Status == "restarting"
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
	st.Pending = s.pendingWork()
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
	case "pending_restart":
		// Ya esta instalada: buscar de nuevo pisaria el estado y la interfaz
		// perderia que hay un reinicio esperando.
		s.updMu.Unlock()
		writeErr(w, i18n.Errorf("la actualizacion ya esta instalada; deckman se reiniciara al terminar los trabajos pendientes"), http.StatusConflict)
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
	// Ya no se niega por haber trabajos: se descarga e instala ahora y el
	// reinicio espera a que terminen (ver decideRestart).
	s.updMu.Lock()
	if s.updState.Status == "applying" || s.updState.Status == "restarting" || s.updState.Status == "pending_restart" {
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
	s.updMu.Unlock()
	s.decideRestart("applying")
}

// decideRestart reinicia si ya no queda trabajo, o deja el estado en
// "pending_restart" si queda. Parte de `from` y no hace nada si el estado ya es
// otro: asi dos disparos a la vez (fin de trabajo y fin de instalacion) no
// reinician dos veces.
//
// La decision y el cambio de estado van bajo updMu. Sin eso, un trabajo que
// acabase justo entre mirar la lista y pasar a "pending_restart" vendria a
// comprobar un estado que aun no era el suyo, y el reinicio se quedaria
// esperando para siempre.
func (s *Server) decideRestart(from string) {
	s.updMu.Lock()
	if s.updState.Status != from {
		s.updMu.Unlock()
		return
	}
	s.updState.Error = ""
	if len(s.pendingWork()) > 0 {
		s.updState.Status = "pending_restart"
		s.updState.Phase = ""
		snap := s.updState
		s.updMu.Unlock()
		s.broadcastUpdate(snap)
		return
	}
	s.updState.Status = "restarting"
	s.updState.Phase = "reiniciando"
	snap := s.updState
	delay := s.updRestartDelay
	s.updMu.Unlock()

	s.broadcastUpdate(snap)
	if delay > 0 {
		time.Sleep(delay)
	}
	s.requestRestart()
}

// maybeRestartAfterUpdate se llama cada vez que puede haber bajado el trabajo
// pendiente (fin de un trabajo, cola modificada). Si el reinicio estaba
// aplazado y ya no queda nada, reinicia; si queda algo, reenvia el estado para
// que la interfaz vea bajar la lista.
func (s *Server) maybeRestartAfterUpdate() {
	s.decideRestart("pending_restart")
}

// handleUpdateRestart fuerza el reinicio aplazado: corta el trabajo en curso.
// Los movimientos en cola quedan como interrumpidos al arrancar
// (RecoverInterrupted); la interfaz lo avisa antes de pedir esto.
func (s *Server) handleUpdateRestart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "solo POST", http.StatusMethodNotAllowed)
		return
	}
	s.updMu.Lock()
	if s.updState.Status != "pending_restart" {
		s.updMu.Unlock()
		writeErr(w, i18n.Errorf("no hay ningun reinicio pendiente"), http.StatusConflict)
		return
	}
	s.updState.Status = "restarting"
	s.updState.Phase = "reiniciando"
	snap := s.updState
	delay := s.updRestartDelay
	s.updMu.Unlock()

	s.broadcastUpdate(snap)
	writeJSON(w, map[string]any{"ok": true})

	s.jobMu.Lock()
	if s.job != nil && s.job.cancel != nil {
		s.job.cancel()
	}
	s.jobMu.Unlock()

	go func() {
		// Dejar que el trabajo cancelado anote su estado "interrumpido" en el
		// historial antes de morir; si no responde, no se espera mas.
		for i := 0; i < 50 && s.jobBusy(); i++ {
			time.Sleep(100 * time.Millisecond)
		}
		if delay > 0 {
			time.Sleep(delay)
		}
		s.requestRestart()
	}()
}

// handleUpdateCancel cancela solo la descarga de la actualizacion; el trabajo
// en curso no se toca (eso es /api/cancel).
func (s *Server) handleUpdateCancel(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "solo POST", http.StatusMethodNotAllowed)
		return
	}
	s.updMu.Lock()
	if s.updCancel != nil {
		s.updCancel()
	}
	s.updMu.Unlock()
	writeJSON(w, map[string]any{"ok": true})
}
