package bedrock

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

// Fixture values below were computed by an independent Python reference
// implementation of SigV4 (hashlib/hmac, not this package) against the same
// canonical request — see the derivation script kept alongside this task's
// notes. This cross-checks the Go implementation against a second,
// independently-written signer rather than merely re-deriving the same
// arithmetic twice in Go.
func TestSignRequestMatchesIndependentReference(t *testing.T) {
	body := []byte(`{"anthropic_version":"bedrock-2023-05-31","messages":[{"role":"user","content":"hi"}],"max_tokens":16}`)
	req, err := http.NewRequest(http.MethodPost,
		"https://bedrock-runtime.us-east-1.amazonaws.com/model/anthropic.claude-3-haiku/invoke",
		strings.NewReader(string(body)))
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")

	fixedTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	creds := Credentials{AccessKeyID: "AKIDEXAMPLE", SecretAccessKey: "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY"}
	SignRequest(req, body, "us-east-1", "bedrock", creds, fixedTime)

	wantAuth := "AWS4-HMAC-SHA256 Credential=AKIDEXAMPLE/20240101/us-east-1/bedrock/aws4_request, " +
		"SignedHeaders=content-type;host;x-amz-date, " +
		"Signature=a69ea9b35ec3a9fd9bb6528515bb3a177bc4529d9712c8ccd680da578d3d585f"
	if got := req.Header.Get("Authorization"); got != wantAuth {
		t.Fatalf("Authorization header mismatch:\n got: %s\nwant: %s", got, wantAuth)
	}
	if got := req.Header.Get("x-amz-date"); got != "20240101T000000Z" {
		t.Fatalf("x-amz-date = %q", got)
	}
}

func TestSignRequestDeterministic(t *testing.T) {
	body := []byte(`{"max_tokens":1}`)
	creds := Credentials{AccessKeyID: "AKID", SecretAccessKey: "secret"}
	fixedTime := time.Date(2024, 6, 15, 12, 0, 0, 0, time.UTC)

	sign := func() string {
		req, _ := http.NewRequest(http.MethodPost, "https://bedrock-runtime.us-east-1.amazonaws.com/model/x/invoke", strings.NewReader(string(body)))
		SignRequest(req, body, "us-east-1", "bedrock", creds, fixedTime)
		return req.Header.Get("Authorization")
	}

	a, b := sign(), sign()
	if a != b {
		t.Fatalf("signing the same request twice produced different signatures:\n%s\n%s", a, b)
	}
}

func TestSignRequestSessionTokenHeader(t *testing.T) {
	body := []byte(`{}`)
	req, _ := http.NewRequest(http.MethodPost, "https://bedrock-runtime.us-east-1.amazonaws.com/model/x/invoke", strings.NewReader(string(body)))
	creds := Credentials{AccessKeyID: "AKID", SecretAccessKey: "secret", SessionToken: "tok123"}
	SignRequest(req, body, "us-east-1", "bedrock", creds, time.Now())

	if req.Header.Get("x-amz-security-token") != "tok123" {
		t.Fatal("session token header not set")
	}
	if !strings.Contains(req.Header.Get("Authorization"), "x-amz-security-token") {
		t.Fatal("session token header must be part of SignedHeaders when present")
	}
}

func TestSignRequestSensitiveToBodyChange(t *testing.T) {
	creds := Credentials{AccessKeyID: "AKID", SecretAccessKey: "secret"}
	fixedTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)

	sign := func(body string) string {
		req, _ := http.NewRequest(http.MethodPost, "https://bedrock-runtime.us-east-1.amazonaws.com/model/x/invoke", strings.NewReader(body))
		SignRequest(req, []byte(body), "us-east-1", "bedrock", creds, fixedTime)
		return req.Header.Get("Authorization")
	}

	if sign(`{"a":1}`) == sign(`{"a":2}`) {
		t.Fatal("different payloads must not produce the same signature")
	}
}
