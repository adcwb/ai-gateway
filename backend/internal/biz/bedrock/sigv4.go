// Package bedrock implements the client-side mechanics needed to call AWS
// Bedrock's Anthropic-model Invoke API: SigV4 request signing and the
// event-stream binary framing used by InvokeModelWithResponseStream. It is
// dependency-free with respect to package biz — the same split used for
// internal/biz/mcpgw, internal/biz/guardrail and internal/biz/vectorindex —
// so the Bedrock outbound dialect branch in internal/biz/protocol_bedrock.go
// is the only consumer.
package bedrock

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

const (
	algorithm     = "AWS4-HMAC-SHA256"
	amzDateFormat = "20060102T150405Z"
	dateFormat    = "20060102"
)

// Credentials is an AWS access key/secret key pair with an optional session
// token (for temporary/STS credentials).
type Credentials struct {
	AccessKeyID     string
	SecretAccessKey string
	SessionToken    string
}

// SignRequest signs req in-place with AWS Signature Version 4. req.URL and
// req.Method must already be final; body is the exact bytes that will be
// sent (SigV4 signs a hash of the payload, so req.Body must not be re-read
// or mutated after signing — the caller is expected to set req.Body itself,
// SignRequest only reads body to compute the payload hash).
func SignRequest(req *http.Request, body []byte, region, service string, creds Credentials, t time.Time) {
	amzDate := t.UTC().Format(amzDateFormat)
	dateStamp := t.UTC().Format(dateFormat)

	req.Header.Set("x-amz-date", amzDate)
	if creds.SessionToken != "" {
		req.Header.Set("x-amz-security-token", creds.SessionToken)
	}
	if req.Header.Get("host") == "" {
		req.Header.Set("Host", req.URL.Host)
	}

	canonicalHeaders, signedHeaders := canonicalizeHeaders(req)
	payloadHash := hashHex(body)

	canonicalRequest := strings.Join([]string{
		req.Method,
		canonicalURI(req.URL.Path),
		canonicalQueryString(req.URL.Query()),
		canonicalHeaders,
		signedHeaders,
		payloadHash,
	}, "\n")

	credentialScope := strings.Join([]string{dateStamp, region, service, "aws4_request"}, "/")
	stringToSign := strings.Join([]string{
		algorithm,
		amzDate,
		credentialScope,
		hashHex([]byte(canonicalRequest)),
	}, "\n")

	signingKey := deriveSigningKey(creds.SecretAccessKey, dateStamp, region, service)
	signature := hex.EncodeToString(hmacSHA256(signingKey, stringToSign))

	authHeader := fmt.Sprintf("%s Credential=%s/%s, SignedHeaders=%s, Signature=%s",
		algorithm, creds.AccessKeyID, credentialScope, signedHeaders, signature)
	req.Header.Set("Authorization", authHeader)
}

func deriveSigningKey(secretKey, dateStamp, region, service string) []byte {
	kDate := hmacSHA256([]byte("AWS4"+secretKey), dateStamp)
	kRegion := hmacSHA256(kDate, region)
	kService := hmacSHA256(kRegion, service)
	return hmacSHA256(kService, "aws4_request")
}

func hmacSHA256(key []byte, data string) []byte {
	h := hmac.New(sha256.New, key)
	h.Write([]byte(data))
	return h.Sum(nil)
}

func hashHex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// canonicalURI URI-encodes each path segment per SigV4 rules (RFC 3986
// unreserved chars untouched, "/" preserved as the segment separator).
func canonicalURI(path string) string {
	if path == "" {
		return "/"
	}
	segments := strings.Split(path, "/")
	for i, s := range segments {
		segments[i] = uriEncode(s, false)
	}
	return strings.Join(segments, "/")
}

func canonicalQueryString(q url.Values) string {
	if len(q) == 0 {
		return ""
	}
	keys := make([]string, 0, len(q))
	for k := range q {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var parts []string
	for _, k := range keys {
		vals := append([]string(nil), q[k]...)
		sort.Strings(vals)
		for _, v := range vals {
			parts = append(parts, uriEncode(k, true)+"="+uriEncode(v, true))
		}
	}
	return strings.Join(parts, "&")
}

// uriEncode implements SigV4's URI-encoding: RFC 3986 unreserved characters
// (A-Z a-z 0-9 - _ . ~) pass through unescaped; everything else is
// percent-encoded in uppercase hex. When encodeSlash is false, "/" also
// passes through unescaped (used for path segments, never for query/header
// values).
func uriEncode(s string, encodeSlash bool) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if isUnreserved(c) || (c == '/' && !encodeSlash) {
			b.WriteByte(c)
			continue
		}
		fmt.Fprintf(&b, "%%%02X", c)
	}
	return b.String()
}

func isUnreserved(c byte) bool {
	return (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') ||
		c == '-' || c == '_' || c == '.' || c == '~'
}

// canonicalizeHeaders returns (canonicalHeaders, signedHeaders). SigV4 requires
// every header name lowercased, values trimmed, sorted by name; Host is
// synthesized from req.Host/req.URL since net/http moves it out of req.Header.
func canonicalizeHeaders(req *http.Request) (string, string) {
	headers := map[string]string{}
	for name, vals := range req.Header {
		lname := strings.ToLower(name)
		trimmed := make([]string, len(vals))
		for i, v := range vals {
			trimmed[i] = strings.TrimSpace(v)
		}
		headers[lname] = strings.Join(trimmed, ",")
	}
	host := req.Host
	if host == "" {
		host = req.URL.Host
	}
	headers["host"] = host

	names := make([]string, 0, len(headers))
	for n := range headers {
		names = append(names, n)
	}
	sort.Strings(names)

	var canonical strings.Builder
	for _, n := range names {
		canonical.WriteString(n)
		canonical.WriteByte(':')
		canonical.WriteString(headers[n])
		canonical.WriteByte('\n')
	}
	return canonical.String(), strings.Join(names, ";")
}
