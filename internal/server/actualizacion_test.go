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

	// Aplicar mientras hay un trabajo en curso devuelve 409.
	s.jobMu.Lock()
	s.job = &job{ID: "j1", Kind: "move", Title: "Moviendo", Prog: deck.Progress{Phase: "copiando"}}
	s.jobMu.Unlock()
	wBusy := apiReq(t, h, http.MethodPost, "/api/update/apply", `{}`)
	if wBusy.Code != http.StatusConflict {
		t.Fatalf("apply con trabajo en curso = %d, se esperaba 409", wBusy.Code)
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
