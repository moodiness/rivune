package password

import (
	"errors"
	"strings"
	"testing"
)

func TestHashAndVerify(t *testing.T) {
	hash, err := Hash("correct-horse-battery-staple")
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	if hash == "correct-horse-battery-staple" {
		t.Fatal("password was stored as plain text")
	}

	matches, err := Verify("correct-horse-battery-staple", hash)
	if err != nil {
		t.Fatalf("verify password: %v", err)
	}
	if !matches {
		t.Fatal("correct password did not match")
	}

	matches, err = Verify("wrong-password", hash)
	if err != nil {
		t.Fatalf("verify wrong password: %v", err)
	}
	if matches {
		t.Fatal("wrong password matched")
	}
}

func TestHashUsesUniqueSalt(t *testing.T) {
	first, err := Hash("correct-horse-battery-staple")
	if err != nil {
		t.Fatalf("hash first password: %v", err)
	}
	second, err := Hash("correct-horse-battery-staple")
	if err != nil {
		t.Fatalf("hash second password: %v", err)
	}
	if first == second {
		t.Fatal("two password hashes used the same salt")
	}
}

func TestVerifyRejectsMalformedHash(t *testing.T) {
	hash, err := Hash("password")
	if err != nil {
		t.Fatal(err)
	}
	mutations := map[string]string{
		"not PHC":               "not-a-password-hash",
		"wrong version":         strings.Replace(hash, "$v=19$", "$v=18$", 1),
		"excessive memory":      strings.Replace(hash, "$m=65536,t=3,p=2$", "$m=4294967295,t=3,p=2$", 1),
		"excessive iterations":  strings.Replace(hash, "$m=65536,t=3,p=2$", "$m=65536,t=4294967295,p=2$", 1),
		"excessive parallelism": strings.Replace(hash, "$m=65536,t=3,p=2$", "$m=65536,t=3,p=255$", 1),
		"trailing parameters":   strings.Replace(hash, "$m=65536,t=3,p=2$", "$m=65536,t=3,p=2,extra=1$", 1),
	}
	parts := strings.Split(hash, "$")
	shortSalt := append([]string(nil), parts...)
	shortSalt[4] = "AA"
	mutations["short salt"] = strings.Join(shortSalt, "$")
	shortDigest := append([]string(nil), parts...)
	shortDigest[5] = "AA"
	mutations["short digest"] = strings.Join(shortDigest, "$")

	for name, malformed := range mutations {
		t.Run(name, func(t *testing.T) {
			if _, err := Verify("password", malformed); !errors.Is(err, ErrInvalidHash) {
				t.Fatalf("Verify error = %v, want %v", err, ErrInvalidHash)
			}
		})
	}
}
