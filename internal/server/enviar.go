package server

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jfrmorales/deckman/internal/config"
	"github.com/jfrmorales/deckman/internal/deck"
	"github.com/jfrmorales/deckman/internal/detect"
	"github.com/jfrmorales/deckman/internal/i18n"
)

// Enviar un juego o una ROM a la Deck: deteccion de la carpeta, registro del
// acceso directo y la copia.

// handleDetect examina una carpeta local y consulta la red para proponer
// nombre, ejecutable y version de Proton.
func (s *Server) handleDetect(w http.ResponseWriter, r *http.Request) {
	var req struct {
		LocalPath string `json:"localPath"`
		Online    *bool  `json:"online"`
		Override  string `json:"overrideAppId"` // para reintentar con otro candidato
	}
	if err := decode(r, &req); err != nil {
		writeErr(w, err, http.StatusBadRequest)
		return
	}
	if req.LocalPath == "" {
		writeErr(w, i18n.Errorf("indica la carpeta del juego"), http.StatusBadRequest)
		return
	}
	if fi, err := os.Stat(req.LocalPath); err != nil {
		writeErr(w, err, http.StatusBadRequest)
		return
	} else if !fi.IsDir() {
		// Un ejecutable suelto: no hay nada que rastrear mas alla del nombre.
		writeJSON(w, map[string]any{
			"local": &detect.Result{
				Dir:       req.LocalPath,
				CleanName: detect.CleanName(strings.TrimSuffix(filepath.Base(req.LocalPath), filepath.Ext(req.LocalPath))),
			},
		})
		return
	}

	local := detect.Detect(req.LocalPath)

	online := true
	if req.Online != nil {
		online = *req.Online
	}
	resp := map[string]any{"local": local}

	if online {
		ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
		defer cancel()

		appID := local.SteamAppID
		if req.Override != "" {
			appID = req.Override
		}
		resp["online"] = s.metaClient().Lookup(ctx, appID, local.CleanName)
	}
	writeJSON(w, resp)
}

// registerShortcut anade un juego no-Steam por la via que toque.
//
// Con Steam abierto SIEMPRE se le pide a Steam: editar shortcuts.vdf por
// detras mientras Steam corre no solo se pierde (Steam reescribe el fichero al
// salir desde su copia en memoria) sino que puede dejar el fichero con menos
// juegos de los que habia. Paso de verdad en una Deck: se anadio un juego,
// Steam se reinicio y quedo una sola entrada de siete.
//
// Devuelve el appid y si se hizo por la via de Steam.
func (s *Server) registerShortcut(ctx context.Context, c *deck.Client, opts deck.NonSteamOptions) (uint32, bool, error) {
	steamCorriendo := c.SteamRunning(ctx)

	if steamCorriendo {
		if !c.CEFAvailable(ctx) {
			return 0, false, i18n.Errorf(
				"Steam esta abierto pero no responde: cierralo en la Deck y vuelve a intentarlo, " +
					"o reinicialo y espera a que cargue del todo. Anadir el juego con Steam abierto " +
					"le haria perder los accesos directos que ya tienes")
		}
		appID, err := c.AddShortcutLive(ctx, opts.Name, opts.Exe, opts.StartDir, opts.LaunchOptions, opts.CompatTool)
		if err != nil {
			return 0, false, i18n.Errorf("no se pudo anadir el juego a traves de Steam: %w", err)
		}
		return appID, true, nil
	}

	// Con Steam cerrado el fichero es nuestro y se puede editar sin riesgo.
	appID, err := c.AddNonSteamGame(ctx, opts)
	return appID, false, err
}

// safeRelExe valida la ruta del ejecutable dentro de la carpeta del juego y la
// devuelve normalizada con barras.
//
// No es paranoia: de esta ruta sale el StartDir que se guarda en el acceso
// directo, y StartDir es exactamente lo que "Eliminar" le pasa a un rm -rf en
// la Deck. Un "../.." colocaria ese borrado fuera de ~/Games.
func safeRelExe(exe string) (string, error) {
	// El navegador puede estar en Windows y mandar "bin\juego.exe".
	clean := strings.ReplaceAll(strings.TrimSpace(exe), `\`, "/")
	malo := i18n.Errorf("la ruta del ejecutable tiene que ser relativa a la carpeta del juego, sin \"..\": %q", exe)
	if clean == "" || strings.HasPrefix(clean, "/") || strings.Contains(clean, ":") {
		return "", malo
	}
	for _, seg := range strings.Split(clean, "/") {
		if seg == ".." {
			return "", malo
		}
	}
	// IsLocal remata: descarta rutas absolutas, escapes y los nombres
	// reservados de Windows.
	if !filepath.IsLocal(filepath.FromSlash(clean)) {
		return "", malo
	}
	return clean, nil
}

func (s *Server) handleSendGame(w http.ResponseWriter, r *http.Request) {
	var req struct {
		LocalPath     string `json:"localPath"`
		Name          string `json:"name"`
		Exe           string `json:"exe"` // relativa dentro de la carpeta
		CompatTool    string `json:"compatTool"`
		LaunchOptions string `json:"launchOptions"`
		AddShortcut   bool   `json:"addShortcut"`
		SteamAppID    string `json:"steamAppId"` // para buscar las caratulas
		Artwork       bool   `json:"artwork"`
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
	if req.LocalPath == "" {
		writeErr(w, i18n.Errorf("elige la carpeta del juego"), http.StatusBadRequest)
		return
	}
	info, err := os.Stat(req.LocalPath)
	if err != nil {
		writeErr(w, err, http.StatusBadRequest)
		return
	}
	if req.Name == "" {
		req.Name = filepath.Base(req.LocalPath)
	}
	if req.AddShortcut && req.Exe == "" {
		writeErr(w, i18n.Errorf("elige el ejecutable del juego para poder anadirlo a Steam"), http.StatusBadRequest)
		return
	}
	exeRel := ""
	if req.Exe != "" {
		exeRel, err = safeRelExe(req.Exe)
		if err != nil {
			writeErr(w, err, http.StatusBadRequest)
			return
		}
	}

	s.mu.Lock()
	s.cfg.LastLocal = filepath.Dir(req.LocalPath)
	cfg := s.cfg.Snapshot()
	s.mu.Unlock()
	s.guardar(cfg)

	gamesDir := c.Home + "/Games"
	folderName := filepath.Base(filepath.Clean(req.LocalPath))
	remoteBase := gamesDir + "/" + folderName

	j, err := s.startJobOp(config.Operation{
		Kind:            "send-game",
		Title:           "Enviando " + req.Name,
		GameName:        req.Name,
		TargetLabel:     "Interno",
		CurrentLocation: "Interno",
		LocalPath:       req.LocalPath,
	}, func(ctx context.Context, report deck.ProgressFunc) error {
		tr := &progressTracker{report: report}
		if err := c.MkdirAll(gamesDir); err != nil {
			return err
		}
		if !info.IsDir() {
			// Un ejecutable suelto va a su propia carpeta.
			remoteBase = gamesDir + "/" + strings.TrimSuffix(folderName, filepath.Ext(folderName))
			if err := c.MkdirAll(remoteBase); err != nil {
				return err
			}
			if err := c.UploadTree(ctx, req.LocalPath, remoteBase, tr.Report); err != nil {
				return err
			}
		} else if err := c.UploadTree(ctx, req.LocalPath, gamesDir, tr.Report); err != nil {
			return err
		}

		if !req.AddShortcut {
			tr.Finish("Ficheros copiados a " + remoteBase)
			return nil
		}

		reg := tr.snapshot()
		reg.Phase, reg.Done, reg.File = "registrando en Steam", false, ""
		reg.Message = "escribiendo shortcuts.vdf"
		tr.Report(reg)
		exeRemote := remoteBase + "/" + exeRel
		if !info.IsDir() {
			exeRemote = remoteBase + "/" + folderName
		}
		startDir := exeRemote[:strings.LastIndex(exeRemote, "/")]

		appID, viaSteam, err := s.registerShortcut(ctx, c, deck.NonSteamOptions{
			Name:          req.Name,
			Exe:           exeRemote,
			StartDir:      startDir,
			LaunchOptions: req.LaunchOptions,
			CompatTool:    req.CompatTool,
		})
		if err != nil {
			return err
		}
		msg := fmt.Sprintf("%s anadido a Steam (id %d).", req.Name, appID)
		if viaSteam {
			msg += " Ya aparece en la biblioteca, sin reiniciar."
		}
		if req.Artwork {
			reg := tr.snapshot()
			reg.Phase, reg.Done, reg.File = "descargando caratulas", false, ""
			reg.Message = "consultando SteamGridDB"
			tr.Report(reg)
			msg += " " + s.installArtwork(ctx, c, appID, req.SteamAppID, req.Name)
		}
		if !viaSteam {
			msg += " Reinicia Steam en la Deck para verlo en la biblioteca."
		}
		tr.Finish(msg)
		return nil
	})
	if err != nil {
		writeErr(w, err, http.StatusConflict)
		return
	}
	writeJSON(w, map[string]any{"ok": true, "job": j.ID})
}

func (s *Server) handleSendROM(w http.ResponseWriter, r *http.Request) {
	var req struct {
		LocalPath string `json:"localPath"`
		RomsDir   string `json:"romsDir"`
		System    string `json:"system"`
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
	if req.LocalPath == "" || req.System == "" {
		writeErr(w, i18n.Errorf("elige la ROM y el sistema de destino"), http.StatusBadRequest)
		return
	}

	s.mu.Lock()
	s.cfg.LastLocal = filepath.Dir(req.LocalPath)
	cfg := s.cfg.Snapshot()
	s.mu.Unlock()
	s.guardar(cfg)

	j, err := s.startJobOp(config.Operation{
		Kind:            "send-rom",
		Title:           "Enviando ROM a " + req.System,
		GameName:        filepath.Base(req.LocalPath),
		TargetLabel:     req.System,
		CurrentLocation: req.System,
		LocalPath:       req.LocalPath,
	}, func(ctx context.Context, report deck.ProgressFunc) error {
		return c.UploadROM(ctx, req.LocalPath, req.RomsDir, req.System, report)
	})
	if err != nil {
		writeErr(w, err, http.StatusConflict)
		return
	}
	writeJSON(w, map[string]any{"ok": true, "job": j.ID})
}
