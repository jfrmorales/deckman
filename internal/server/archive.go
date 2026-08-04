package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

type SearchResult struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	URL   string `json:"url"` // Direct download URL
	Size  int64  `json:"size"`
}

// searchArchiveOrg busca software en archive.org
func searchArchiveOrg(query string) ([]SearchResult, error) {
	// 1. Buscar items
	searchURL := fmt.Sprintf("https://archive.org/advancedsearch.php?q=%s+AND+mediatype:(software)&fl[]=identifier,title&rows=10&output=json", url.QueryEscape(query))
	resp, err := http.Get(searchURL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var searchRes struct {
		Response struct {
			Docs []struct {
				Identifier string `json:"identifier"`
				Title      string `json:"title"`
			} `json:"docs"`
		} `json:"response"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&searchRes); err != nil {
		return nil, err
	}

	var results []SearchResult
	// 2. Por cada item, buscar un archivo descargable (zip, iso, roms)
	// Para no saturar, miraremos solo los primeros 5 resultados y sacaremos el primer archivo válido de cada uno.
	limit := 5
	for i, doc := range searchRes.Response.Docs {
		if i >= limit {
			break
		}
		
		metaURL := fmt.Sprintf("https://archive.org/metadata/%s", doc.Identifier)
		mResp, mErr := http.Get(metaURL)
		if mErr != nil {
			continue
		}
		
		var meta struct {
			Files []struct {
				Name   string `json:"name"`
				Format string `json:"format"`
				Size   string `json:"size"`
			} `json:"files"`
		}
		json.NewDecoder(mResp.Body).Decode(&meta)
		mResp.Body.Close()

		for _, f := range meta.Files {
			// Filtramos por formatos típicos
			lower := strings.ToLower(f.Name)
			if strings.HasSuffix(lower, ".zip") || strings.HasSuffix(lower, ".7z") || strings.HasSuffix(lower, ".rar") || 
			   strings.HasSuffix(lower, ".iso") || strings.HasSuffix(lower, ".sfc") || strings.HasSuffix(lower, ".gba") ||
			   strings.HasSuffix(lower, ".nes") || strings.HasSuffix(lower, ".md") {
				
				var size int64
				fmt.Sscanf(f.Size, "%d", &size)
				
				results = append(results, SearchResult{
					ID:    doc.Identifier,
					Title: doc.Title + " (" + f.Name + ")",
					URL:   fmt.Sprintf("https://archive.org/download/%s/%s", doc.Identifier, f.Name),
					Size:  size,
				})
				break // Solo un archivo por item para no llenar la lista de basura
			}
		}
	}

	return results, nil
}
