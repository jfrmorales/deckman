//go:build windows

package main

import (
	"os"
	"runtime"

	webview2 "github.com/jchv/go-webview2"
)

// El bucle de mensajes de la ventana tiene que vivir en el hilo principal del
// proceso; sin esto Windows puede congelar la ventana.
func init() { runtime.LockOSThread() }

// runWindow abre la interfaz en una ventana nativa (WebView2, el motor de
// Edge, presente de serie en Windows 10 y 11) y bloquea hasta que se cierre.
// Devuelve false si el runtime no esta disponible: quien llama cae al modo
// app de un navegador o, en ultima instancia, a una pestana.
//
// quit y stop pueden ser nil (al reabrir contra un deckman ya en marcha):
// entonces la ventana solo se cierra desde la propia ventana.
func runWindow(url string, quit <-chan struct{}, stop <-chan os.Signal) bool {
	w := webview2.NewWithOptions(webview2.WebViewOptions{
		AutoFocus: true,
		WindowOptions: webview2.WindowOptions{
			Title:  "deckman",
			Width:  1280,
			Height: 820,
			Center: true,
		},
	})
	if w == nil {
		return false
	}
	defer w.Destroy()

	// El boton Salir de la interfaz (o un Ctrl+C si hay consola) tambien debe
	// cerrar la ventana, no solo al reves.
	go func() {
		select {
		case <-quit:
		case <-stop:
		}
		w.Dispatch(w.Terminate)
	}()

	w.Navigate(url)
	w.Run()
	return true
}
