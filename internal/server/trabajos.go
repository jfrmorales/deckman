package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/jfrmorales/deckman/internal/config"
	"github.com/jfrmorales/deckman/internal/deck"
	"github.com/jfrmorales/deckman/internal/i18n"
)

// Trabajos largos: uno a la vez, con progreso y cancelacion.

// --- trabajos largos ---

// startJob arranca una operacion larga si no hay otra en curso.
func (s *Server) startJob(kind, title string, fn func(ctx context.Context, report deck.ProgressFunc) error) (*job, error) {
	return s.startJobOp(config.Operation{Kind: kind, Title: title}, fn)
}

func (s *Server) deckHost() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cfg.ActiveDeck().Host
}

func (s *Server) broadcastOperations(ops []config.Operation) {
	s.broadcast(map[string]any{"type": "operations", "operations": ops})
}

// startJobOp arranca una operacion larga registrandola en el historial persistente.
func (s *Server) startJobOp(op config.Operation, fn func(ctx context.Context, report deck.ProgressFunc) error) (*job, error) {
	// Solo se rechaza cuando el reinicio ya esta en marcha: mientras se
	// descarga o se espera a que acabe la cola, los trabajos siguen.
	if s.updateRestarting() {
		return nil, i18n.Errorf("hay una actualizacion en curso")
	}
	if op.DeckHost == "" {
		op.DeckHost = s.deckHost()
	}

	s.jobMu.Lock()
	if s.job != nil && !s.job.Prog.Done && s.job.Prog.Error == "" {
		title := s.job.Title
		s.jobMu.Unlock()
		return nil, i18n.Errorf("ya hay una operacion en curso: %s", title)
	}

	if op.ID == "" {
		op.ID = strconv.FormatInt(time.Now().UnixNano(), 36)
	}
	ctx, cancel := context.WithCancel(context.Background())
	j := &job{
		ID:     op.ID,
		Kind:   op.Kind,
		Title:  op.Title,
		Prog:   deck.Progress{Phase: "empezando"},
		cancel: cancel,
	}
	s.job = j
	s.jobMu.Unlock()

	op.Status = "en_curso"
	op.StartedAt = time.Now().Unix()
	op.FinishedAt = 0
	op.Message = ""
	s.mu.Lock()
	s.cfg.UpsertOperation(op)
	snapStart := s.cfg.Snapshot()
	s.mu.Unlock()
	s.guardar(snapStart)
	s.broadcastOperations(snapStart.Operations)

	var lastMu sync.Mutex
	var lastMsg string
	report := func(p deck.Progress) {
		if p.Message != "" {
			lastMu.Lock()
			lastMsg = p.Message
			lastMu.Unlock()
		}
		// Done se marca solo cuando fn ha retornado y el historial se ha guardado,
		// para evitar huecos entre el fin de un trabajo y el arranque del siguiente en cola.
		running := p
		running.Done = false
		s.jobMu.Lock()
		j.Prog = running
		s.jobMu.Unlock()
		s.broadcast(map[string]any{"type": "progress", "job": j.ID, "title": j.Title, "kind": j.Kind, "progress": running})
	}

	go func() {
		defer cancel()
		err := fn(ctx, report)

		lastMu.Lock()
		msg := lastMsg
		lastMu.Unlock()

		s.jobMu.Lock()
		final := j.Prog
		s.jobMu.Unlock()

		final.Done = true
		final.Speed = 0
		if final.Message == "" {
			final.Message = msg
		}
		if err != nil {
			final.Error = err.Error()
			final.Phase = "error"
		} else if final.Phase != "listo" {
			final.Phase = "listo"
		}

		op.FinishedAt = time.Now().Unix()
		if final.BytesTotal > 0 {
			op.BytesTotal = final.BytesTotal
		}
		op.BytesDone = final.BytesDone
		canceled := errors.Is(err, context.Canceled)
		if err != nil {
			if canceled {
				op.Status = "interrumpido"
				op.Message = "Cancelado por el usuario."
			} else {
				op.Status = "error"
				op.Message = err.Error()
			}
			if op.Kind == "move" && op.CurrentLocation == "" {
				op.CurrentLocation = op.FromLabel
			}
		} else {
			op.Status = "completado"
			if final.Message != "" {
				op.Message = final.Message
			} else if op.Kind == "move" && op.TargetLabel != "" {
				op.Message = fmt.Sprintf("Movido a %s.", op.TargetLabel)
			}
			if op.Kind == "move" && op.TargetLabel != "" {
				op.CurrentLocation = op.TargetLabel
			}
			if op.BytesTotal > 0 && op.BytesDone < op.BytesTotal {
				op.BytesDone = op.BytesTotal
			}
		}

		s.mu.Lock()
		s.cfg.UpsertOperation(op)
		if canceled && op.Kind == "move" {
			now := time.Now().Unix()
			for i := range s.cfg.Operations {
				q := &s.cfg.Operations[i]
				if q.Status == "en_cola" && (q.DeckHost == "" || q.DeckHost == op.DeckHost) {
					q.Status = "interrumpido"
					q.FinishedAt = now
					q.Message = "Cola detenida al cancelar el movimiento en curso."
				}
			}
		}
		snap := s.cfg.Snapshot()
		s.mu.Unlock()
		s.guardar(snap)

		s.jobMu.Lock()
		j.Prog = final
		s.jobMu.Unlock()

		s.broadcastOperations(snap.Operations)
		s.broadcast(map[string]any{"type": "progress", "job": j.ID, "title": j.Title, "kind": j.Kind, "progress": final})

		// advanceQueue primero y el reinicio aplazado despues: si fuera al
		// reves, entre el fin de este trabajo y el arranque del siguiente en
		// cola la lista pendiente se veria vacia y se reiniciaria a medias.
		go func() {
			if !canceled {
				s.advanceQueue()
			}
			s.maybeRestartAfterUpdate()
		}()
	}()

	return j, nil
}

// progressTracker recuerda el ultimo parte emitido para que el aviso de
// finalizacion conserve los contadores. Sin esto, el mensaje final llega con
// los bytes y los ficheros a cero y la interfaz pierde el resumen.
type progressTracker struct {
	report deck.ProgressFunc
	mu     sync.Mutex
	last   deck.Progress
}

func (t *progressTracker) Report(p deck.Progress) {
	t.mu.Lock()
	t.last = p
	t.mu.Unlock()
	t.report(p)
}

// snapshot devuelve el ultimo parte con su cerrojo puesto. Los llamantes de
// hoy leen cuando las gorutinas de la subida ya han terminado, pero last tiene
// dueno declarado (t.mu) y leerlo sin el es invitar a la proxima carrera.
func (t *progressTracker) snapshot() deck.Progress {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.last
}

// Finish emite el parte final reutilizando los contadores acumulados.
func (t *progressTracker) Finish(msg string) {
	t.mu.Lock()
	p := t.last
	t.mu.Unlock()
	p.Phase = "listo"
	p.Done = true
	p.Speed = 0
	p.File = ""
	p.Message = msg
	t.report(p)
}

func (s *Server) handleCancel(w http.ResponseWriter, r *http.Request) {
	s.jobMu.Lock()
	if s.job != nil && s.job.cancel != nil {
		s.job.cancel()
	}
	s.jobMu.Unlock()

	writeJSON(w, map[string]any{"ok": true})
}
