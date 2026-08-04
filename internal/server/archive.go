package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// SearchResult es un fichero descargable encontrado en archive.org.
type SearchResult struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	URL   string `json:"url"` // enlace directo, listo para /api/roms/download
	Size  int64  `json:"size"`
}

// archiveBase es variable para que las pruebas puedan apuntar a un servidor
// falso: probar el troceo de la respuesta no deberia depender de que
// archive.org este en pie ni de tener red.
var archiveBase = "https://archive.org"

// archiveHTTP: un cliente propio con plazo. Con http.DefaultClient (sin
// timeout) una respuesta que no llega nunca deja la peticion del navegador
// colgada para siempre y, de paso, ocupa el hueco del siguiente.
var archiveHTTP = &http.Client{Timeout: 20 * time.Second}

// archiveMaxCuerpo corta la lectura del JSON: archive.org devuelve
// metadatos de items con miles de ficheros, y no hay motivo para meterlos
// enteros en memoria.
const archiveMaxCuerpo = 4 << 20 // 4 MiB

// extensiones aceptadas, de mas a menos apetecible. El orden importa: un item
// de archive.org trae de todo (readme, capturas, el .md de turno), y sin
// preferencia el "primer fichero que valga" acaba siendo la documentacion.
var extsROM = []string{
	".zip", ".7z", ".rar",
	".iso", ".chd", ".cue", ".bin",
	".sfc", ".smc", ".nes", ".gba", ".gb", ".gbc", ".n64", ".z64", ".md", ".gen",
}

// elegirFichero se queda con el fichero mas prometedor de un item.
func elegirFichero(files []archiveFile) (archiveFile, bool) {
	mejor := -1
	mejorRango := len(extsROM)
	for i, f := range files {
		lower := strings.ToLower(f.Name)
		for rango, ext := range extsROM {
			if rango >= mejorRango {
				break // ya tenemos uno mejor; el resto de la lista es peor
			}
			if strings.HasSuffix(lower, ext) {
				mejorRango, mejor = rango, i
				break
			}
		}
	}
	if mejor < 0 {
		return archiveFile{}, false
	}
	return files[mejor], true
}

type archiveFile struct {
	Name string `json:"name"`
	Size string `json:"size"` // archive.org lo manda como cadena
}

// searchArchiveOrg busca software en archive.org y devuelve enlaces directos.
//
// Son dos viajes por resultado (buscar da identificadores; los ficheros estan
// en los metadatos de cada item), asi que se limita a unos pocos y se piden en
// paralelo: en serie, cinco items con mala latencia son cinco esperas seguidas.
func searchArchiveOrg(ctx context.Context, query string) ([]SearchResult, error) {
	const maxItems = 5

	searchURL := fmt.Sprintf(
		"%s/advancedsearch.php?q=%s+AND+mediatype%%3A%%28software%%29&fl%%5B%%5D=identifier&fl%%5B%%5D=title&rows=%d&output=json",
		archiveBase, url.QueryEscape(query), maxItems)

	var busqueda struct {
		Response struct {
			Docs []struct {
				Identifier string `json:"identifier"`
				Title      string `json:"title"`
			} `json:"docs"`
		} `json:"response"`
	}
	if err := archiveGetJSON(ctx, searchURL, &busqueda); err != nil {
		return nil, fmt.Errorf("no se pudo consultar archive.org: %w", err)
	}

	docs := busqueda.Response.Docs
	if len(docs) > maxItems {
		docs = docs[:maxItems]
	}

	resultados := make([]SearchResult, len(docs))
	var wg sync.WaitGroup
	for i, doc := range docs {
		wg.Add(1)
		go func(i int, id, title string) {
			defer wg.Done()
			var meta struct {
				Files []archiveFile `json:"files"`
			}
			if err := archiveGetJSON(ctx, archiveBase+"/metadata/"+url.PathEscape(id), &meta); err != nil {
				return // un item que no contesta no invalida la busqueda entera
			}
			f, ok := elegirFichero(meta.Files)
			if !ok {
				return
			}
			size, _ := strconv.ParseInt(f.Size, 10, 64)
			resultados[i] = SearchResult{
				ID:    id,
				Title: title + " (" + f.Name + ")",
				URL:   archiveBase + "/download/" + url.PathEscape(id) + "/" + url.PathEscape(f.Name),
				Size:  size,
			}
		}(i, doc.Identifier, doc.Title)
	}
	wg.Wait()

	// Los huecos son items descartados. Se quita el hueco, no el orden: el que
	// trae archive.org es por relevancia y aqui no tenemos nada mejor.
	final := make([]SearchResult, 0, len(resultados))
	for _, r := range resultados {
		if r.ID != "" {
			final = append(final, r)
		}
	}
	return final, nil
}

func archiveGetJSON(ctx context.Context, u string, v any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "deckman")
	resp, err := archiveHTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("archive.org respondio %s", resp.Status)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, archiveMaxCuerpo)).Decode(v)
}
