package password

import "testing"

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
	if _, err := Verify("password", "not-a-password-hash"); err == nil {
		t.Fatal("expected malformed hash to be rejected")
	}
}
