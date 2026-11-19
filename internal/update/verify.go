package update

import (
	"bufio"
	"bytes"
	"crypto/ecdsa"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"strings"

	"github.com/jfrmorales/deckman/internal/i18n"
)

// DefaultCosignPubKeyPEM es la clave publica de firma de releases fijada en el
// binario desde v0.6.4 (huella SHA-256:
// 29e5cbe2bbd71402bdea9db4a6d4cf4fc99dab59dd4c9cb58a010f6801520544).
// Se fija aqui a proposito para no confiar en una clave descargada de la misma
// release que se pretende verificar.
const DefaultCosignPubKeyPEM = "-----BEGIN PUBLIC KEY-----\n" +
	"MFkwEwYHKoZIzj0CAQYIKoZIzj0DAQcDQgAEIHSGD3YCvJPgiTv1QQKvWd7z2Pre\n" +
	"tFMjg2SrGy9ldh0vV7WW8aitMq0n/ln7ry4JM3oxi8GdOZHesZuhuZn0ww==\n" +
	"-----END PUBLIC KEY-----\n"

// LookupSHA256 busca el resumen SHA-256 del fichero indicado dentro del
// contenido de un SHA256SUMS estandar.
func LookupSHA256(sums []byte, filename string) ([]byte, error) {
	scanner := bufio.NewScanner(bytes.NewReader(sums))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		// sha256sum escribe "<64-hex>  <nombre>" o "<64-hex> *<nombre>".
		idx := strings.IndexAny(line, " \t")
		if idx < 0 {
			continue
		}
		hexPart := line[:idx]
		if len(hexPart) != sha256.Size*2 {
			continue
		}
		digest, err := hex.DecodeString(hexPart)
		if err != nil || len(digest) != sha256.Size {
			continue
		}
		namePart := strings.TrimSpace(line[idx+1:])
		namePart = strings.TrimPrefix(namePart, "*")
		namePart = strings.TrimPrefix(namePart, "./")
		if namePart == filename {
			return digest, nil
		}
	}
	return nil, i18n.Errorf("no se encontro la suma SHA-256 de %s en SHA256SUMS", filename)
}

// VerifySHA256 comprueba en tiempo constante que dos resumenes SHA-256 coinciden.
func VerifySHA256(actual, expected []byte) error {
	if len(actual) != sha256.Size || len(expected) != sha256.Size {
		return i18n.Errorf("la suma SHA-256 del fichero descargado no coincide")
	}
	if subtle.ConstantTimeCompare(actual, expected) != 1 {
		return i18n.Errorf("la suma SHA-256 del fichero descargado no coincide")
	}
	return nil
}

type cosignBundle struct {
	MediaType        string `json:"mediaType"`
	MessageSignature *struct {
		MessageDigest struct {
			Algorithm string `json:"algorithm"`
			Digest    string `json:"digest"`
		} `json:"messageDigest"`
		Signature string `json:"signature"`
	} `json:"messageSignature"`
}

// VerifyCosignBundle verifica un bundle JSON de cosign v0.3 (`sign-blob --bundle`)
// sobre data usando la clave publica ECDSA P-256 en formato PEM.
//
// No verifica offline el inclusionProof del registro Rekor porque el modelo de
// confianza del autoactualizador se apoya en la clave publica fijada en el
// binario: quien no tenga la clave privada no puede producir una firma valida.
func VerifyCosignBundle(pubPEM, data, bundleJSON []byte) error {
	block, _ := pem.Decode(pubPEM)
	if block == nil {
		return i18n.Errorf("la firma criptografica de SHA256SUMS no es valida")
	}
	pubAny, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return i18n.Errorf("la firma criptografica de SHA256SUMS no es valida")
	}
	pub, ok := pubAny.(*ecdsa.PublicKey)
	if !ok {
		return i18n.Errorf("la firma criptografica de SHA256SUMS no es valida")
	}

	var b cosignBundle
	if err := json.Unmarshal(bundleJSON, &b); err != nil {
		return i18n.Errorf("la firma criptografica de SHA256SUMS no es valida")
	}
	if b.MessageSignature == nil || b.MessageSignature.MessageDigest.Algorithm != "SHA2_256" {
		return i18n.Errorf("la firma criptografica de SHA256SUMS no es valida")
	}

	digest, err := base64.StdEncoding.DecodeString(b.MessageSignature.MessageDigest.Digest)
	if err != nil || len(digest) != sha256.Size {
		return i18n.Errorf("la firma criptografica de SHA256SUMS no es valida")
	}
	sig, err := base64.StdEncoding.DecodeString(b.MessageSignature.Signature)
	if err != nil || len(sig) == 0 {
		return i18n.Errorf("la firma criptografica de SHA256SUMS no es valida")
	}

	sum := sha256.Sum256(data)
	if subtle.ConstantTimeCompare(digest, sum[:]) != 1 {
		return i18n.Errorf("la firma criptografica de SHA256SUMS no es valida")
	}
	if !ecdsa.VerifyASN1(pub, sum[:], sig) {
		return i18n.Errorf("la firma criptografica de SHA256SUMS no es valida")
	}
	return nil
}
