package main

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"syscall"
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
			if runtime.GOOS == "darwin" {
				// En macOS cerrar la ultima ventana no cierra la aplicacion:
				// Chrome sigue vivo en el Dock y proc.Wait no vuelve nunca.
				// Lo que si desaparece es la conexion de la interfaz, y eso es
				// lo que se vigila. Al salir se cierra esa instancia, que con
				// su perfil propio no es la del usuario y nadie mas la usa.
				defer cerrarNavegador(proc)
				esperarAbandono(srv.Clientes, srv.Busy, srv.QuitRequested(), stop, graciaAbandono, time.Second)
				return
			}
			windowClosed := make(chan struct{})
			go func() {
				_ = proc.Wait()
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

	// Con una pestana normal no hay proceso propio que vigilar: cerrada la
	// pestana, deckman se quedaba vivo en segundo plano para siempre, y
	// lanzado desde el menu del escritorio ni siquiera hay consola donde
	// pulsar Ctrl+C. Se apaga cuando la interfaz lleva un rato cerrada.
	openBrowser(url)
	esperarAbandono(srv.Clientes, srv.Busy, srv.QuitRequested(), stop, graciaAbandono, time.Second)
}

// graciaAbandono es cuanto tiene que pasar sin ninguna interfaz conectada para
// darla por cerrada. Holgado a proposito: recargar la pagina, o que el
// navegador tarde en reconectar tras suspender el PC, corta la conexion unos
// segundos y eso no es cerrar.
const graciaAbandono = 60 * time.Second

// esperarAbandono bloquea hasta que toque salir: Ctrl+C, el boton Salir, o
// que la interfaz lleve gracia sin conectar y no haya ningun trabajo en marcha.
//
// Solo cuenta despues de haber visto alguna interfaz conectada: si el
// navegador no llego a abrirse y el usuario escribe la direccion a mano,
// deckman tiene que seguir esperandole.
func esperarAbandono(clientes func() int, ocupado func() bool, quit <-chan struct{}, stop <-chan os.Signal, gracia, paso time.Duration) {
	tick := time.NewTicker(paso)
	defer tick.Stop()
	visto := false
	var sinNadieDesde time.Time
	for {
		select {
		case <-stop:
			return
		case <-quit:
			return
		case ahora := <-tick.C:
			if clientes() > 0 {
				visto = true
				sinNadieDesde = time.Time{}
				continue
			}
			if !visto || ocupado() {
				sinNadieDesde = time.Time{}
				continue
			}
			if sinNadieDesde.IsZero() {
				sinNadieDesde = ahora
			}
			if ahora.Sub(sinNadieDesde) >= gracia {
				fmt.Println("La interfaz lleva un rato cerrada; deckman se apaga.")
				return
			}
		}
	}
}

// cerrarNavegador termina la instancia del modo app que lanzo deckman. Con
// SIGTERM y no con Kill: Chrome lo trata como un cierre normal y no ensena el
// aviso de "no se cerro correctamente" la proxima vez.
func cerrarNavegador(proc *exec.Cmd) {
	if proc.Process != nil {
		_ = proc.Process.Signal(syscall.SIGTERM)
	}
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
