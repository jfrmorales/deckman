package config

import (
	"encoding/json"
	"testing"
)

// Un config.json de la epoca de una sola Deck tiene que seguir funcionando: si
// la migracion falla, el usuario se encuentra la Deck "perdida" y vuelve a
// conectar con contrasena, dejando una segunda clave huerfana en la maquina.
func TestMigracionDesdeUnaSolaDeck(t *testing.T) {
	viejo := `{
		"host": "192.168.1.50",
		"port": 22,
		"user": "deck",
		"keyPath": "/home/quien/.config/deckman/id_ed25519",
		"gridKey": "abc"
	}`
	var c Config
	if err := json.Unmarshal([]byte(viejo), &c); err != nil {
		t.Fatal(err)
	}
	c.normalize()

	if len(c.Decks) != 1 {
		t.Fatalf("esperaba 1 Deck migrada, hay %d", len(c.Decks))
	}
	d := c.ActiveDeck()
	if d.Host != "192.168.1.50" || d.Port != 22 || d.User != "deck" {
		t.Fatalf("la Deck migrada no conserva los datos: %+v", d)
	}
	if c.KeyPath == "" || c.GridKey != "abc" {
		t.Fatal("la migracion ha perdido la clave SSH o la de SteamGridDB")
	}

	// Al guardar, los campos viejos ya no se escriben: si se quedaran, habria
	// dos sitios diciendo cual es la Deck en uso.
	out, err := json.Marshal(&c)
	if err != nil {
		t.Fatal(err)
	}
	var suelto map[string]any
	json.Unmarshal(out, &suelto)
	for _, k := range []string{"host", "port", "user"} {
		if _, hay := suelto[k]; hay {
			t.Errorf("el campo viejo %q sigue escribiendose", k)
		}
	}
}

func TestConfigVaciaNoTieneDeckActiva(t *testing.T) {
	c := &Config{Active: -1}
	c.normalize()
	if len(c.Decks) != 0 || c.Active != -1 {
		t.Fatalf("una configuracion nueva no deberia tener Decks: %+v", c)
	}
	// Aun asi la interfaz pide la activa para pintar el formulario.
	d := c.ActiveDeck()
	if d.Port != 22 || d.User != "deck" {
		t.Fatalf("los valores por defecto no son los de SteamOS: %+v", d)
	}
}

func TestUpsertActualizaEnVezDeDuplicar(t *testing.T) {
	c := &Config{Active: -1}
	c.UpsertDeck(Deck{Host: "10.0.0.1", Port: 22, User: "deck", Name: "salon"})

	// Mismo host y puerto, otro usuario: es la misma maquina.
	c.UpsertDeck(Deck{Host: "10.0.0.1", Port: 22, User: "otro"})
	if len(c.Decks) != 1 {
		t.Fatalf("se ha duplicado la Deck: %+v", c.Decks)
	}
	if c.Decks[0].User != "otro" {
		t.Error("no se ha actualizado el usuario")
	}
	if c.Decks[0].Name != "salon" {
		t.Error("conectar ha borrado el nombre que puso el usuario")
	}

	// Otro puerto es otra entrada: puede ser otra Deck detras del mismo NAT.
	c.UpsertDeck(Deck{Host: "10.0.0.1", Port: 2222, User: "deck"})
	if len(c.Decks) != 2 {
		t.Fatalf("esperaba 2 Decks, hay %d", len(c.Decks))
	}
	if c.Active != 1 {
		t.Errorf("la Deck recien guardada deberia quedar activa, Active=%d", c.Active)
	}
}

// Al olvidar una Deck, la que estaba en uso tiene que seguir siendo la misma.
// Si Active se quedase apuntando al indice de al lado, la siguiente conexion
// iria a una maquina que el usuario no ha elegido.
func TestRemoveDeckMantieneLaActiva(t *testing.T) {
	c := &Config{Active: -1}
	c.UpsertDeck(Deck{Host: "a", Port: 22, User: "deck"})
	c.UpsertDeck(Deck{Host: "b", Port: 22, User: "deck"})
	c.UpsertDeck(Deck{Host: "c", Port: 22, User: "deck"})
	c.Active = 2 // estamos en "c"

	if _, ok := c.RemoveDeck(0); !ok { // olvidamos "a"
		t.Fatal("RemoveDeck ha fallado")
	}
	if got := c.ActiveDeck().Host; got != "c" {
		t.Fatalf("la Deck activa ha cambiado sola a %q", got)
	}

	// Olvidar la activa: hay que caer en alguna que exista, no en un indice
	// fuera de rango.
	c.RemoveDeck(c.Active)
	if c.Active < 0 || c.Active >= len(c.Decks) {
		t.Fatalf("Active=%d fuera de rango con %d Decks", c.Active, len(c.Decks))
	}

	// Y al quitar la ultima no puede quedar una activa fantasma.
	c.RemoveDeck(0)
	if len(c.Decks) != 0 || c.Active != -1 {
		t.Fatalf("sin Decks, Active deberia ser -1: %+v", c)
	}
	if _, ok := c.RemoveDeck(0); ok {
		t.Error("RemoveDeck deberia rechazar un indice inexistente")
	}
}
