package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSignAndVerifyManifestRoundTrip(t *testing.T) {
	directory := t.TempDir()
	manifestPath := filepath.Join(directory, "rivune-update.json")
	privatePath := filepath.Join(directory, "private.pem")
	signaturePath := manifestPath + ".sig"
	manifest := []byte("{\"schemaVersion\":3}\n")
	if err := os.WriteFile(manifestPath, manifest, 0o600); err != nil {
		t.Fatal(err)
	}
	privateKey, publicDER := writeTestKey(t, privatePath)
	if err := signManifest(manifestPath, privatePath, signaturePath); err != nil {
		t.Fatal(err)
	}
	if err := verifyManifestSignature(manifestPath, signaturePath, "", base64.StdEncoding.EncodeToString(publicDER)); err != nil {
		t.Fatal(err)
	}

	sidecarBytes, err := os.ReadFile(signaturePath)
	if err != nil {
		t.Fatal(err)
	}
	var sidecar manifestSignature
	if err := json.Unmarshal(sidecarBytes, &sidecar); err != nil {
		t.Fatal(err)
	}
	keyDigest := sha256.Sum256(publicDER)
	if sidecar.SchemaVersion != 1 || sidecar.Algorithm != signatureAlgorithm || sidecar.KeyID != hex.EncodeToString(keyDigest[:]) {
		t.Fatalf("unexpected sidecar: %#v", sidecar)
	}
	if privateKey.Curve != elliptic.P256() {
		t.Fatal("fixture curve changed")
	}
}

func TestSignatureVerificationRejectsTamperingAndMalformedSidecars(t *testing.T) {
	directory := t.TempDir()
	manifestPath := filepath.Join(directory, "manifest.json")
	privatePath := filepath.Join(directory, "private.pem")
	signaturePath := filepath.Join(directory, "manifest.json.sig")
	manifest := []byte("signed manifest bytes")
	if err := os.WriteFile(manifestPath, manifest, 0o600); err != nil {
		t.Fatal(err)
	}
	_, publicDER := writeTestKey(t, privatePath)
	publicBase64 := base64.StdEncoding.EncodeToString(publicDER)
	if err := signManifest(manifestPath, privatePath, signaturePath); err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(signaturePath)
	if err != nil {
		t.Fatal(err)
	}
	var valid manifestSignature
	if err := json.Unmarshal(original, &valid); err != nil {
		t.Fatal(err)
	}

	cases := map[string][]byte{
		"altered manifest digest": mutateSidecar(t, valid, func(value *manifestSignature) { value.ManifestSHA256 = strings.Repeat("0", 64) }),
		"unknown key":             mutateSidecar(t, valid, func(value *manifestSignature) { value.KeyID = strings.Repeat("0", 64) }),
		"malformed base64":        mutateSidecar(t, valid, func(value *manifestSignature) { value.Signature = "%%%" }),
		"malformed DER":           mutateSidecar(t, valid, func(value *manifestSignature) { value.Signature = base64.StdEncoding.EncodeToString([]byte{1, 2, 3}) }),
		"unknown field":           append(original[:len(original)-2], []byte(",\"future\":true}\n")...),
		"oversized":               []byte(strings.Repeat(" ", maximumSignatureBytes+1)),
		"empty":                   {},
	}
	for name, bytes := range cases {
		t.Run(name, func(t *testing.T) {
			if err := os.WriteFile(signaturePath, bytes, 0o600); err != nil {
				t.Fatal(err)
			}
			if err := verifyManifestSignature(manifestPath, signaturePath, "", publicBase64); err == nil {
				t.Fatal("verification succeeded")
			}
		})
	}
	if err := os.WriteFile(signaturePath, original, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifestPath, append(manifest, '!'), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := verifyManifestSignature(manifestPath, signaturePath, "", publicBase64); err == nil {
		t.Fatal("altered manifest verified")
	}
}

func writeTestKey(t *testing.T, path string) (*ecdsa.PrivateKey, []byte) {
	t.Helper()
	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	privateDER, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privateDER}), 0o600); err != nil {
		t.Fatal(err)
	}
	publicDER, err := x509.MarshalPKIXPublicKey(&privateKey.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	return privateKey, publicDER
}

func mutateSidecar(t *testing.T, original manifestSignature, mutate func(*manifestSignature)) []byte {
	t.Helper()
	mutate(&original)
	encoded, err := json.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}
	return append(encoded, '\n')
}
