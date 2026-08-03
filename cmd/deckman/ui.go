package main

import (
	"fmt"
	"os"
	"time"

	"github.com/jfrmorales/deckman/internal/server"
)

// runUI muestra la interfaz y bloquea hasta que toque salir: Ctrl+C, el boton
// Salir de la interfaz, o el cierre de la ventana de la aplicacion.
//
// La escalera de preferencia es: ventana nativa (solo Windows) → ventana modo
// app de un navegador Chromium → pestana del navegador de siempre. Con
// -browser se salta directo a la pestana, y con -no-browser no se abre nada.
func runUI(url string, srv *server.Server, stop <-chan os.Signal, headless, plainBrowser bool) {
	waitExit := func() {
		select {
		case <-stop:
		case <-srv.QuitRequested():
		}
	}

	if headless {
		waitExit()
		return
	}

	if !plainBrowser {
		if runWindow(url, srv.QuitRequested(), stop) {
			// Ventana nativa cerrada: se sale. Si habia una transferencia, se
			// corta, pero las copias son reanudables (se salta lo ya copiado).
			return
		}
		if proc := openAppWindow(url); proc != nil {
			windowClosed := make(chan struct{})
			go func() {
				proc.Wait()
				close(windowClosed)
			}()
			select {
			case <-stop:
				return
			case <-srv.QuitRequested():
				return
			case <-windowClosed:
			}
			if !srv.Busy() {
				return
			}
			// La ventana se cerro con una transferencia en marcha. Cortarla
			// porque si seria tirar lo ya copiado: deckman sigue en segundo
			// plano hasta acabarla (o hasta Ctrl+C), y luego se apaga solo.
			// Si se vuelve a abrir deckman mientras tanto, reutiliza este
			// servidor y la ventana vuelve a ensenar el progreso.
			fmt.Println("Ventana cerrada con un trabajo en curso; deckman sigue hasta terminarlo.")
			tick := time.NewTicker(15 * time.Second)
			defer tick.Stop()
			for {
				select {
				case <-stop:
					return
				case <-srv.QuitRequested():
					return
				case <-tick.C:
					if !srv.Busy() {
						return
					}
				}
			}
		}
	}

	openBrowser(url)
	waitExit()
}

// reopenUI abre la interfaz contra un deckman que ya estaba corriendo (el
// caso "lo he abierto dos veces"). En Windows bloquea mientras la ventana
// este abierta; en el resto solo lanza la ventana o pestana y vuelve.
func reopenUI(url string) bool {
	if runWindow(url, nil, nil) {
		return true
	}
	return openAppWindow(url) != nil
}
