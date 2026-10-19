package main

import (
	"os"
	"sync/atomic"
	"testing"
	"time"
)

// esperarAbandonoEn lanza esperarAbandono con tiempos cortos y dice si volvio
// antes del limite.
func esperarAbandonoEn(t *testing.T, clientes func() int, ocupado func() bool, limite time.Duration) bool {
	t.Helper()
	hecho := make(chan struct{})
	go func() {
		esperarAbandono(clientes, ocupado, make(chan struct{}), make(chan os.Signal), 50*time.Millisecond, 5*time.Millisecond)
		close(hecho)
	}()
	select {
	case <-hecho:
		return true
	case <-time.After(limite):
		return false
	}
}

// Una interfaz que se conecto y luego se cerro apaga deckman pasada la gracia.
func TestAbandonoTrasCerrar(t *testing.T) {
	var n atomic.Int32
	n.Store(1)
	go func() { time.Sleep(30 * time.Millisecond); n.Store(0) }()
	if !esperarAbandonoEn(t, func() int { return int(n.Load()) }, func() bool { return false }, 2*time.Second) {
		t.Fatal("no se apago con la interfaz cerrada")
	}
}

// Si nunca llego a conectarse nadie (el navegador no se abrio y el usuario va
// a escribir la direccion a mano), no se apaga.
func TestAbandonoSinHaberVistoANadie(t *testing.T) {
	if esperarAbandonoEn(t, func() int { return 0 }, func() bool { return false }, 300*time.Millisecond) {
		t.Fatal("se apago sin que se hubiera abierto ninguna interfaz")
	}
}

// Con un trabajo en marcha no se apaga aunque la interfaz este cerrada:
// cortarlo seria tirar lo ya copiado.
func TestAbandonoConTrabajoEnCurso(t *testing.T) {
	var n atomic.Int32
	n.Store(1)
	go func() { time.Sleep(20 * time.Millisecond); n.Store(0) }()
	if esperarAbandonoEn(t, func() int { return int(n.Load()) }, func() bool { return true }, 300*time.Millisecond) {
		t.Fatal("se apago con un trabajo en curso")
	}
}

// Una recarga corta la conexion un momento: si vuelve antes de la gracia, la
// cuenta empieza de cero.
func TestAbandonoRecargaNoCuenta(t *testing.T) {
	var n atomic.Int32
	n.Store(1)
	go func() {
		for i := 0; i < 6; i++ {
			time.Sleep(30 * time.Millisecond)
			n.Store(0)
			time.Sleep(30 * time.Millisecond)
			n.Store(1)
		}
	}()
	if esperarAbandonoEn(t, func() int { return int(n.Load()) }, func() bool { return false }, 300*time.Millisecond) {
		t.Fatal("una recarga se tomo por cierre")
	}
}
