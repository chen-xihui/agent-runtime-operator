package telemetry

import (
	"testing"
)

func TestRedactMap(t *testing.T) {
	fields := map[string]interface{}{
		"tenant":      "tenant-a",
		"agent":       "code-reviewer",
		"token":       "abc123",
		"api_key":     "secret-key",
		"result":      "ok",
		"password":    "p@ss",
		"credentials": "user:pass",
	}
	redacted := RedactMap(fields)

	if redacted["tenant"] != "tenant-a" {
		t.Fatalf("non-sensitive field should not be redacted: %v", redacted["tenant"])
	}
	if redacted["result"] != "ok" {
		t.Fatalf("result should not be redacted: %v", redacted["result"])
	}
	if redacted["token"] != "[REDACTED]" {
		t.Fatalf("token should be redacted: %v", redacted["token"])
	}
	if redacted["api_key"] != "[REDACTED]" {
		t.Fatalf("api_key should be redacted: %v", redacted["api_key"])
	}
	if redacted["password"] != "[REDACTED]" {
		t.Fatalf("password should be redacted: %v", redacted["password"])
	}
	// credentials 匹配 credential 前缀
	if redacted["credentials"] != "[REDACTED]" {
		t.Fatalf("credentials should be redacted: %v", redacted["credentials"])
	}
}

func TestRedactString(t *testing.T) {
	s := "call db.query with token=abc123 and password=p@ss"
	redacted := RedactString(s)
	if contains(redacted, "abc123") {
		t.Fatalf("token value leaked: %q", redacted)
	}
	if contains(redacted, "p@ss") {
		t.Fatalf("password value leaked: %q", redacted)
	}
	// 非敏感内容保留
	if !contains(redacted, "db.query") {
		t.Fatalf("non-sensitive content lost: %q", redacted)
	}
}

func TestIsSensitiveKey(t *testing.T) {
	cases := map[string]bool{
		"token":       true,
		"api_key":     true,
		"password":    true,
		"tenant":      false,
		"agent":       false,
		"auth_token":  true,
		"credential_x": true,
	}
	for k, want := range cases {
		if got := IsSensitiveKey(k); got != want {
			t.Fatalf("IsSensitiveKey(%q) = %v, want %v", k, got, want)
		}
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
