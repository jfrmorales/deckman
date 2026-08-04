// deckman gestiona los juegos de una Steam Deck desde el PC: ver que hay
// instalado, enviar juegos de Windows y ROMs, mover juegos entre el
// almacenamiento interno y la microSD, y liberar espacio.
//
// Arranca un servidor local y abre la interfaz en el navegador. No necesita
// nada instalado ni en el PC ni en la Deck: solo SSH activado en SteamOS.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	"github.com/jfrmorales/deckman/internal/server"
)

var version = "dev"

func main() {
	port := flag.Int("port", 8777, "puerto local de la interfaz")
	noBrowser := flag.Bool("no-browser", false, "no abrir la interfaz automaticamente")
	browserMode := flag.Bool("browser", false, "abrir la interfaz como pestana del navegador en vez de ventana propia")
	showVersion := flag.Bool("version", false, "mostrar la version y salir")
	flag.Parse()

	if *showVersion {
		fmt.Println("deckman", version)
		return
	}

	srv := server.New()
	srv.Version = version
	defer srv.Close()

	// Solo localhost: la interfaz puede borrar ficheros de la Deck, no tiene
	// por que estar accesible desde la red.
	// ¿Pidio el usuario un puerto concreto?
	portGiven := false
	flag.Visit(func(f *flag.Flag) {
		if f.Name == "port" {
			portGiven = true
		}
	})

	addr := fmt.Sprintf("127.0.0.1:%d", *port)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		// Si el puerto lo eligio el usuario, cambiarlo por detras solo confunde:
		// se quedaria hablando con lo que hubiera escuchando ahi (a menudo otro
		// deckman que se dejo abierto). Mejor decirlo y parar.
		if portGiven {
			fmt.Fprintf(os.Stderr, "error: el puerto %d esta ocupado.\n", *port)
			fmt.Fprintln(os.Stderr, "Puede que ya tengas otro deckman abierto. Cierralo, o elige otro puerto con -port.")
			os.Exit(1)
		}
		// Si lo que ocupa el puerto es otro deckman (lanzado dos veces desde el
		// escritorio, donde no se ve la consola), no arrancamos un segundo
		// servidor: abrimos el navegador contra el que ya corre y listo.
		if existing := fmt.Sprintf("http://%s/", addr); isDeckman(existing) {
			fmt.Println("Ya hay un deckman abierto en", existing)
			if !*noBrowser {
				if *browserMode || !reopenUI(existing) {
					openBrowser(existing)
				}
			}
			return
		}
		// Con el puerto por defecto si buscamos uno libre: es una comodidad,
		// no una sorpresa.
		fmt.Fprintf(os.Stderr, "aviso: el puerto %d esta ocupado, uso otro libre\n", *port)
		ln, err = net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			fmt.Fprintln(os.Stderr, "error: no se pudo abrir el puerto local:", err)
			os.Exit(1)
		}
	}
	url := fmt.Sprintf("http://%s/", ln.Addr().String())

	httpSrv := &http.Server{
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 15 * time.Second,
	}

	fmt.Println()
	fmt.Println("  deckman", version)
	fmt.Println("  Interfaz:", url)
	fmt.Println("  Deja esta ventana abierta. Ctrl+C para salir.")
	fmt.Println()

	go func() {
		if err := httpSrv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			fmt.Fprintln(os.Stderr, "error del servidor:", err)
			os.Exit(1)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	// Bloquea hasta que toque salir: Ctrl+C, el boton Salir de la interfaz, o
	// el cierre de la ventana de la aplicacion.
	runUI(url, srv, stop, *noBrowser, *browserMode)

	fmt.Println("\ncerrando...")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	// Se esta saliendo: si alguna peticion no termina en 5 segundos, el
	// proceso muere igual y no hay nada que informar.
	_ = httpSrv.Shutdown(ctx)
}

// isDeckman comprueba si lo que escucha en esa direccion es otro deckman,
// preguntandole por /api/state. Cualquier otro servidor (u otra version sin el
// campo "app") simplemente no contesta eso.
func isDeckman(base string) bool {
	client := &http.Client{Timeout: 2 * time.Second}
	req, err := http.NewRequest(http.MethodGet, base+"api/state", nil)
	if err != nil {
		return false
	}
	// El guard del servidor exige esta cabecera en toda la API, tambien en GET.
	req.Header.Set("X-Deckman", "1")
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	var st struct {
		App string `json:"app"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&st); err != nil {
		return false
	}
	return st.App == "deckman"
}

func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	case "darwin":
		cmd = exec.Command("open", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	if err := cmd.Start(); err != nil {
		fmt.Println("  (abre la direccion a mano en el navegador)")
	}
}
