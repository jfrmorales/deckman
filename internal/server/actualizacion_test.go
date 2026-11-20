package server

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"context"
	"sync/atomic"

	"github.com/jfrmorales/deckman/internal/config"
	"github.com/jfrmorales/deckman/internal/deck"
	"github.com/jfrmorales/deckman/internal/update"
)

func apiReq(t *testing.T, h http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	var rd *strings.Reader
	if body != "" {
		rd = strings.NewReader(body)
	} else {
		rd = strings.NewReader("")
	}
	req := httptest.NewRequest(method, path, rd)
	req.Host = "127.0.0.1:8777"
	req.Header.Set("X-Deckman", "1")
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

func TestUpdateSettingsAndGuards(t *testing.T) {
	s := nuevoServidorDePrueba(t)
	s.Version = "v0.8.0"
	h := s.Handler()

	// Por defecto autoCheckUpdates es true.
	w := apiReq(t, h, http.MethodGet, "/api/state", "")
	if w.Code != http.StatusOK {
		t.Fatalf("/api/state = %d", w.Code)
	}
	var st struct {
		AutoCheckUpdates bool        `json:"autoCheckUpdates"`
		Update           UpdateState `json:"update"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &st); err != nil {
		t.Fatal(err)
	}
	if !st.AutoCheckUpdates {
		t.Fatal("autoCheckUpdates deberia ser true por defecto")
	}

	// Cambiar autoCheckUpdates a false y comprobar persistencia en /api/state.
	wSet := apiReq(t, h, http.MethodPost, "/api/settings", `{"autoCheckUpdates":false}`)
	if wSet.Code != http.StatusOK {
		t.Fatalf("/api/settings = %d (%s)", wSet.Code, wSet.Body.String())
	}
	w2 := apiReq(t, h, http.MethodGet, "/api/state", "")
	if err := json.Unmarshal(w2.Body.Bytes(), &st); err != nil {
		t.Fatal(err)
	}
	if st.AutoCheckUpdates {
		t.Fatal("autoCheckUpdates no cambio a false")
	}

	// Aplicar sin comprobar antes devuelve 400.
	wApply := apiReq(t, h, http.MethodPost, "/api/update/apply", `{}`)
	if wApply.Code != http.StatusBadRequest {
		t.Fatalf("apply sin check = %d, se esperaba 400", wApply.Code)
	}

	// Con un trabajo en curso, aplicar ya no se niega por eso: sigue siendo 400
	// aqui solo porque no hay ninguna actualizacion comprobada.
	s.jobMu.Lock()
	s.job = &job{ID: "j1", Kind: "move", Title: "Moviendo", Prog: deck.Progress{Phase: "copiando"}}
	s.jobMu.Unlock()
	wBusy := apiReq(t, h, http.MethodPost, "/api/update/apply", `{}`)
	if wBusy.Code != http.StatusBadRequest {
		t.Fatalf("apply con trabajo en curso = %d, se esperaba 400 (sin actualizacion lista)", wBusy.Code)
	}
}

func TestUpdateCheckAndApplyFlow(t *testing.T) {
	oldBin := []byte("old-binary-v0.8.0")
	newBin := []byte("new-binary-v0.8.1")
	sum := sha256.Sum256(newBin)
	assetName := "deckman-0.8.1-linux-amd64"
	sumsBody := []byte(hex.EncodeToString(sum[:]) + "  " + assetName + "\n")

	var relSrv *httptest.Server
	relSrv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/latest":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"tag_name": "v0.8.1",
				"html_url": "https://github.com/jfrmorales/deckman/releases/tag/v0.8.1",
				"body":     "Mejoras de prueba",
				"assets": []map[string]any{
					{"name": assetName, "size": len(newBin), "browser_download_url": relSrv.URL + "/bin"},
					{"name": "SHA256SUMS", "size": len(sumsBody), "browser_download_url": relSrv.URL + "/sums"},
				},
			})
		case "/sums":
			_, _ = w.Write(sumsBody)
		case "/bin":
			_, _ = w.Write(newBin)
		default:
			http.NotFound(w, r)
		}
	}))
	defer relSrv.Close()

	exePath := filepath.Join(t.TempDir(), "deckman")
	if err := os.WriteFile(exePath, oldBin, 0o755); err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(relSrv.URL)
	if err != nil {
		t.Fatal(err)
	}

	s := nuevoServidorDePrueba(t)
	s.Version = "v0.8.0"
	s.updRestartDelay = 10 * time.Millisecond

	u := update.New("v0.8.0")
	u.ReleaseURL = relSrv.URL + "/latest"
	u.AllowURL = func(raw string) error {
		pu, err := url.Parse(raw)
		if err != nil || pu.Host != parsed.Host {
			return fmt.Errorf("host invalido: %s", raw)
		}
		return nil
	}
	u.GOOS = "linux"
	u.GOARCH = "amd64"
	u.InFlatpak = func() bool { return false }
	u.ExecutablePath = func() (string, error) { return exePath, nil }
	s.updater = u

	// Suscriptor SSE en memoria para verificar la emision de eventos de actualizacion.
	evCh := make(chan []byte, 32)
	s.listMu.Lock()
	s.listeners[evCh] = true
	s.listMu.Unlock()
	defer func() {
		s.listMu.Lock()
		delete(s.listeners, evCh)
		close(evCh)
		s.listMu.Unlock()
	}()

	h := s.Handler()

	wChk := apiReq(t, h, http.MethodPost, "/api/update/check", `{}`)
	if wChk.Code != http.StatusOK {
		t.Fatalf("/api/update/check = %d (%s)", wChk.Code, wChk.Body.String())
	}
	var chk UpdateState
	if err := json.Unmarshal(wChk.Body.Bytes(), &chk); err != nil {
		t.Fatal(err)
	}
	if !chk.Available || !chk.CanApply || chk.Latest != "v0.8.1" || chk.Status != "available" {
		t.Fatalf("estado tras check inesperado: %+v", chk)
	}

	// Reflejo en /api/state.
	wSt := apiReq(t, h, http.MethodGet, "/api/state", "")
	var st struct {
		Update UpdateState `json:"update"`
	}
	if err := json.Unmarshal(wSt.Body.Bytes(), &st); err != nil {
		t.Fatal(err)
	}
	if !st.Update.Available || st.Update.Latest != "v0.8.1" {
		t.Fatalf("state.update inesperado: %+v", st.Update)
	}

	wApply := apiReq(t, h, http.MethodPost, "/api/update/apply", `{}`)
	if wApply.Code != http.StatusOK {
		t.Fatalf("/api/update/apply = %d (%s)", wApply.Code, wApply.Body.String())
	}

	select {
	case <-s.QuitRequested():
	case <-time.After(5 * time.Second):
		t.Fatal("timeout esperando a QuitRequested tras actualizar")
	}

	if !s.RestartRequested() {
		t.Fatal("RestartRequested deberia ser true")
	}
	gotBin, err := os.ReadFile(exePath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(gotBin, newBin) {
		t.Fatalf("binario tras apply = %q, esperaba %q", gotBin, newBin)
	}

	sawRestarting := false
	for {
		select {
		case msg := <-evCh:
			var ev struct {
				Type   string      `json:"type"`
				Update UpdateState `json:"update"`
			}
			if json.Unmarshal(msg, &ev) == nil && ev.Type == "update" && ev.Update.Status == "restarting" {
				sawRestarting = true
			}
		default:
			goto doneDrain
		}
	}
doneDrain:
	if !sawRestarting {
		t.Fatal("no se emitio el evento SSE con status=restarting")
	}
}

// servidorConActualizacionLista monta un servidor con un updater falso contra
// un servidor de releases local, y deja hecha la comprobacion: lo siguiente es
// llamar a /api/update/apply.
func servidorConActualizacionLista(t *testing.T) (*Server, http.Handler) {
	t.Helper()
	newBin := []byte("new-binary-v0.8.1")
	sum := sha256.Sum256(newBin)
	assetName := "deckman-0.8.1-linux-amd64"
	sumsBody := []byte(hex.EncodeToString(sum[:]) + "  " + assetName + "\n")

	var relSrv *httptest.Server
	relSrv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/latest":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"tag_name": "v0.8.1",
				"html_url": "https://github.com/jfrmorales/deckman/releases/tag/v0.8.1",
				"assets": []map[string]any{
					{"name": assetName, "size": len(newBin), "browser_download_url": relSrv.URL + "/bin"},
					{"name": "SHA256SUMS", "size": len(sumsBody), "browser_download_url": relSrv.URL + "/sums"},
				},
			})
		case "/sums":
			_, _ = w.Write(sumsBody)
		case "/bin":
			_, _ = w.Write(newBin)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(relSrv.Close)

	exePath := filepath.Join(t.TempDir(), "deckman")
	if err := os.WriteFile(exePath, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	parsed, _ := url.Parse(relSrv.URL)

	s := nuevoServidorDePrueba(t)
	s.Version = "v0.8.0"
	s.updRestartDelay = time.Millisecond
	u := update.New("v0.8.0")
	u.ReleaseURL = relSrv.URL + "/latest"
	u.AllowURL = func(raw string) error {
		pu, err := url.Parse(raw)
		if err != nil || pu.Host != parsed.Host {
			return fmt.Errorf("host invalido: %s", raw)
		}
		return nil
	}
	u.GOOS = "linux"
	u.GOARCH = "amd64"
	u.InFlatpak = func() bool { return false }
	u.ExecutablePath = func() (string, error) { return exePath, nil }
	s.updater = u

	h := s.Handler()
	if w := apiReq(t, h, http.MethodPost, "/api/update/check", `{}`); w.Code != http.StatusOK {
		t.Fatalf("check = %d (%s)", w.Code, w.Body.String())
	}
	return s, h
}

func esperarEstadoUpdate(t *testing.T, s *Server, status string) UpdateState {
	t.Helper()
	fin := time.Now().Add(5 * time.Second)
	for time.Now().Before(fin) {
		if st := s.updateSnapshot(); st.Status == status {
			return st
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("el estado de la actualizacion nunca llego a %q (esta en %q)", status, s.updateSnapshot().Status)
	return UpdateState{}
}

func quitPedido(s *Server) bool {
	select {
	case <-s.QuitRequested():
		return true
	default:
		return false
	}
}

// lanzaTrabajoBloqueado arranca un trabajo que no termina hasta que se cierre
// el canal (o se cancele). Devuelve el canal para soltarlo.
func lanzaTrabajoBloqueado(t *testing.T, s *Server, titulo string) chan struct{} {
	t.Helper()
	suelta := make(chan struct{})
	_, err := s.startJob("copy", titulo, func(ctx context.Context, report deck.ProgressFunc) error {
		select {
		case <-suelta:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	return suelta
}

func encolaMovimiento(s *Server, id, host string) {
	s.mu.Lock()
	s.cfg.UpsertOperation(config.Operation{ID: id, Kind: "move", Title: "Moviendo " + id, Status: "en_cola", DeckHost: host})
	s.mu.Unlock()
}

func TestUpdateApplyWithActiveJobDefersRestart(t *testing.T) {
	s, h := servidorConActualizacionLista(t)
	suelta := lanzaTrabajoBloqueado(t, s, "Copiando")

	if w := apiReq(t, h, http.MethodPost, "/api/update/apply", `{}`); w.Code != http.StatusOK {
		t.Fatalf("apply con trabajo en curso = %d (%s)", w.Code, w.Body.String())
	}
	st := esperarEstadoUpdate(t, s, "pending_restart")
	if len(st.Pending) != 1 || st.Pending[0].Status != "en_curso" || st.Pending[0].Title != "Copiando" {
		t.Fatalf("Pending inesperado: %+v", st.Pending)
	}
	time.Sleep(50 * time.Millisecond)
	if s.RestartRequested() || quitPedido(s) {
		t.Fatal("no deberia reiniciar mientras el trabajo sigue")
	}

	close(suelta)
	select {
	case <-s.QuitRequested():
	case <-time.After(5 * time.Second):
		t.Fatal("no reinicio al acabar el trabajo")
	}
	if !s.RestartRequested() {
		t.Fatal("RestartRequested deberia ser true")
	}
}

func TestUpdateApplyWaitsForQueuedMoves(t *testing.T) {
	s, h := servidorConActualizacionLista(t)
	// Sin Deck conectada advanceQueue no arranca nada, asi que la cola solo
	// baja cuando se quita: justo lo que se quiere comprobar.
	encolaMovimiento(s, "otra", "otra-deck") // de otra Deck: no cuenta
	encolaMovimiento(s, "mia", "")

	if w := apiReq(t, h, http.MethodPost, "/api/update/apply", `{}`); w.Code != http.StatusOK {
		t.Fatalf("apply = %d", w.Code)
	}
	st := esperarEstadoUpdate(t, s, "pending_restart")
	if len(st.Pending) != 1 || st.Pending[0].ID != "mia" || st.Pending[0].Status != "en_cola" {
		t.Fatalf("Pending deberia ser solo la op en cola de esta Deck: %+v", st.Pending)
	}
	time.Sleep(50 * time.Millisecond)
	if quitPedido(s) {
		t.Fatal("reinicio con un movimiento en cola")
	}

	// Quitarlo de la cola libera el reinicio.
	if w := apiReq(t, h, http.MethodPost, "/api/operations/clear", `{"id":"mia"}`); w.Code != http.StatusOK {
		t.Fatalf("clear = %d (%s)", w.Code, w.Body.String())
	}
	select {
	case <-s.QuitRequested():
	case <-time.After(5 * time.Second):
		t.Fatal("no reinicio al vaciarse la cola")
	}
}

func TestUpdateApplyOtherHostQueueDoesNotCount(t *testing.T) {
	s, h := servidorConActualizacionLista(t)
	encolaMovimiento(s, "otra", "otra-deck")
	if w := apiReq(t, h, http.MethodPost, "/api/update/apply", `{}`); w.Code != http.StatusOK {
		t.Fatalf("apply = %d", w.Code)
	}
	select {
	case <-s.QuitRequested():
	case <-time.After(5 * time.Second):
		t.Fatal("una op en cola de otra Deck no deberia retener el reinicio")
	}
}

func TestUpdateRestartNowForcesAndCancelsJob(t *testing.T) {
	s, h := servidorConActualizacionLista(t)

	// Fuera de pending_restart no vale.
	if w := apiReq(t, h, http.MethodPost, "/api/update/restart", `{}`); w.Code != http.StatusConflict {
		t.Fatalf("restart sin pendiente = %d, se esperaba 409", w.Code)
	}

	lanzaTrabajoBloqueado(t, s, "Copiando")
	apiReq(t, h, http.MethodPost, "/api/update/apply", `{}`)
	esperarEstadoUpdate(t, s, "pending_restart")

	if w := apiReq(t, h, http.MethodPost, "/api/update/restart", `{}`); w.Code != http.StatusOK {
		t.Fatalf("restart = %d (%s)", w.Code, w.Body.String())
	}
	select {
	case <-s.QuitRequested():
	case <-time.After(5 * time.Second):
		t.Fatal("el reinicio forzado no se pidio")
	}
	if !s.RestartRequested() {
		t.Fatal("RestartRequested deberia ser true")
	}
	if s.jobBusy() {
		t.Fatal("el trabajo en curso deberia haberse cancelado")
	}
}

func TestUpdateCheckRejectedWhilePendingRestart(t *testing.T) {
	s := nuevoServidorDePrueba(t)
	s.updMu.Lock()
	s.updState.Status = "pending_restart"
	s.updMu.Unlock()
	if w := apiReq(t, s.Handler(), http.MethodPost, "/api/update/check", `{}`); w.Code != http.StatusConflict {
		t.Fatalf("check en pending_restart = %d, se esperaba 409", w.Code)
	}
	if st := s.updateSnapshot(); st.Status != "pending_restart" {
		t.Fatalf("el estado no deberia cambiar: %q", st.Status)
	}
}

func TestCancelDoesNotCancelUpdateDownload(t *testing.T) {
	s := nuevoServidorDePrueba(t)
	h := s.Handler()
	var cancelada atomic.Bool
	s.updMu.Lock()
	s.updCancel = func() { cancelada.Store(true) }
	s.updMu.Unlock()

	if w := apiReq(t, h, http.MethodPost, "/api/cancel", `{}`); w.Code != http.StatusOK {
		t.Fatalf("/api/cancel = %d", w.Code)
	}
	if cancelada.Load() {
		t.Fatal("/api/cancel no deberia cancelar la descarga de la actualizacion")
	}
	if w := apiReq(t, h, http.MethodPost, "/api/update/cancel", `{}`); w.Code != http.StatusOK {
		t.Fatalf("/api/update/cancel = %d", w.Code)
	}
	if !cancelada.Load() {
		t.Fatal("/api/update/cancel deberia cancelar la descarga")
	}
}

func TestStartJobAllowedExceptWhileRestarting(t *testing.T) {
	for _, estado := range []string{"applying", "pending_restart"} {
		s := nuevoServidorDePrueba(t)
		s.updMu.Lock()
		s.updState.Status = estado
		s.updMu.Unlock()
		suelta := lanzaTrabajoBloqueado(t, s, "Copiando en "+estado)
		close(suelta)
	}

	s := nuevoServidorDePrueba(t)
	s.updMu.Lock()
	s.updState.Status = "restarting"
	s.updMu.Unlock()
	if _, err := s.startJob("copy", "x", func(ctx context.Context, report deck.ProgressFunc) error { return nil }); err == nil {
		t.Fatal("startJob deberia rechazar trabajos mientras se reinicia")
	}
}
