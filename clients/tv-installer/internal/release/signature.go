package release

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/asn1"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"math/big"
)

const (
	maximumManifestSignatureBytes int64 = 4 * 1024
	manifestSignatureAlgorithm          = "ecdsa-p256-sha256"
	manifestSigningKeyID                = "4e9b15a0b6aed77908f3686fbf05a0a9c322ad846662eb758f56d4e65c22796f"
	manifestSigningPublicKey            = "MFkwEwYHKoZIzj0CAQYIKoZIzj0DAQcDQgAEacg8w48bnbKqa/KOJd070if0/100iHsU+o6ecokqIS6p7thhZb1ZR9YawxW7HuoEs5k6dW9sTCOyMjUcsgAQww=="
)

type manifestSignature struct {
	SchemaVersion  int    `json:"schemaVersion"`
	Algorithm      string `json:"algorithm"`
	KeyID          string `json:"keyId"`
	ManifestSHA256 string `json:"manifestSha256"`
	Signature      string `json:"signature"`
}

type derSignature struct{ R, S *big.Int }

func verifyManifestSignature(manifest, sidecar []byte) error {
	if len(sidecar) == 0 || int64(len(sidecar)) > maximumManifestSignatureBytes {
		return errors.New("update manifest signature size is invalid")
	}
	for _, field := range []string{"schemaVersion", "algorithm", "keyId", "manifestSha256", "signature"} {
		if bytes.Count(sidecar, []byte(`"`+field+`"`)) != 1 {
			return errors.New("update manifest signature fields are invalid")
		}
	}
	var value manifestSignature
	if err := strictJSON(sidecar, &value); err != nil {
		return err
	}
	if value.SchemaVersion != 1 || value.Algorithm != manifestSignatureAlgorithm || value.KeyID != manifestSigningKeyID {
		return errors.New("update manifest signature contract is not trusted")
	}
	digest := sha256.Sum256(manifest)
	if value.ManifestSHA256 != hex.EncodeToString(digest[:]) {
		return errors.New("update manifest signature digest does not match")
	}
	signature, err := base64.StdEncoding.Strict().DecodeString(value.Signature)
	if err != nil || base64.StdEncoding.EncodeToString(signature) != value.Signature {
		return errors.New("update manifest signature is not canonical base64")
	}
	var parsed derSignature
	rest, err := asn1.Unmarshal(signature, &parsed)
	if err != nil || len(rest) != 0 || parsed.R == nil || parsed.S == nil || parsed.R.Sign() <= 0 || parsed.S.Sign() <= 0 {
		return errors.New("update manifest signature DER is invalid")
	}
	canonical, err := asn1.Marshal(parsed)
	if err != nil || !bytes.Equal(canonical, signature) {
		return errors.New("update manifest signature DER is not canonical")
	}
	publicDER, err := base64.StdEncoding.Strict().DecodeString(manifestSigningPublicKey)
	if err != nil {
		return err
	}
	publicValue, err := x509.ParsePKIXPublicKey(publicDER)
	if err != nil {
		return err
	}
	publicKey, ok := publicValue.(*ecdsa.PublicKey)
	if !ok || parsed.R.Cmp(publicKey.Params().N) >= 0 || parsed.S.Cmp(publicKey.Params().N) >= 0 || !ecdsa.VerifyASN1(publicKey, digest[:], signature) {
		return errors.New("update manifest signature is invalid")
	}
	return nil
}
