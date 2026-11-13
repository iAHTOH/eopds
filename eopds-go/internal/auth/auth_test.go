package auth

import "testing"

func TestHashVerify(t *testing.T) {
	h, err := HashPassword("secret123")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	ok, err := VerifyPassword("secret123", h)
	if err != nil || !ok {
		t.Fatalf("verify correct: ok=%v err=%v", ok, err)
	}
	ok, err = VerifyPassword("wrong", h)
	if err != nil {
		t.Fatalf("verify wrong err: %v", err)
	}
	if ok {
		t.Fatal("wrong password accepted")
	}
	if _, err := VerifyPassword("x", "garbage"); err == nil {
		t.Fatal("expected error for invalid hash")
	}
}
