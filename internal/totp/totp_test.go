package totp

import (
	"testing"
	"time"
)

func TestRFC6238SixDigitSHA1(t *testing.T) {
	// RFC 6238 的 SHA-1 测试密钥，长度为测试向量要求的 20 字节。
	secret := "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ"
	at := time.Unix(59, 0)
	got, step, err := Code(secret, at)
	if err != nil || got != "287082" || step != 1 {
		t.Fatalf("Code = %q, %d, %v; want 287082 at step 1", got, step, err)
	}
	if got, ok := Verify(secret, "287082", at); !ok || got != 1 {
		t.Fatalf("Verify = %d, %v", got, ok)
	}
}

func TestVerifyWindowAndInput(t *testing.T) {
	secret, err := GenerateSecret()
	if err != nil {
		t.Fatal(err)
	}
	at := time.Unix(1_700_000_000, 0)
	previous, _, err := Code(secret, at.Add(-30*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := Verify(secret, previous, at); !ok {
		t.Fatal("previous time window should be accepted")
	}
	for _, invalid := range []string{"", "12345", "1234567", "12a456"} {
		if _, ok := Verify(secret, invalid, at); ok {
			t.Fatalf("accepted invalid code %q", invalid)
		}
	}
}

func TestProvisioningURI(t *testing.T) {
	uri := ProvisioningURI("ABC123", "a+b@example.com")
	if uri != "otpauth://totp/Danlu:a+b@example.com?secret=ABC123&issuer=Danlu&algorithm=SHA1&digits=6&period=30" {
		t.Fatalf("unexpected URI %q", uri)
	}
}
