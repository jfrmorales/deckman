package update

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseVersionAndIsNewer(t *testing.T) {
	v, ok := ParseVersion("v0.8.1")
	if !ok || v.Major != 0 || v.Minor != 8 || v.Patch != 1 || v.Suffix != "" {
		t.Fatalf("ParseVersion(v0.8.1) = %+v, %v", v, ok)
	}
	v2, ok := ParseVersion("0.8.1-3-gabc123")
	if !ok || v2.Suffix != "3-gabc123" {
		t.Fatalf("ParseVersion(0.8.1-3-gabc123) = %+v, %v", v2, ok)
	}

	for _, bad := range []string{"", "dev", "v0.8", "0.8.1.2", "v0.8.1-", "v+1.0.0", "v-1.0.0", "abc"} {
		if _, ok := ParseVersion(bad); ok {
			t.Errorf("ParseVersion(%q) deberia fallar", bad)
		}
		if !IsDevVersion(bad) {
			t.Errorf("IsDevVersion(%q) deberia ser true", bad)
		}
	}
	if IsDevVersion("v0.8.1") {
		t.Error("v0.8.1 no es dev")
	}

	cases := []struct {
		cur  string
		lat  string
		want bool
	}{
		{"v0.8.0", "v0.8.1", true},
		{"0.8.0", "v0.9.0", true},
		{"v0.8.1", "v1.0.0", true},
		{"v0.8.1", "v0.8.1", false},
		{"v0.8.2", "v0.8.1", false},
		{"dev", "v0.8.1", false},
		{"", "v0.8.1", false},
		{"v0.8.1-3-gabc", "v0.8.2", true},
		{"v0.8.1-3-gabc", "v0.8.1", false},
		{"v0.8.1-dirty", "v0.8.1", false},
		{"v0.8.1", "v0.8.2-rc1", false},
	}
	for _, tc := range cases {
		if got := IsNewer(tc.cur, tc.lat); got != tc.want {
			t.Errorf("IsNewer(%q, %q) = %v, se esperaba %v", tc.cur, tc.lat, got, tc.want)
		}
	}
}

func TestLookupAndVerifySHA256(t *testing.T) {
	payload := []byte("binario de prueba")
	sum := sha256.Sum256(payload)
	hexSum := hex.EncodeToString(sum[:])

	sums := []byte("# comentario\n" +
		"0000000000000000000000000000000000000000000000000000000000000000  otro\n" +
		hexSum + "  *./deckman-0.8.1-linux-amd64\n")

	got, err := LookupSHA256(sums, "deckman-0.8.1-linux-amd64")
	if err != nil {
		t.Fatalf("LookupSHA256: %v", err)
	}
	if err := VerifySHA256(sum[:], got); err != nil {
		t.Fatalf("VerifySHA256: %v", err)
	}
	if err := LookupSHA256Err(sums, "inexistente"); err == nil {
		t.Fatal("esperaba error para fichero ausente")
	}
	bad := append([]byte(nil), sum[:]...)
	bad[0] ^= 0xff
	if err := VerifySHA256(bad, got); err == nil {
		t.Fatal("esperaba error con hash distinto")
	}
	if _, err := LookupSHA256([]byte("zzzz  deckman"), "deckman"); err == nil {
		t.Fatal("esperaba error con hex invalido")
	}
}

func LookupSHA256Err(sums []byte, name string) error {
	_, err := LookupSHA256(sums, name)
	return err
}

func generateTestKeyAndBundle(t *testing.T, data []byte) ([]byte, []byte, *ecdsa.PrivateKey) {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKIXPublicKey(&priv.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	pubPEM := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der})

	bundle := signWithKey(t, priv, data, "SHA2_256")
	return pubPEM, bundle, priv
}

func signWithKey(t *testing.T, priv *ecdsa.PrivateKey, data []byte, algo string) []byte {
	t.Helper()
	sum := sha256.Sum256(data)
	sig, err := ecdsa.SignASN1(rand.Reader, priv, sum[:])
	if err != nil {
		t.Fatal(err)
	}
	doc := map[string]any{
		"mediaType": "application/vnd.dev.sigstore.bundle.v0.3+json",
		"messageSignature": map[string]any{
			"messageDigest": map[string]any{
				"algorithm": algo,
				"digest":    base64.StdEncoding.EncodeToString(sum[:]),
			},
			"signature": base64.StdEncoding.EncodeToString(sig),
		},
	}
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestDefaultCosignPubKeyMatchesFingerprint(t *testing.T) {
	block, _ := pem.Decode([]byte(DefaultCosignPubKeyPEM))
	if block == nil {
		t.Fatal("DefaultCosignPubKeyPEM no es PEM valido")
	}
	pubAny, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		t.Fatalf("ParsePKIXPublicKey: %v", err)
	}
	if _, ok := pubAny.(*ecdsa.PublicKey); !ok {
		t.Fatalf("la clave publica no es ECDSA: %T", pubAny)
	}
	fp := sha256.Sum256([]byte(DefaultCosignPubKeyPEM))
	const expectedFP = "29e5cbe2bbd71402bdea9db4a6d4cf4fc99dab59dd4c9cb58a010f6801520544"
	if got := hex.EncodeToString(fp[:]); got != expectedFP {
		t.Fatalf("huella de DefaultCosignPubKeyPEM = %s, se esperaba %s", got, expectedFP)
	}
}

func TestVerifyCosignBundle(t *testing.T) {
	sums := []byte("0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef  deckman\n")
	pubPEM, bundle, priv := generateTestKeyAndBundle(t, sums)

	if err := VerifyCosignBundle(pubPEM, sums, bundle); err != nil {
		t.Fatalf("firma valida rechazada: %v", err)
	}

	altered := append([]byte(nil), sums...)
	altered[0] = 'f'
	if err := VerifyCosignBundle(pubPEM, altered, bundle); err == nil {
		t.Fatal("deberia rechazar SHA256SUMS alterado")
	}

	otherPub, _, _ := generateTestKeyAndBundle(t, sums)
	if err := VerifyCosignBundle(otherPub, sums, bundle); err == nil {
		t.Fatal("deberia rechazar firma con otra clave publica")
	}

	badAlgo := signWithKey(t, priv, sums, "SHA512")
	if err := VerifyCosignBundle(pubPEM, sums, badAlgo); err == nil {
		t.Fatal("deberia rechazar algoritmo distinto de SHA2_256")
	}

	if err := VerifyCosignBundle(pubPEM, sums, []byte(`{"messageSignature":{"messageDigest":{"algorithm":"SHA2_256","digest":"AAAA"},"signature":"AAAA"}}`)); err == nil {
		t.Fatal("deberia rechazar firma truncada")
	}
}

func TestAllowedReleaseURL(t *testing.T) {
	buenas := []string{
		"https://api.github.com/repos/jfrmorales/deckman/releases/latest",
		"https://github.com/jfrmorales/deckman/releases/download/v0.8.1/SHA256SUMS",
		"https://release-assets.githubusercontent.com/abc/file",
		"https://objects.githubusercontent.com/github-production-release-asset/123",
	}
	for _, u := range buenas {
		if err := AllowedReleaseURL(u); err != nil {
			t.Errorf("AllowedReleaseURL(%q) = %v; se esperaba nil", u, err)
		}
	}

	malas := []string{
		"http://github.com/jfrmorales/deckman/releases/latest",
		"https://127.0.0.1/file",
		"https://github.com.evil.example/file",
		"https://githubusercontent.com/file",
		"https://.githubusercontent.com/file",
		"file:///etc/passwd",
		"://roto",
	}
	for _, u := range malas {
		if err := AllowedReleaseURL(u); err == nil {
			t.Errorf("AllowedReleaseURL(%q) = nil; tenia que rechazarse", u)
		}
	}
}

func TestExpectedAssetName(t *testing.T) {
	cases := []struct {
		ver     string
		goos    string
		goarch  string
		flatpak bool
		want    string
		ok      bool
	}{
		{"v0.8.1", "linux", "amd64", false, "deckman-0.8.1-linux-amd64", true},
		{"0.8.1", "linux", "arm64", false, "deckman-0.8.1-linux-arm64", true},
		{"v0.8.1", "windows", "amd64", false, "deckman-0.8.1-windows-amd64.exe", true},
		{"v0.8.1", "windows", "arm64", false, "deckman-0.8.1-windows-arm64.exe", true},
		{"v0.8.1", "darwin", "amd64", false, "deckman-0.8.1-darwin-amd64", true},
		{"v0.8.1", "darwin", "arm64", false, "deckman-0.8.1-darwin-arm64", true},
		{"v0.8.1", "linux", "amd64", true, "deckman-0.8.1.flatpak", true},
		{"v0.8.1", "linux", "arm64", true, "", false},
		{"v0.8.1", "freebsd", "amd64", false, "", false},
		{"dev", "linux", "amd64", false, "", false},
	}
	for _, tc := range cases {
		got, ok := ExpectedAssetName(tc.ver, tc.goos, tc.goarch, tc.flatpak)
		if got != tc.want || ok != tc.ok {
			t.Errorf("ExpectedAssetName(%q, %s, %s, %v) = (%q, %v), want (%q, %v)",
				tc.ver, tc.goos, tc.goarch, tc.flatpak, got, ok, tc.want, tc.ok)
		}
	}
}

func newLocalAllowURL(t *testing.T, baseURL string) func(string) error {
	t.Helper()
	parsed, err := url.Parse(baseURL)
	if err != nil {
		t.Fatal(err)
	}
	wantHost := parsed.Host
	return func(raw string) error {
		u, err := url.Parse(raw)
		if err != nil || u.Host != wantHost {
			return fmt.Errorf("host no permitido en prueba: %s", raw)
		}
		return nil
	}
}

func TestCheckAndApplyBinarySignedAndUnsigned(t *testing.T) {
	for _, signed := range []bool{true, false} {
		name := "unsigned"
		if signed {
			name = "signed"
		}
		t.Run(name, func(t *testing.T) {
			oldContent := []byte("deckman-old-v0.8.0")
			newContent := []byte("deckman-new-v0.8.1-binary")
			sum := sha256.Sum256(newContent)
			assetName := "deckman-0.8.1-linux-amd64"
			sumsBody := []byte(hex.EncodeToString(sum[:]) + "  " + assetName + "\n")
			pubPEM, bundleBody, _ := generateTestKeyAndBundle(t, sumsBody)

			var srv *httptest.Server
			srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/latest":
					assets := []map[string]any{
						{"name": assetName, "size": len(newContent), "browser_download_url": srv.URL + "/bin"},
						{"name": "SHA256SUMS", "size": len(sumsBody), "browser_download_url": srv.URL + "/sums"},
					}
					if signed {
						assets = append(assets, map[string]any{
							"name": "SHA256SUMS.bundle", "size": len(bundleBody), "browser_download_url": srv.URL + "/bundle",
						})
					}
					_ = json.NewEncoder(w).Encode(map[string]any{
						"tag_name": "v0.8.1",
						"html_url": "https://github.com/jfrmorales/deckman/releases/tag/v0.8.1",
						"body":     "Notas de prueba",
						"assets":   assets,
					})
				case "/sums":
					_, _ = w.Write(sumsBody)
				case "/bundle":
					_, _ = w.Write(bundleBody)
				case "/bin":
					_, _ = w.Write(newContent)
				default:
					http.NotFound(w, r)
				}
			}))
			defer srv.Close()

			dir := t.TempDir()
			exePath := filepath.Join(dir, "deckman")
			if err := os.WriteFile(exePath, oldContent, 0o755); err != nil {
				t.Fatal(err)
			}

			u := New("v0.8.0")
			u.ReleaseURL = srv.URL + "/latest"
			u.CosignPubKeyPEM = pubPEM
			u.AllowURL = newLocalAllowURL(t, srv.URL)
			u.GOOS = "linux"
			u.GOARCH = "amd64"
			u.InFlatpak = func() bool { return false }
			u.ExecutablePath = func() (string, error) { return exePath, nil }

			info, err := u.Check(context.Background())
			if err != nil {
				t.Fatalf("Check: %v", err)
			}
			if !info.Available || !info.CanApply || info.Signed != signed || info.AssetName != assetName {
				t.Fatalf("Info inesperado: %+v", info)
			}

			var phases []string
			err = u.Apply(context.Background(), info, func(p Progress) {
				phases = append(phases, p.Phase)
			})
			if err != nil {
				t.Fatalf("Apply: %v", err)
			}

			got, err := os.ReadFile(exePath)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, newContent) {
				t.Fatalf("contenido tras Apply = %q, esperaba %q", got, newContent)
			}
			fi, err := os.Stat(exePath)
			if err != nil {
				t.Fatal(err)
			}
			if fi.Mode().Perm()&0o100 == 0 {
				t.Fatalf("el binario perdio el permiso de ejecucion: %v", fi.Mode().Perm())
			}
			if len(phases) == 0 || phases[0] != "verificando" || phases[len(phases)-1] != "instalando" {
				t.Fatalf("fases inesperadas: %v", phases)
			}
		})
	}
}

func TestApplyRejectsBadSignatureAndBadChecksumWithoutTouchingBinary(t *testing.T) {
	oldContent := []byte("deckman-original")
	newContent := []byte("deckman-nuevo")
	sum := sha256.Sum256(newContent)
	assetName := "deckman-0.8.1-linux-amd64"
	goodSums := []byte(hex.EncodeToString(sum[:]) + "  " + assetName + "\n")
	pubPEM, goodBundle, _ := generateTestKeyAndBundle(t, goodSums)

	t.Run("bad_cosign_bundle", func(t *testing.T) {
		badBundle := []byte(`{"mediaType":"v0.3","messageSignature":{"messageDigest":{"algorithm":"SHA2_256","digest":"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="},"signature":"AAAA"}}`)
		var srv *httptest.Server
		srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/latest":
				_ = json.NewEncoder(w).Encode(map[string]any{
					"tag_name": "v0.8.1",
					"assets": []map[string]any{
						{"name": assetName, "size": len(newContent), "browser_download_url": srv.URL + "/bin"},
						{"name": "SHA256SUMS", "size": len(goodSums), "browser_download_url": srv.URL + "/sums"},
						{"name": "SHA256SUMS.bundle", "size": len(badBundle), "browser_download_url": srv.URL + "/bundle"},
					},
				})
			case "/sums":
				_, _ = w.Write(goodSums)
			case "/bundle":
				_, _ = w.Write(badBundle)
			case "/bin":
				_, _ = w.Write(newContent)
			}
		}))
		defer srv.Close()

		exePath := filepath.Join(t.TempDir(), "deckman")
		if err := os.WriteFile(exePath, oldContent, 0o755); err != nil {
			t.Fatal(err)
		}

		u := New("v0.8.0")
		u.ReleaseURL = srv.URL + "/latest"
		u.CosignPubKeyPEM = pubPEM
		u.AllowURL = newLocalAllowURL(t, srv.URL)
		u.GOOS = "linux"
		u.GOARCH = "amd64"
		u.InFlatpak = func() bool { return false }
		u.ExecutablePath = func() (string, error) { return exePath, nil }

		info, err := u.Check(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if err := u.Apply(context.Background(), info, nil); err == nil {
			t.Fatal("esperaba error por firma invalida")
		}
		got, _ := os.ReadFile(exePath)
		if !bytes.Equal(got, oldContent) {
			t.Fatalf("el binario se toco tras fallo de firma: %q", got)
		}
	})

	t.Run("bad_sha256", func(t *testing.T) {
		tampered := []byte("deckman-manipulado")
		var srv *httptest.Server
		srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/latest":
				_ = json.NewEncoder(w).Encode(map[string]any{
					"tag_name": "v0.8.1",
					"assets": []map[string]any{
						{"name": assetName, "size": len(tampered), "browser_download_url": srv.URL + "/bin"},
						{"name": "SHA256SUMS", "size": len(goodSums), "browser_download_url": srv.URL + "/sums"},
						{"name": "SHA256SUMS.bundle", "size": len(goodBundle), "browser_download_url": srv.URL + "/bundle"},
					},
				})
			case "/sums":
				_, _ = w.Write(goodSums)
			case "/bundle":
				_, _ = w.Write(goodBundle)
			case "/bin":
				_, _ = w.Write(tampered)
			}
		}))
		defer srv.Close()

		exePath := filepath.Join(t.TempDir(), "deckman")
		if err := os.WriteFile(exePath, oldContent, 0o755); err != nil {
			t.Fatal(err)
		}

		u := New("v0.8.0")
		u.ReleaseURL = srv.URL + "/latest"
		u.CosignPubKeyPEM = pubPEM
		u.AllowURL = newLocalAllowURL(t, srv.URL)
		u.GOOS = "linux"
		u.GOARCH = "amd64"
		u.InFlatpak = func() bool { return false }
		u.ExecutablePath = func() (string, error) { return exePath, nil }

		info, err := u.Check(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if err := u.Apply(context.Background(), info, nil); err == nil {
			t.Fatal("esperaba error por checksum distinto")
		}
		got, _ := os.ReadFile(exePath)
		if !bytes.Equal(got, oldContent) {
			t.Fatalf("el binario se toco tras fallo de checksum: %q", got)
		}
	})
}

func TestApplyBlocksDisallowedRedirect(t *testing.T) {
	evil := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("evil"))
	}))
	defer evil.Close()

	assetName := "deckman-0.8.1-linux-amd64"
	sum := sha256.Sum256([]byte("evil"))
	sumsBody := []byte(hex.EncodeToString(sum[:]) + "  " + assetName + "\n")

	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/latest":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"tag_name": "v0.8.1",
				"assets": []map[string]any{
					{"name": assetName, "size": 4, "browser_download_url": srv.URL + "/bin"},
					{"name": "SHA256SUMS", "size": len(sumsBody), "browser_download_url": srv.URL + "/sums"},
				},
			})
		case "/sums":
			_, _ = w.Write(sumsBody)
		case "/bin":
			http.Redirect(w, r, evil.URL+"/payload", http.StatusFound)
		}
	}))
	defer srv.Close()

	exePath := filepath.Join(t.TempDir(), "deckman")
	if err := os.WriteFile(exePath, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}

	u := New("v0.8.0")
	u.ReleaseURL = srv.URL + "/latest"
	u.AllowURL = newLocalAllowURL(t, srv.URL)
	u.GOOS = "linux"
	u.GOARCH = "amd64"
	u.InFlatpak = func() bool { return false }
	u.ExecutablePath = func() (string, error) { return exePath, nil }

	info, err := u.Check(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	err = u.Apply(context.Background(), info, nil)
	if err == nil || !strings.Contains(err.Error(), "sitio no permitido") {
		t.Fatalf("esperaba bloqueo de redireccion, got %v", err)
	}
}

func TestCheckReadOnlyDirAndFlatpakModes(t *testing.T) {
	assetBin := "deckman-0.8.1-linux-amd64"
	assetFlatpak := "deckman-0.8.1.flatpak"
	bundleData := []byte("fake-flatpak-bundle")
	bundleSum := sha256.Sum256(bundleData)
	sumsBody := []byte(hex.EncodeToString(bundleSum[:]) + "  " + assetFlatpak + "\n")

	var includeBundle bool
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/latest":
			assets := []map[string]any{
				{"name": assetBin, "size": 10, "browser_download_url": srv.URL + "/bin"},
				{"name": "SHA256SUMS", "size": len(sumsBody), "browser_download_url": srv.URL + "/sums"},
			}
			if includeBundle {
				assets = append(assets, map[string]any{
					"name": assetFlatpak, "size": len(bundleData), "browser_download_url": srv.URL + "/flatpak",
				})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"tag_name": "v0.8.1",
				"assets":   assets,
			})
		case "/sums":
			_, _ = w.Write(sumsBody)
		case "/flatpak":
			_, _ = w.Write(bundleData)
		}
	}))
	defer srv.Close()

	// Directorio no escribible para binario suelto.
	uNoWrite := New("v0.8.0")
	uNoWrite.ReleaseURL = srv.URL + "/latest"
	uNoWrite.AllowURL = newLocalAllowURL(t, srv.URL)
	uNoWrite.GOOS = "linux"
	uNoWrite.GOARCH = "amd64"
	uNoWrite.InFlatpak = func() bool { return false }
	uNoWrite.ExecutablePath = func() (string, error) {
		return filepath.Join(t.TempDir(), "inexistente", "deckman"), nil
	}
	infoNoWrite, err := uNoWrite.Check(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !infoNoWrite.Available || infoNoWrite.CanApply || infoNoWrite.ManualReason != "no_write_permission" {
		t.Fatalf("esperaba no_write_permission, got %+v", infoNoWrite)
	}

	// Permisos 0555 cuando no corre como root.
	if os.Geteuid() != 0 {
		roDir := filepath.Join(t.TempDir(), "ro")
		if err := os.MkdirAll(roDir, 0o755); err != nil {
			t.Fatal(err)
		}
		roExe := filepath.Join(roDir, "deckman")
		if err := os.WriteFile(roExe, []byte("x"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(roDir, 0o555); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(roDir, 0o755) })
		uNoWrite.ExecutablePath = func() (string, error) { return roExe, nil }
		infoRO, err := uNoWrite.Check(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if infoRO.CanApply || infoRO.ManualReason != "no_write_permission" {
			t.Fatalf("esperaba no_write_permission en 0555, got %+v", infoRO)
		}
	}

	// Flatpak sin bundle en la release.
	includeBundle = false
	uFlatpak := New("v0.8.0")
	uFlatpak.ReleaseURL = srv.URL + "/latest"
	uFlatpak.AllowURL = newLocalAllowURL(t, srv.URL)
	uFlatpak.GOOS = "linux"
	uFlatpak.GOARCH = "amd64"
	uFlatpak.FlatpakID = "io.github.jfrmorales.deckman"
	uFlatpak.InFlatpak = func() bool { return true }
	uFlatpak.CheckFlatpakUser = func(context.Context, string) bool { return true }

	infoNoBundle, err := uFlatpak.Check(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !infoNoBundle.Available || infoNoBundle.CanApply || infoNoBundle.ManualReason != "flatpak_no_bundle" {
		t.Fatalf("esperaba flatpak_no_bundle, got %+v", infoNoBundle)
	}

	// Flatpak instalado en --system (no --user).
	includeBundle = true
	uFlatpak.CheckFlatpakUser = func(context.Context, string) bool { return false }
	infoSys, err := uFlatpak.Check(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if infoSys.CanApply || infoSys.ManualReason != "flatpak_system" {
		t.Fatalf("esperaba flatpak_system, got %+v", infoSys)
	}

	// Flatpak --user con bundle: verifica, llama al instalador y limpia el temporal.
	cacheDir := t.TempDir()
	var installedData []byte
	var installedName string
	uFlatpak.CacheDir = func() (string, error) { return cacheDir, nil }
	uFlatpak.CheckFlatpakUser = func(context.Context, string) bool { return true }
	uFlatpak.InstallFlatpak = func(_ context.Context, bundlePath string) error {
		installedName = filepath.Base(bundlePath)
		var rErr error
		installedData, rErr = os.ReadFile(bundlePath)
		return rErr
	}

	infoUser, err := uFlatpak.Check(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !infoUser.Available || !infoUser.CanApply || !infoUser.IsFlatpak {
		t.Fatalf("esperaba flatpak aplicable, got %+v", infoUser)
	}
	if err := uFlatpak.Apply(context.Background(), infoUser, nil); err != nil {
		t.Fatalf("Apply flatpak: %v", err)
	}
	if installedName != assetFlatpak || !bytes.Equal(installedData, bundleData) {
		t.Fatalf("bundle instalado incorrecto: %q (%q)", installedName, installedData)
	}
	if _, err := os.Stat(filepath.Join(cacheDir, "deckman-update", assetFlatpak)); !os.IsNotExist(err) {
		t.Fatalf("el bundle temporal no se limpio tras instalar: %v", err)
	}
}

func TestSwapBinaryWithBackup(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "deckman.exe")
	tmp := filepath.Join(dir, "deckman.tmp")
	old := target + ".old"

	if err := os.WriteFile(target, []byte("v1"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tmp, []byte("v2"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := swapBinaryWithBackup(tmp, target, old); err != nil {
		t.Fatalf("swapBinaryWithBackup: %v", err)
	}
	if got, _ := os.ReadFile(target); string(got) != "v2" {
		t.Fatalf("target = %q, esperaba v2", got)
	}
	if got, _ := os.ReadFile(old); string(got) != "v1" {
		t.Fatalf("old = %q, esperaba v1", got)
	}

	// Rollback si tmp no existe.
	if err := swapBinaryWithBackup(filepath.Join(dir, "falta"), target, old); err == nil {
		t.Fatal("esperaba error si tmp no existe")
	}
	if got, _ := os.ReadFile(target); string(got) != "v2" {
		t.Fatalf("rollback fallido, target = %q, esperaba v2", got)
	}
}
