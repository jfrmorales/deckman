package deck

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"testing"
	"time"
)

// Mide la bajada de un fichero grande con las dos variantes de lectura, para
// comprobar de verdad que io.Copy (la ruta WriteTo concurrente de pkg/sftp)
// gana a io.ReadAll, que es la trampa de HALLAZGOS-STEAM.md §9 al reves.
// Requiere DECKMAN_TEST_HOST.
func TestIntegrationDownloadThroughput(t *testing.T) {
	c, ctx := integrationClient(t)
	sc, _ := setupSandbox(t, c, ctx)

	const size = 40 << 20
	local := filepath.Join(t.TempDir(), "grande.bin")
	buf := make([]byte, size)
	for i := range buf {
		buf[i] = byte(i)
	}
	if err := os.WriteFile(local, buf, 0o644); err != nil {
		t.Fatal(err)
	}

	remoteDir := path.Join(sc.Home, "bench")
	if err := sc.MkdirAll(remoteDir); err != nil {
		t.Fatal(err)
	}
	remote := path.Join(remoteDir, "grande.bin")
	src, err := os.Open(local)
	if err != nil {
		t.Fatal(err)
	}
	out, err := sc.SFTP().Create(remote)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := out.ReadFrom(&ctxReader{ctx: ctx, f: src}); err != nil {
		t.Fatal(err)
	}
	out.Close()
	src.Close()

	variants := []struct {
		name string
		get  func(p string) ([]byte, error)
	}{
		{"io.ReadAll (antes)", func(p string) ([]byte, error) {
			f, err := sc.SFTP().Open(p)
			if err != nil {
				return nil, err
			}
			defer f.Close()
			return io.ReadAll(f)
		}},
		{"ReadFile (io.Copy)", func(p string) ([]byte, error) {
			return sc.ReadFile(p)
		}},
	}

	for _, v := range variants {
		start := time.Now()
		data, err := v.get(remote)
		elapsed := time.Since(start)
		if err != nil {
			t.Errorf("%s: %v", v.name, err)
			continue
		}
		if len(data) != size || !bytes.Equal(data[:1024], buf[:1024]) {
			t.Errorf("%s: lo bajado no cuadra (%d bytes)", v.name, len(data))
			continue
		}
		t.Logf("%-22s %6.2f s  %6.1f MB/s", v.name, elapsed.Seconds(),
			float64(size)/(1<<20)/elapsed.Seconds())
	}
	sc.Run(context.Background(), fmt.Sprintf("rm -rf %s", ShellQuote(remoteDir)))
}
