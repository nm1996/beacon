package auth

import (
	"crypto/x509"
	"net/url"
	"strings"
	"testing"
)

// certificateFor returns a certificate carrying the given DNS names and
// URIs. AuthenticateCertificate only reads those, so it need not be signed.
func certificateFor(test *testing.T, dnsNames []string, uris ...string) *x509.Certificate {
	test.Helper()

	certificate := &x509.Certificate{DNSNames: dnsNames}

	for _, raw := range uris {
		parsed, err := url.Parse(raw)
		if err != nil {
			test.Fatalf("parse %q: %v", raw, err)
		}

		certificate.URIs = append(certificate.URIs, parsed)
	}

	return certificate
}

func TestAuthenticateCertificateMatchesSubjectAlternativeNames(test *testing.T) {
	path := writeTokens(test, `
[[certificates]]
name = "orders"
dns = "Orders.Internal"
permissions = ["register", "discover"]

[[certificates]]
name = "payments"
uri = "spiffe://example.org/payments"
permissions = ["discover"]

[[certificates]]
name = "fallback"
dns = "shared.internal"
`)

	identities, err := Load(path)
	if err != nil {
		test.Fatalf("load: %v", err)
	}

	if !identities.HasCertificates() {
		test.Error("expected HasCertificates to report the entries")
	}

	cases := []struct {
		name        string
		certificate *x509.Certificate
		wantName    string
	}{
		{name: "DNS name, any case", certificate: certificateFor(test, []string{"orders.INTERNAL"}), wantName: "orders"},
		{name: "SPIFFE ID", certificate: certificateFor(test, nil, "spiffe://example.org/payments"), wantName: "payments"},
		{name: "one of several names", certificate: certificateFor(test, []string{"unknown.internal", "shared.internal"}), wantName: "fallback"},
		{name: "first entry in file order wins", certificate: certificateFor(test, []string{"shared.internal", "orders.internal"}), wantName: "orders"},
		{name: "unknown DNS name", certificate: certificateFor(test, []string{"billing.internal"})},
		{name: "URI with another path", certificate: certificateFor(test, nil, "spiffe://example.org/payments/admin")},
		{name: "DNS name given as URI", certificate: certificateFor(test, nil, "https://orders.internal")},
		{name: "no names at all", certificate: certificateFor(test, nil)},
	}

	for _, testCase := range cases {
		test.Run(testCase.name, func(test *testing.T) {
			identity, ok := identities.AuthenticateCertificate(testCase.certificate)

			if ok != (testCase.wantName != "") || identity.Name != testCase.wantName {
				test.Errorf("expected %q, got %q (accepted %t)", testCase.wantName, identity.Name, ok)
			}
		})
	}

	payments, _ := identities.AuthenticateCertificate(certificateFor(test, nil, "spiffe://example.org/payments"))
	if !payments.Allows(PermissionDiscover) || payments.Allows(PermissionRegister) {
		test.Errorf("expected payments to hold discover only, got %+v", payments)
	}

	fallback, _ := identities.AuthenticateCertificate(certificateFor(test, []string{"shared.internal"}))
	if !fallback.Allows(PermissionMetrics) {
		test.Error("expected an entry without a permission list to hold every permission")
	}
}

func TestTokensAndCertificatesLiveSideBySide(test *testing.T) {
	path := writeTokens(test, "[[tokens]]\nname = \"orders-token\"\nsha256 = \""+mustHash(test, ordersToken)+"\"\n"+
		"[[certificates]]\nname = \"orders-cert\"\ndns = \"orders.internal\"\n")

	identities, err := Load(path)
	if err != nil {
		test.Fatalf("load: %v", err)
	}

	if identity, ok := identities.Authenticate(ordersToken); !ok || identity.Name != "orders-token" {
		test.Errorf("expected the token to be accepted, got %q, %t", identity.Name, ok)
	}

	if identity, ok := identities.AuthenticateCertificate(certificateFor(test, []string{"orders.internal"})); !ok || identity.Name != "orders-cert" {
		test.Errorf("expected the certificate to be accepted, got %q, %t", identity.Name, ok)
	}
}

func TestTokensOnlyHasNoCertificates(test *testing.T) {
	identities, err := Load(writeTokens(test, "[[tokens]]\nname = \"a\"\nsha256 = \""+mustHash(test, ordersToken)+"\"\n"))
	if err != nil {
		test.Fatalf("load: %v", err)
	}

	if identities.HasCertificates() {
		test.Error("expected no certificates")
	}

	if _, ok := identities.AuthenticateCertificate(certificateFor(test, []string{"orders.internal"})); ok {
		test.Error("expected no certificate to be accepted")
	}
}

func TestLoadCertificateFailures(test *testing.T) {
	hash := mustHash(test, ordersToken)

	cases := []struct {
		name    string
		content string
		wantErr string
	}{
		{name: "neither dns nor uri", content: "[[certificates]]\nname = \"a\"", wantErr: `certificate "a": set exactly one of dns and uri`},
		{name: "both dns and uri", content: "[[certificates]]\nname = \"a\"\ndns = \"a.internal\"\nuri = \"spiffe://example.org/a\"", wantErr: "set exactly one of dns and uri"},
		{name: "missing name", content: "[[certificates]]\ndns = \"a.internal\"", wantErr: "certificates[0]: name is required"},
		{name: "wildcard DNS", content: "[[certificates]]\nname = \"a\"\ndns = \"*.internal\"", wantErr: "wildcards are not matched"},
		{name: "relative URI", content: "[[certificates]]\nname = \"a\"\nuri = \"orders\"", wantErr: "must be an absolute URI"},
		{name: "unknown key", content: "[[certificates]]\nname = \"a\"\ncommon_name = \"a\"", wantErr: "certificates.common_name"},
		{
			name:    "same DNS name twice, any case",
			content: "[[certificates]]\nname = \"a\"\ndns = \"a.internal\"\n[[certificates]]\nname = \"b\"\ndns = \"A.Internal\"",
			wantErr: `certificate "b": matches the same certificate name as another entry`,
		},
		{
			name:    "name shared with a token",
			content: "[[tokens]]\nname = \"orders\"\nsha256 = \"" + hash + "\"\n[[certificates]]\nname = \"orders\"\ndns = \"orders.internal\"",
			wantErr: `certificate "orders": name is used more than once`,
		},
		{
			name:    "unknown permission",
			content: "[[certificates]]\nname = \"a\"\ndns = \"a.internal\"\npermissions = [\"root\"]",
			wantErr: `certificate "a": unknown permission "root"`,
		},
	}

	for _, testCase := range cases {
		test.Run(testCase.name, func(test *testing.T) {
			_, err := Load(writeTokens(test, testCase.content))

			if err == nil || !strings.Contains(err.Error(), testCase.wantErr) {
				test.Errorf("expected an error containing %q, got %v", testCase.wantErr, err)
			}
		})
	}
}
