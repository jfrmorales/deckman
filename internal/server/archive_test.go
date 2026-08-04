package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Un item de archive.org trae de todo. Sin preferencia por extension, el
// "primer fichero que valga" acaba siendo el readme o la captura de pantalla,
// que es lo que pasaba antes de extsROM.
func TestElegirFichero(t *testing.T) {
	files := []archiveFile{
		{Name: "__ia_thumb.jpg", Size: "1000"},
		{Name: "LEEME.md", Size: "500"},
		{Name: "Sonic (USA).sfc", Size: "524288"},
		{Name: "Sonic (USA).zip", Size: "262144"},
	}
	f, ok := elegirFichero(files)
	if !ok {
		t.Fatal("no eligio ningun fichero")
	}
	if f.Name != "Sonic (USA).zip" {
		t.Errorf("eligio %q; el .zip va antes que el .sfc y que el .md", f.Name)
	}

	if _, ok := elegirFichero([]archiveFile{{Name: "portada.png"}, {Name: "notas.txt"}}); ok {
		t.Error("eligio un fichero de un item sin nada descargable")
	}
	if _, ok := elegirFichero(nil); ok {
		t.Error("eligio un fichero de un item sin ficheros")
	}
}

// La busqueda va contra un archive.org de mentira: comprobar que troceamos
// bien la respuesta no deberia depender de tener red ni de que el servicio de
// verdad este en pie.
func TestSearchArchiveOrg(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/advancedsearch.php"):
			if q := r.URL.Query().Get("q"); !strings.Contains(q, "sonic") {
				t.Errorf("la busqueda no llevaba el texto: %q", q)
			}
			w.Write([]byte(`{"response":{"docs":[
				{"identifier":"item-con-rom","title":"Sonic"},
				{"identifier":"item-sin-rom","title":"Solo documentacion"},
				{"identifier":"item-roto","title":"No contesta"}
			]}}`))
		case r.URL.Path == "/metadata/item-con-rom":
			w.Write([]byte(`{"files":[{"name":"leeme.txt","size":"10"},{"name":"Sonic (USA).zip","size":"262144"}]}`))
		case r.URL.Path == "/metadata/item-sin-rom":
			w.Write([]byte(`{"files":[{"name":"portada.png","size":"10"}]}`))
		default:
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	defer srv.Close()

	viejo := archiveBase
	archiveBase = srv.URL
	defer func() { archiveBase = viejo }()

	res, err := searchArchiveOrg(context.Background(), "sonic")
	if err != nil {
		t.Fatalf("searchArchiveOrg: %v", err)
	}
	// De los tres items solo uno tiene fichero descargable: los otros dos se
	// caen, pero no se llevan por delante la busqueda entera.
	if len(res) != 1 {
		t.Fatalf("devolvio %d resultados, se esperaba 1: %+v", len(res), res)
	}
	if res[0].Size != 262144 {
		t.Errorf("tamano %d, se esperaba 262144", res[0].Size)
	}
	if !strings.HasSuffix(res[0].URL, "/download/item-con-rom/Sonic%20%28USA%29.zip") {
		t.Errorf("URL de descarga inesperada: %q", res[0].URL)
	}
	if !strings.Contains(res[0].Title, "Sonic (USA).zip") {
		t.Errorf("el titulo no dice que fichero se baja: %q", res[0].Title)
	}
}

// Si archive.org no contesta, el error sube: mejor decirlo que devolver una
// lista vacia que parece "no hay resultados".
func TestSearchArchiveOrgFallo(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	viejo := archiveBase
	archiveBase = srv.URL
	defer func() { archiveBase = viejo }()

	if _, err := searchArchiveOrg(context.Background(), "sonic"); err == nil {
		t.Error("searchArchiveOrg = nil con archive.org caido")
	}
}
