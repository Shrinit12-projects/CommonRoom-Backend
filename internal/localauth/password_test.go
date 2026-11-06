package localauth

import (
	"encoding/hex"
	"testing"
)

func TestPBKDF2SHA256Vector(t *testing.T) {
	want, _ := hex.DecodeString("120fb6cffcf8b32c43e7225256c4f837a86548c92ccc35480805987cb70be17b")
	if got := derive("password", []byte("salt"), 1); string(got) != string(want) {
		t.Fatalf("PBKDF2 vector mismatch: %x", got)
	}
}

func TestPasswordHashVerifiesOnlyMatchingPassword(t *testing.T) {
	salt, hash, err := Hash("a long local development password")
	if err != nil {
		t.Fatal(err)
	}
	if !Verify("a long local development password", salt, hash, Iterations) {
		t.Fatal("correct password did not verify")
	}
	if Verify("different password", salt, hash, Iterations) {
		t.Fatal("incorrect password verified")
	}
}
