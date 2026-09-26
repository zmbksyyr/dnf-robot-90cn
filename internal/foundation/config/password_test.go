package config

import "testing"

func TestHashWebPasswordRoundTrip(t *testing.T) {
	hash, err := HashWebPassword("s3cret-password")
	if err != nil {
		t.Fatal(err)
	}
	if VerifyWebPassword("", hash, "s3cret-password") != true {
		t.Fatal("hash did not verify the correct password")
	}
	if VerifyWebPassword("", hash, "wrong-password") {
		t.Fatal("hash verified a wrong password")
	}
	if VerifyWebPassword("", hash, "") {
		t.Fatal("hash verified an empty password")
	}
}

func TestVerifyWebPasswordFallsBackToPlaintext(t *testing.T) {
	if !VerifyWebPassword("twadmin", "", "twadmin") {
		t.Fatal("plaintext password did not verify")
	}
	if VerifyWebPassword("twadmin", "", "other") {
		t.Fatal("plaintext password verified a wrong value")
	}
}

func TestVerifyWebPasswordRejectsMalformedHash(t *testing.T) {
	for _, hash := range []string{
		"pbkdf2-sha256$0$c2FsdA$a2V5",
		"pbkdf2-sha256$not-a-number$c2FsdA$a2V5",
		"pbkdf2-sha256$1000$!!!$a2V5",
		"pbkdf2-sha256$1000$c2FsdA$",
		"other-scheme$1000$c2FsdA$a2V5",
		"garbage",
	} {
		if VerifyWebPassword("", hash, "whatever") {
			t.Fatalf("malformed hash %q verified a password", hash)
		}
	}
}
