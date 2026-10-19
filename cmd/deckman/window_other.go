//go:build !windows

package main

import "os"

// En Linux y macOS no hay ventana nativa: un webview obligaria a CGO (WebKitGTK
// en Linux, que no esta garantizado en todas las distros; WebKit en macOS, que
// exige compilar en un Mac) y romperia la compilacion cruzada del contenedor.
// La ventana se consigue con el modo app de un navegador Chromium (appmode.go).
func runWindow(string, <-chan struct{}, <-chan os.Signal) bool { return false }
