package main

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/asn1"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"math/big"
	"os"
)

const (
	signatureSchemaVersion = 1
	signatureAlgorithm     = "ecdsa-p256-sha256"
	maximumSignatureBytes  = 4 * 1024
)

type manifestSignature struct {
	SchemaVersion  int    `json:"schemaVersion"`
	Algorithm      string `json:"algorithm"`
	KeyID          string `json:"keyId"`
	ManifestSHA256 string `json:"manifestSha256"`
	Signature      string `json:"signature"`
}

type ecdsaSignature struct {
	R, S *big.Int
}

func signManifest(manifestPath, privateKeyPath, outputPath string) error {
	manifest, err := os.ReadFile(manifestPath)
	if err != nil {
		return fmt.Errorf("read manifest: %w", err)
	}
	privateKey, err := readP256PrivateKey(privateKeyPath)
	if err != nil {
		return err
	}
	digest := sha256.Sum256(manifest)
	signature, err := ecdsa.SignASN1(rand.Reader, privateKey, digest[:])
	if err != nil {
		return fmt.Errorf("sign manifest: %w", err)
	}
	publicDER, err := x509.MarshalPKIXPublicKey(&privateKey.PublicKey)
	if err != nil {
		return fmt.Errorf("encode public key: %w", err)
	}
	keyDigest := sha256.Sum256(publicDER)
	sidecar := manifestSignature{
		SchemaVersion:  signatureSchemaVersion,
		Algorithm:      signatureAlgorithm,
		KeyID:          hex.EncodeToString(keyDigest[:]),
		ManifestSHA256: hex.EncodeToString(digest[:]),
		Signature:      base64.StdEncoding.EncodeToString(signature),
	}
	encoded, err := json.Marshal(sidecar)
	if err != nil {
		return err
	}
	encoded = append(encoded, '\n')
	if len(encoded) > maximumSignatureBytes {
		return errors.New("signature sidecar exceeds 4 KiB")
	}
	if err := os.WriteFile(outputPath, encoded, 0o644); err != nil {
		return fmt.Errorf("write signature sidecar: %w", err)
	}
	return nil
}

func verifyManifestSignature(manifestPath, signaturePath, publicKeyPath, publicKeyBase64 string) error {
	manifest, err := os.ReadFile(manifestPath)
	if err != nil {
		return fmt.Errorf("read manifest: %w", err)
	}
	sidecarBytes, err := os.ReadFile(signaturePath)
	if err != nil {
		return fmt.Errorf("read signature sidecar: %w", err)
	}
	publicKey, publicDER, err := readP256PublicKey(publicKeyPath, publicKeyBase64)
	if err != nil {
		return err
	}
	return verifySignatureBytes(manifest, sidecarBytes, publicKey, publicDER)
}

func verifySignatureBytes(manifest, sidecarBytes []byte, publicKey *ecdsa.PublicKey, publicDER []byte) error {
	if len(sidecarBytes) == 0 || len(sidecarBytes) > maximumSignatureBytes {
		return errors.New("signature sidecar size is invalid")
	}
	var sidecar manifestSignature
	decoder := json.NewDecoder(bytes.NewReader(sidecarBytes))
	duplicateDecoder := json.NewDecoder(bytes.NewReader(sidecarBytes))
	if err := consumeUniqueJSONValue(duplicateDecoder, "signature sidecar"); err != nil {
		return err
	}
	if _, err := duplicateDecoder.Token(); !errors.Is(err, io.EOF) {
		return errors.New("signature sidecar must contain exactly one JSON document")
	}
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&sidecar); err != nil {
		return fmt.Errorf("decode signature sidecar: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("signature sidecar must contain exactly one JSON document")
	}
	if sidecar.SchemaVersion != signatureSchemaVersion || sidecar.Algorithm != signatureAlgorithm {
		return errors.New("signature sidecar contract is unsupported")
	}
	keyDigest := sha256.Sum256(publicDER)
	if sidecar.KeyID != hex.EncodeToString(keyDigest[:]) {
		return errors.New("signature sidecar key ID is not trusted")
	}
	digest := sha256.Sum256(manifest)
	if sidecar.ManifestSHA256 != hex.EncodeToString(digest[:]) {
		return errors.New("signature sidecar manifest digest does not match")
	}
	signature, err := base64.StdEncoding.Strict().DecodeString(sidecar.Signature)
	if err != nil || base64.StdEncoding.EncodeToString(signature) != sidecar.Signature {
		return errors.New("signature sidecar signature is not canonical base64")
	}
	var parsed ecdsaSignature
	rest, err := asn1.Unmarshal(signature, &parsed)
	if err != nil || len(rest) != 0 || parsed.R == nil || parsed.S == nil || parsed.R.Sign() <= 0 || parsed.S.Sign() <= 0 || parsed.R.Cmp(publicKey.Params().N) >= 0 || parsed.S.Cmp(publicKey.Params().N) >= 0 {
		return errors.New("signature sidecar signature is not valid ECDSA DER")
	}
	canonical, err := asn1.Marshal(parsed)
	if err != nil || !bytes.Equal(canonical, signature) {
		return errors.New("signature sidecar signature is not canonical ECDSA DER")
	}
	if !ecdsa.VerifyASN1(publicKey, digest[:], signature) {
		return errors.New("manifest signature verification failed")
	}
	return nil
}

func readP256PrivateKey(path string) (*ecdsa.PrivateKey, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("stat private key: %w", err)
	}
	if info.Mode().Perm()&0o077 != 0 {
		return nil, errors.New("private key permissions must not grant group or other access")
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read private key: %w", err)
	}
	block, rest := pem.Decode(contents)
	if block == nil || len(bytes.TrimSpace(rest)) != 0 {
		return nil, errors.New("private key must contain exactly one PEM block")
	}
	var key any
	switch block.Type {
	case "PRIVATE KEY":
		key, err = x509.ParsePKCS8PrivateKey(block.Bytes)
	case "EC PRIVATE KEY":
		key, err = x509.ParseECPrivateKey(block.Bytes)
	default:
		return nil, errors.New("private key PEM type is unsupported")
	}
	if err != nil {
		return nil, fmt.Errorf("parse private key: %w", err)
	}
	privateKey, ok := key.(*ecdsa.PrivateKey)
	if !ok || privateKey.Curve != elliptic.P256() {
		return nil, errors.New("private key must be ECDSA P-256")
	}
	return privateKey, nil
}

func readP256PublicKey(path, encoded string) (*ecdsa.PublicKey, []byte, error) {
	if (path == "") == (encoded == "") {
		return nil, nil, errors.New("exactly one public key source is required")
	}
	var der []byte
	var err error
	if encoded != "" {
		der, err = base64.StdEncoding.Strict().DecodeString(encoded)
		if err != nil || base64.StdEncoding.EncodeToString(der) != encoded {
			return nil, nil, errors.New("public key is not canonical base64")
		}
	} else {
		contents, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil, nil, fmt.Errorf("read public key: %w", readErr)
		}
		if block, rest := pem.Decode(contents); block != nil {
			if block.Type != "PUBLIC KEY" || len(bytes.TrimSpace(rest)) != 0 {
				return nil, nil, errors.New("public key must contain exactly one PUBLIC KEY PEM block")
			}
			der = block.Bytes
		} else {
			der = contents
		}
	}
	parsed, err := x509.ParsePKIXPublicKey(der)
	if err != nil {
		return nil, nil, fmt.Errorf("parse public key: %w", err)
	}
	publicKey, ok := parsed.(*ecdsa.PublicKey)
	if !ok || publicKey.Curve != elliptic.P256() {
		return nil, nil, errors.New("public key must be ECDSA P-256")
	}
	canonical, err := x509.MarshalPKIXPublicKey(publicKey)
	if err != nil || !bytes.Equal(canonical, der) {
		return nil, nil, errors.New("public key is not canonical SPKI DER")
	}
	return publicKey, der, nil
}
