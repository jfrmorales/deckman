package deck

import (
	"context"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"testing"
	"time"
)

// Mide la subida de un fichero grande con distintas variantes, para averiguar
// donde se pierde el rendimiento. Requiere DECKMAN_TEST_HOST.
func TestIntegrationUploadThroughput(t *testing.T) {
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

	variants := []struct {
		name string
		put  func(src *os.File, dst string) error
	}{
		{"ctxReader con Stat", func(src *os.File, dst string) error {
			out, err := sc.SFTP().Create(dst)
			if err != nil {
				return err
			}
			defer out.Close()
			_, err = out.ReadFrom(&ctxReader{ctx: ctx, f: src})
			return err
		}},
		{"*os.File directo", func(src *os.File, dst string) error {
			out, err := sc.SFTP().Create(dst)
			if err != nil {
				return err
			}
			defer out.Close()
			_, err = out.ReadFrom(src)
			return err
		}},
		{"io.Copy", func(src *os.File, dst string) error {
			out, err := sc.SFTP().Create(dst)
			if err != nil {
				return err
			}
			defer out.Close()
			_, err = io.Copy(out, src)
			return err
		}},
	}

	for i, v := range variants {
		src, err := os.Open(local)
		if err != nil {
			t.Fatal(err)
		}
		dst := path.Join(remoteDir, fmt.Sprintf("v%d.bin", i))
		start := time.Now()
		err = v.put(src, dst)
		elapsed := time.Since(start)
		src.Close()
		if err != nil {
			t.Errorf("%s: %v", v.name, err)
			continue
		}
		st, err := sc.SFTP().Stat(dst)
		if err != nil || st.Size() != size {
			t.Errorf("%s: el fichero remoto no cuadra", v.name)
			continue
		}
		t.Logf("%-22s %6.2f s  %6.1f MB/s", v.name, elapsed.Seconds(),
			float64(size)/(1<<20)/elapsed.Seconds())
		sc.SFTP().Remove(dst)
	}
	sc.Run(context.Background(), "rm -rf "+ShellQuote(remoteDir))
}
