package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/jfrmorales/deckman/internal/config"
	"github.com/jfrmorales/deckman/internal/deck"
	"github.com/jfrmorales/deckman/internal/i18n"
)

// Conectar con una Deck, recordarla y olvidarla.

func (s *Server) handleConnect(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Host     string `json:"host"`
		Port     int    `json:"port"`
		User     string `json:"user"`
		Name     string `json:"name"` // etiqueta opcional, para distinguir varias Decks
		Password string `json:"password"`
		// ConfiarClave llega solo cuando el usuario ya ha visto el aviso de
		// clave de host cambiada y ha dicho que si. Nunca viene en el primer
		// intento: la interfaz lo manda al reintentar.
		ConfiarClave bool `json:"confiarClave"`
	}
	if err := decode(r, &req); err != nil {
		writeErr(w, err, http.StatusBadRequest)
		return
	}
	if req.Host == "" {
		writeErr(w, i18n.Errorf("indica la IP de SteamOS"), http.StatusBadRequest)
		return
	}
	if req.Port == 0 {
		req.Port = 22
	}
	if req.User == "" {
		req.User = "deck"
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	creds := deck.Credentials{
		Host: req.Host, Port: req.Port, User: req.User, Password: req.Password,
		KnownHostsPath:    config.KnownHostsPath(),
		ConfiarClaveNueva: req.ConfiarClave,
	}

	// Si ya tenemos clave instalada, la preferimos y no pedimos contrasena.
	s.mu.RLock()
	existingKey := s.cfg.KeyPath
	s.mu.RUnlock()
	if existingKey != "" {
		if _, err := os.Stat(existingKey); err == nil {
			creds.KeyPath = existingKey
		}
	}

	client, err := deck.Connect(ctx, creds)
	if err != nil {
		// La clave de host cambiada no es un fallo mas: lleva una pregunta
		// detras. Va con su propio distintivo para que la interfaz pueda
		// enseñar las huellas y ofrecer volver a confiar, en vez de dejar al
		// usuario con un error del que no se sale.
		var cambiada *deck.ClaveDeHostCambiada
		if errors.As(err, &cambiada) {
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.WriteHeader(http.StatusConflict)
			json.NewEncoder(w).Encode(map[string]any{
				"error":            i18n.Traducir(err, idioma()),
				"claveHostCambio":  true,
				"huellaPresentada": cambiada.Presentada,
				"huellaRecordada":  cambiada.Recordada,
			})
			return
		}
		writeErr(w, err, http.StatusBadGateway)
		return
	}

	// Dejamos instalada nuestra clave publica para no depender de la contrasena
	// en adelante.
	var keyNote string
	keyPath, pubKey, kerr := config.EnsureKey()
	if kerr == nil {
		if err := client.InstallPublicKey(ctx, pubKey); err == nil {
			s.mu.Lock()
			s.cfg.KeyPath = keyPath
			s.mu.Unlock()
			keyNote = "clave SSH instalada: las proximas conexiones no pediran contrasena"
		} else {
			keyNote = "conectado, pero no se pudo instalar la clave SSH: " + err.Error()
		}
	}

	s.mu.Lock()
	if s.client != nil {
		s.client.Close()
	}
	s.client = client
	s.cfg.UpsertDeck(config.Deck{Host: req.Host, Port: req.Port, User: req.User, Name: req.Name})
	cfg := s.cfg.Snapshot()
	s.mu.Unlock()
	s.guardar(cfg)

	writeJSON(w, map[string]any{"ok": true, "home": client.Home, "note": keyNote})
}

// handleForgetDeck olvida una Deck guardada y, sobre todo, le retira la clave
// SSH que deckman le instalo.
//
// Borrarla solo de la configuracion local no valdria: la clave se quedaria en
// authorized_keys de la Deck dando acceso permanente desde este PC, y el
// usuario creeria que ha cortado el acceso. Si la Deck no se puede alcanzar
// para revocarla, se dice claramente y se explica como quitarla a mano, en vez
// de callarlo.
func (s *Server) handleForgetDeck(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "solo POST", http.StatusMethodNotAllowed)
		return
	}
	if s.Busy() {
		writeErr(w, i18n.Errorf("hay una operacion en curso; espera a que termine antes de olvidar una Deck"), http.StatusConflict)
		return
	}
	var req struct {
		Index int `json:"index"`
	}
	if err := decode(r, &req); err != nil {
		writeErr(w, err, http.StatusBadRequest)
		return
	}

	s.mu.RLock()
	if req.Index < 0 || req.Index >= len(s.cfg.Decks) {
		s.mu.RUnlock()
		writeErr(w, i18n.Errorf("esa Deck ya no esta en la lista"), http.StatusBadRequest)
		return
	}
	objetivo := s.cfg.Decks[req.Index]
	activa := s.cfg.ActiveDeck()
	conectado := s.client != nil
	keyPath := s.cfg.KeyPath
	s.mu.RUnlock()

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	// La clave publica que hay que retirar es la nuestra. Si no la tenemos, no
	// hay nada que revocar (nunca se llego a instalar).
	var pubKey string
	if keyPath != "" {
		if _, pub, err := config.EnsureKey(); err == nil {
			pubKey = pub
		}
	}

	aviso := ""
	revocada := false
	if pubKey != "" {
		// Con la Deck delante se usa la sesion abierta; si no, se abre una con
		// la propia clave que vamos a retirar. Para eso esta.
		var c *deck.Client
		if conectado && activa.Same(objetivo) {
			s.mu.RLock()
			c = s.client
			s.mu.RUnlock()
		} else {
			nuevo, err := deck.Connect(ctx, deck.Credentials{
				Host: objetivo.Host, Port: objetivo.Port, User: objetivo.User, KeyPath: keyPath,
				KnownHostsPath: config.KnownHostsPath(),
			})
			if err == nil {
				defer nuevo.Close()
				c = nuevo
			} else {
				aviso = fmt.Sprintf("no se pudo conectar con %s para retirar la clave (%v). "+
					"La quito de la lista igualmente, pero la clave sigue en esa Deck: "+
					"para retirarla, borra la linea que acaba en %q de ~/.ssh/authorized_keys",
					objetivo.Label(), err, comentarioClave(pubKey))
			}
		}
		if c != nil {
			n, err := c.RemovePublicKey(ctx, pubKey)
			switch {
			case err != nil:
				aviso = "no se pudo retirar la clave de la Deck: " + err.Error()
			case n > 0:
				revocada = true
			default:
				aviso = "la clave ya no estaba en esa Deck"
			}
		}
	}

	s.mu.Lock()
	// La lista ha podido cambiar mientras hablabamos con la Deck.
	if req.Index >= len(s.cfg.Decks) || !s.cfg.Decks[req.Index].Same(objetivo) {
		s.mu.Unlock()
		writeErr(w, i18n.Errorf("esa Deck ya no esta en la lista"), http.StatusConflict)
		return
	}
	s.cfg.RemoveDeck(req.Index)

	// Si era la que estaba conectada, se cierra la sesion: seguir usandola con
	// la clave ya revocada solo lleva a errores raros al primer reintento.
	if conectado && activa.Same(objetivo) && s.client != nil {
		s.client.Close()
		s.client = nil
	}

	// La clave local es una sola para todas las Decks. Solo se borra al
	// olvidar la ultima; hacerlo antes dejaria a las demas con una clave
	// instalada que este PC ya no tiene.
	if len(s.cfg.Decks) == 0 && s.cfg.KeyPath != "" {
		os.Remove(s.cfg.KeyPath)
		os.Remove(s.cfg.KeyPath + ".pub")
		s.cfg.KeyPath = ""
	}
	cfg := s.cfg.Snapshot()
	s.mu.Unlock()
	s.guardar(cfg)

	// Fuera tambien lo que recordabamos de su clave de host. Si no, volver a
	// añadir esa Deck despues de reinstalarle SteamOS saldria con el aviso de
	// clave cambiada, que en ese momento no significa nada y solo enseña a
	// darle a "confiar" sin leer.
	if err := deck.OlvidarClaveDeHost(config.KnownHostsPath(), objetivo.Host, objetivo.Port); err != nil {
		s.logf("no se pudo olvidar la clave de host de %s: %v", objetivo.Label(), err)
	}

	writeJSON(w, map[string]any{
		"ok":       true,
		"revocada": revocada,
		"aviso":    aviso,
		"decks":    cfg.Decks,
		"active":   cfg.Active,
	})
}

// comentarioClave saca el comentario final de una clave publica (deckman@pc),
// que es por lo que el usuario la reconoce en authorized_keys.
func comentarioClave(pubKey string) string {
	campos := strings.Fields(strings.TrimSpace(pubKey))
	if len(campos) < 3 {
		return "deckman@"
	}
	return campos[len(campos)-1]
}
