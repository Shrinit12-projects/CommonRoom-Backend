package localauth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
)

const Iterations = 310000

func Hash(password string) (salt, hash []byte, err error) {
	salt = make([]byte, 16)
	if _, err = rand.Read(salt); err != nil {
		return nil, nil, err
	}
	return salt, derive(password, salt, Iterations), nil
}

func Verify(password string, salt, expected []byte, iterations int) bool {
	if len(salt) != 16 || len(expected) != sha256.Size || iterations < 200000 || iterations > 1000000 {
		return false
	}
	actual := derive(password, salt, iterations)
	return subtle.ConstantTimeCompare(actual, expected) == 1
}

func derive(password string, salt []byte, iterations int) []byte {
	mac := hmac.New(sha256.New, []byte(password))
	block := append(append([]byte(nil), salt...), 0, 0, 0, 1)
	mac.Write(block)
	u := mac.Sum(nil)
	out := append([]byte(nil), u...)
	for i := 1; i < iterations; i++ {
		mac.Reset()
		mac.Write(u)
		u = mac.Sum(u[:0])
		for j := range out {
			out[j] ^= u[j]
		}
	}
	return out
}
