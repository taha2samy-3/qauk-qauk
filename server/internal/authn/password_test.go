package authn

import "testing"

// Generated with Django 5.2: make_password("s3cret-Pass!").
const djangoHash = "pbkdf2_sha256$1000000$POH1cDNkIXwzcmgW69izsU$4k5B6uBqBG2oHZH9oigY2q1LsiyxQeIR3D8AbcQLztc="

func TestDjangoPBKDF2(t *testing.T) {
	ok, rehash, err := VerifyPassword(djangoHash, "s3cret-Pass!")
	if err != nil || !ok || !rehash {
		t.Fatalf("valid Django password: ok=%v rehash=%v err=%v", ok, rehash, err)
	}
	if ok, _, _ := VerifyPassword(djangoHash, "wrong"); ok {
		t.Fatal("wrong password accepted")
	}
}

func TestArgon2RoundTrip(t *testing.T) {
	h, err := HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	if ok, rehash, err := VerifyPassword(h, "correct horse battery staple"); !ok || rehash || err != nil {
		t.Fatalf("ok=%v rehash=%v err=%v", ok, rehash, err)
	}
	if ok, _, _ := VerifyPassword(h, "Correct horse battery staple"); ok {
		t.Fatal("wrong password accepted")
	}
	h2, _ := HashPassword("correct horse battery staple")
	if h == h2 {
		t.Fatal("hashes must be salted")
	}
}

func TestUnknownAndMalformedHashes(t *testing.T) {
	for _, h := range []string{"", "!unusable", "md5$abc$def", "bcrypt$x", "pbkdf2_sha256$x$salt$hash", "$argon2id$v=19$garbage"} {
		if ok, _, _ := VerifyPassword(h, "anything"); ok {
			t.Fatalf("hash %q accepted a password", h)
		}
	}
}

func TestSessionToken(t *testing.T) {
	tok, hash, err := NewSessionToken()
	if err != nil || len(tok) < 40 {
		t.Fatalf("token %q err %v", tok, err)
	}
	if string(HashToken(tok)) != string(hash) {
		t.Fatal("hash mismatch")
	}
}
