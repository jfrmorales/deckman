//go:build !windows

package main

import "os"

// En Linux no hay ventana nativa: un webview obligaria a CGO y a WebKitGTK,
// que no esta garantizado en todas las distros y rompe la compilacion cruzada
// del contenedor. La ventana se consigue con el modo app de un navegador
// Chromium (appmode.go).
func runWindow(string, <-chan struct{}, <-chan os.Signal) bool { return false }
