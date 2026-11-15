package deck

import (
	"encoding/json"
	"testing"
)

// La biblioteca de destino se busca por ruta, pero la de libraryfolders.vdf y
// la de la API pueden diferir en la barra final. Equivocarse de indice seria
// mandar el juego a otra unidad.
func TestInstallFolderIndex(t *testing.T) {
	folders := []installFolder{
		{Index: 0, Path: "/home/deck/.local/share/Steam"},
		{Index: 1, Path: "/run/media/deck/USD00/"},
		{Index: 2, Path: ""},
	}
	cases := []struct {
		target string
		want   int
		ok     bool
	}{
		{"/run/media/deck/USD00", 1, true},
		{"/run/media/deck/USD00/", 1, true},
		{"/home/deck/.local/share/Steam", 0, true},
		{"/run/media/deck/OTRA", 0, false},
		{"", 0, false},
	}
	for _, c := range cases {
		got, ok := installFolderIndex(folders, c.target)
		if got != c.want || ok != c.ok {
			t.Errorf("installFolderIndex(%q) = %d, %v; quiero %d, %v", c.target, got, ok, c.want, c.ok)
		}
	}
}

// Steam da los bytes como cadena; si no los da, se estima con la fraccion.
func TestMoveStateBytes(t *testing.T) {
	parse := func(js string) moveState {
		var m moveState
		if err := json.Unmarshal([]byte(js), &m); err != nil {
			t.Fatal(err)
		}
		return m
	}
	cases := []struct {
		js         string
		total      uint64
		done, size uint64
	}{
		{`{"progress":null}`, 1000, 0, 1000},
		{`{"progress":{"strBytesMoved":"250","strTotalBytesToMove":"2000","flProgress":0.1}}`, 1000, 250, 2000},
		{`{"progress":{"strBytesMoved":"","strTotalBytesToMove":"","flProgress":0.5}}`, 1000, 500, 1000},
		{`{"progress":{"flProgress":40}}`, 1000, 400, 1000},
		{`{"progress":{"strBytesMoved":"x","strTotalBytesToMove":"0"}}`, 1000, 0, 1000},
	}
	for _, c := range cases {
		done, size := parse(c.js).bytes(c.total)
		if done != c.done || size != c.size {
			t.Errorf("%s: %d/%d; quiero %d/%d", c.js, done, size, c.done, c.size)
		}
	}
}

// En el depurador de Steam, eError == 20 significa «moviendo» y 0 «terminado».
// Cualquier otro codigo distinto de ambos si es un fallo del traslado.
func TestIsMoveError(t *testing.T) {
	casos := []struct {
		code  int
		error bool
	}{
		{0, false},  // terminado con exito
		{20, false}, // traslado en curso
		{7, true},   // contenido compartido / fallo real
		{15, true},  // ruta invalida
		{22, true},  // no se puede mover
		{39, true},  // fichero bloqueado / destino existente
	}
	for _, tc := range casos {
		if got := isMoveError(tc.code); got != tc.error {
			t.Errorf("isMoveError(%d) = %v, quiero %v", tc.code, got, tc.error)
		}
	}
}
