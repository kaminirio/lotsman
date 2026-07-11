package auth

import "testing"

func TestHashAndComparePassword(t *testing.T) {
	hash, err := HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	if hash == "correct horse battery staple" {
		t.Fatal("hash must not equal the plaintext")
	}

	cases := []struct {
		name     string
		hash     string
		password string
		want     bool
	}{
		{"correct password", hash, "correct horse battery staple", true},
		{"wrong password", hash, "wrong", false},
		{"empty password vs hash", hash, "", false},
		{"empty hash (sso-only account)", "", "anything", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ComparePassword(tc.hash, tc.password); got != tc.want {
				t.Errorf("ComparePassword = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestHashPasswordRejectsEmpty(t *testing.T) {
	if _, err := HashPassword(""); err == nil {
		t.Fatal("empty password must be rejected")
	}
}
