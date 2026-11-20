package server

import (
	"context"
	"fmt"
	"net/http"
	"path"
	"strconv"
	"time"

	"github.com/jfrmorales/deckman/internal/config"
	"github.com/jfrmorales/deckman/internal/deck"
	"github.com/jfrmorales/deckman/internal/i18n"
)

// La biblioteca de la Deck: inventario, mover (con su cola e historial),
// eliminar, cambiar Proton y reiniciar Steam.

func (s *Server) handleInventory(w http.ResponseWriter, r *http.Request) {
	c, err := s.conn()
	if err != nil {
		writeErr(w, err, http.StatusConflict)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 120*time.Second)
	defer cancel()

	inv, err := c.Scan(ctx)
	if err != nil {
		writeErr(w, err, http.StatusBadGateway)
		return
	}
	s.storeInventory(c, inv)
	s.reconcileOperations(inv, s.deckHost())
	// Las portadas se resuelven aqui y no dentro de Scan: es una consulta mas a
	// la Deck que solo le hace falta a la biblioteca, no a mover ni a borrar.
	s.covers.setIndex(c.AnnotateCovers(ctx, inv))
	writeJSON(w, inv)
}

// storeInventory guarda una copia de la foto para mover/borrar. Copia y no el
// puntero: AnnotateCovers sigue escribiendo HasCover sobre el original
// mientras un trabajo podria estar leyendo la foto guardada.
func (s *Server) storeInventory(c *deck.Client, inv *deck.Inventory) {
	copia := *inv
	copia.Games = append([]deck.Game(nil), inv.Games...)
	copia.Libraries = append([]deck.Library(nil), inv.Libraries...)
	s.invMu.Lock()
	s.inv, s.invAt, s.invClient = &copia, time.Now(), c
	s.invMu.Unlock()
}

func (s *Server) dropInventory() {
	s.invMu.Lock()
	s.inv, s.invClient = nil, nil
	s.invMu.Unlock()
}

// inventory devuelve la foto reciente de esta misma conexion, o escanea. Los
// datos que salen de aqui solo se usan para localizar rutas y tamanos; toda
// decision peligrosa (escribir shortcuts.vdf, borrar) se comprueba fresca en
// deck.MoveGame/Delete.
func (s *Server) inventory(ctx context.Context, c *deck.Client) (*deck.Inventory, error) {
	s.invMu.Lock()
	if s.inv != nil && s.invClient == c && time.Since(s.invAt) < inventarioTTL {
		inv := s.inv
		s.invMu.Unlock()
		return inv, nil
	}
	s.invMu.Unlock()

	inv, err := c.Scan(ctx)
	if err != nil {
		return nil, err
	}
	s.storeInventory(c, inv)
	return inv, nil
}

func libraryLabel(l deck.Library) string {
	if l.Label != "" {
		return l.Label
	}
	if l.IsDefault {
		return "Interno"
	}
	return path.Base(l.Path)
}

func humanBytes(b uint64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := uint64(unit), 0
	for n := b / unit; n >= unit && exp < 4; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(b)/float64(div), "KMGTP"[exp])
}

func (s *Server) handleMove(w http.ResponseWriter, r *http.Request) {
	var req struct {
		AppID  string `json:"appId"`
		Target string `json:"target"`
	}
	if err := decode(r, &req); err != nil {
		writeErr(w, err, http.StatusBadRequest)
		return
	}
	c, err := s.conn()
	if err != nil {
		writeErr(w, err, http.StatusConflict)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()

	inv, err := s.inventory(ctx, c)
	if err != nil {
		writeErr(w, err, http.StatusBadGateway)
		return
	}

	var game *deck.Game
	for i := range inv.Games {
		if inv.Games[i].AppID == req.AppID {
			game = &inv.Games[i]
			break
		}
	}
	if game == nil {
		writeErr(w, i18n.Errorf("no se encontro el juego con id %s", req.AppID), http.StatusNotFound)
		return
	}

	libsByPath := make(map[string]string, len(inv.Libraries))
	var target *deck.Library
	for i := range inv.Libraries {
		libsByPath[inv.Libraries[i].Path] = libraryLabel(inv.Libraries[i])
		if inv.Libraries[i].Path == req.Target {
			target = &inv.Libraries[i]
		}
	}
	if target == nil {
		writeErr(w, i18n.Errorf("la unidad de destino %s no esta registrada en Steam", req.Target), http.StatusBadRequest)
		return
	}

	targetLabel := libraryLabel(*target)
	if game.LibraryPath == target.Path {
		writeErr(w, i18n.Errorf("%s ya esta en %s", game.Name, targetLabel), http.StatusConflict)
		return
	}
	needed := game.Size + game.CompatSize + game.ShaderSize
	if target.Free > 0 && needed > 0 && needed+512<<20 > target.Free {
		writeErr(w, i18n.Errorf("no cabe: %s ocupa %s y en %s quedan %s",
			game.Name, humanBytes(needed), targetLabel, humanBytes(target.Free)), http.StatusConflict)
		return
	}

	fromLabel := libsByPath[game.LibraryPath]
	if fromLabel == "" {
		fromLabel = "Interno"
	}

	host := s.deckHost()
	s.mu.RLock()
	cfg := s.cfg.Snapshot()
	s.mu.RUnlock()
	for _, existing := range cfg.Operations {
		if existing.Kind == "move" && existing.AppID == game.AppID &&
			(existing.DeckHost == "" || existing.DeckHost == host) &&
			(existing.Status == "en_curso" || existing.Status == "en_cola") {
			writeErr(w, i18n.Errorf("'%s' ya esta en curso o en la cola de movimientos", game.Name), http.StatusConflict)
			return
		}
	}

	op := config.Operation{
		ID:              strconv.FormatInt(time.Now().UnixNano(), 36),
		DeckHost:        host,
		Kind:            "move",
		Title:           "Moviendo " + game.Name,
		AppID:           game.AppID,
		GameName:        game.Name,
		FromLabel:       fromLabel,
		TargetPath:      target.Path,
		TargetLabel:     targetLabel,
		CurrentLocation: fromLabel,
		BytesTotal:      needed,
	}

	enqueue := func() {
		op.Status = "en_cola"
		op.StartedAt = time.Now().Unix()
		op.Message = fmt.Sprintf("En cola: %s -> %s", fromLabel, targetLabel)
		s.mu.Lock()
		s.cfg.UpsertOperation(op)
		snap := s.cfg.Snapshot()
		s.mu.Unlock()
		s.guardar(snap)
		s.broadcastOperations(snap.Operations)
		writeJSON(w, map[string]any{"ok": true, "job": op.ID, "queued": true, "operations": snap.Operations})
	}

	// Con una actualizacion descargandose o esperando el reinicio, el movimiento
	// arranca igual: solo se encola si hay un trabajo o el reinicio ya va.
	if s.jobBusy() || s.updateRestarting() {
		enqueue()
		return
	}

	j, err := s.startMoveOperation(op)
	if err != nil {
		if s.jobBusy() || s.updateRestarting() {
			enqueue()
			return
		}
		writeErr(w, err, http.StatusConflict)
		return
	}
	writeJSON(w, map[string]any{"ok": true, "job": j.ID, "queued": false})
}

func (s *Server) startMoveOperation(op config.Operation) (*job, error) {
	if op.Title == "" {
		if op.GameName != "" {
			op.Title = "Moviendo " + op.GameName
		} else {
			op.Title = "Moviendo juego"
		}
	}
	return s.startJobOp(op, func(ctx context.Context, report deck.ProgressFunc) error {
		c, err := s.conn()
		if err != nil {
			return err
		}
		inv, err := s.inventory(ctx, c)
		if err != nil {
			return err
		}
		// Si estaba en cola y mientras tanto ya llego al destino en la Deck,
		// lo damos por completado sin repetir la copia.
		for _, g := range inv.Games {
			if g.AppID == op.AppID && g.LibraryPath == op.TargetPath {
				report(deck.Progress{
					Phase:      "listo",
					Done:       true,
					BytesDone:  op.BytesTotal,
					BytesTotal: op.BytesTotal,
					Message:    fmt.Sprintf("%s ya esta en %s.", op.GameName, op.TargetLabel),
				})
				return nil
			}
		}
		// Tras mover, la foto guardada miente (rutas y espacio libre): fuera.
		defer s.dropInventory()
		return c.MoveGame(ctx, inv, op.AppID, op.TargetPath, report)
	})
}

// advanceQueue arranca el siguiente movimiento en cola en orden FIFO para la Deck conectada.
func (s *Server) advanceQueue() {
	if _, err := s.conn(); err != nil {
		return
	}
	// No Busy(): descargar una actualizacion no debe parar la cola.
	if s.jobBusy() || s.updateRestarting() {
		return
	}
	host := s.deckHost()
	s.mu.RLock()
	cfg := s.cfg.Snapshot()
	s.mu.RUnlock()

	for i := len(cfg.Operations) - 1; i >= 0; i-- {
		op := cfg.Operations[i]
		if op.Status == "en_cola" && op.Kind == "move" && (op.DeckHost == "" || op.DeckHost == host) {
			_, _ = s.startMoveOperation(op)
			return
		}
	}
}

// reconcileOperations actualiza donde esta cada juego segun la foto real de
// la Deck y marca como completado cualquier movimiento interrumpido que Steam
// terminase por su cuenta mientras deckman no estaba conectado.
func (s *Server) reconcileOperations(inv *deck.Inventory, deckHost string) {
	if inv == nil {
		return
	}
	libsByPath := make(map[string]string, len(inv.Libraries))
	for _, lib := range inv.Libraries {
		libsByPath[lib.Path] = libraryLabel(lib)
	}
	gamesByID := make(map[string]deck.Game, len(inv.Games))
	for _, g := range inv.Games {
		gamesByID[g.AppID] = g
	}

	s.mu.Lock()
	changed := false
	for i := range s.cfg.Operations {
		op := &s.cfg.Operations[i]
		if op.DeckHost != "" && deckHost != "" && op.DeckHost != deckHost {
			continue
		}
		if op.Kind != "move" || op.AppID == "" {
			continue
		}
		g, ok := gamesByID[op.AppID]
		if !ok {
			continue
		}
		loc := libsByPath[g.LibraryPath]
		if loc == "" && g.LibraryPath != "" {
			loc = path.Base(g.LibraryPath)
		}
		if loc != "" && op.CurrentLocation != loc {
			op.CurrentLocation = loc
			changed = true
		}
		if (op.Status == "interrumpido" || op.Status == "error") && op.TargetPath != "" && g.LibraryPath == op.TargetPath {
			op.Status = "completado"
			if op.FinishedAt == 0 {
				op.FinishedAt = time.Now().Unix()
			}
			if op.BytesTotal > 0 {
				op.BytesDone = op.BytesTotal
			}
			op.Message = fmt.Sprintf("Completado en la Deck (%s).", loc)
			changed = true
		}
	}
	var snap config.Config
	if changed {
		snap = s.cfg.Snapshot()
	}
	s.mu.Unlock()

	if changed {
		s.guardar(snap)
		s.broadcastOperations(snap.Operations)
	}
}

func (s *Server) handleClearOperations(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "solo POST", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		ID string `json:"id"`
	}
	if r.Body != nil {
		_ = decode(r, &req)
	}

	s.mu.Lock()
	if req.ID != "" {
		for _, op := range s.cfg.Operations {
			if op.ID == req.ID && op.Status == "en_curso" {
				s.mu.Unlock()
				writeErr(w, i18n.Errorf("hay una operacion en curso; cancelala o espera a que termine"), http.StatusConflict)
				return
			}
		}
		s.cfg.RemoveOperation(req.ID)
	} else {
		s.cfg.ClearFinishedOperations()
	}
	snap := s.cfg.Snapshot()
	s.mu.Unlock()
	s.guardar(snap)
	s.broadcastOperations(snap.Operations)
	// Quitar un movimiento de la cola puede dejar vacio lo que retenia el
	// reinicio de la actualizacion.
	s.maybeRestartAfterUpdate()
	writeJSON(w, map[string]any{"ok": true, "operations": snap.Operations})
}

func (s *Server) handleDelete(w http.ResponseWriter, r *http.Request) {
	var req struct {
		AppID   string             `json:"appId"`
		Targets deck.DeleteTargets `json:"targets"`
	}
	if err := decode(r, &req); err != nil {
		writeErr(w, err, http.StatusBadRequest)
		return
	}
	c, err := s.conn()
	if err != nil {
		writeErr(w, err, http.StatusConflict)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Minute)
	defer cancel()

	inv, err := s.inventory(ctx, c)
	if err != nil {
		writeErr(w, err, http.StatusBadGateway)
		return
	}
	// Tras borrar, la foto guardada miente (juego y tamanos): fuera.
	defer s.dropInventory()
	freed, err := c.Delete(ctx, inv, req.AppID, req.Targets)
	if err != nil {
		writeErr(w, err, http.StatusBadGateway)
		return
	}
	writeJSON(w, map[string]any{"ok": true, "freed": freed})
}

func (s *Server) handleCompat(w http.ResponseWriter, r *http.Request) {
	var req struct {
		AppID string `json:"appId"`
		Tool  string `json:"tool"`
	}
	if err := decode(r, &req); err != nil {
		writeErr(w, err, http.StatusBadRequest)
		return
	}
	c, err := s.conn()
	if err != nil {
		writeErr(w, err, http.StatusConflict)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()

	// Con Steam abierto se le pide a el: escribir config.vdf por detras se
	// pierde en cuanto Steam se cierra, que era justo lo que la interfaz pedia
	// hacer para ver el cambio. Ver deck.ApplyCompatTool.
	live, err := c.ApplyCompatTool(ctx, req.AppID, req.Tool)
	if err != nil {
		writeErr(w, err, http.StatusBadGateway)
		return
	}
	writeJSON(w, map[string]any{"ok": true, "live": live})
}

func (s *Server) handleRestartSteam(w http.ResponseWriter, r *http.Request) {
	c, err := s.conn()
	if err != nil {
		writeErr(w, err, http.StatusConflict)
		return
	}
	// Sin timeout del request: reiniciar Steam puede pasar del minuto y no
	// queremos abortarlo a medias.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	msg, err := c.RestartSteam(ctx)
	if err != nil {
		writeErr(w, err, http.StatusBadGateway)
		return
	}
	writeJSON(w, map[string]any{"ok": true, "message": msg})
}
