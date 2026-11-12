package api

import (
	"crypto/tls"
	"crypto/x509"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// requestWithCertificate builds a request as it arrives after a TLS
// handshake that presented a certificate for dnsName. verified says whether
// the handshake verified it against the client CAs.
func requestWithCertificate(method, path, dnsName string, verified bool) *http.Request {
	request := httptest.NewRequest(method, path, nil)
	certificate := &x509.Certificate{DNSNames: []string{dnsName}}

	request.TLS = &tls.ConnectionState{PeerCertificates: []*x509.Certificate{certificate}}
	if verified {
		request.TLS.VerifiedChains = [][]*x509.Certificate{{certificate}}
	}

	return request
}

// requestOverTLSWithoutCertificate builds a request that arrived over TLS
// from a client that presented no certificate.
func requestOverTLSWithoutCertificate(method, path string) *http.Request {
	request := httptest.NewRequest(method, path, nil)
	request.TLS = &tls.ConnectionState{}

	return request
}

func TestGuardAcceptsVerifiedClientCertificates(test *testing.T) {
	cases := []struct {
		name       string
		request    *http.Request
		wantStatus int
		wantReason string
	}{
		{
			name:       "known certificate with permission",
			request:    requestWithCertificate(http.MethodGet, servicesPath, "orders.internal", true),
			wantStatus: http.StatusOK,
		},
		{
			name:       "known certificate registers",
			request:    requestWithCertificate(http.MethodPost, servicesPath, "orders.internal", true),
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "known certificate without permission",
			request:    requestWithCertificate(http.MethodPost, servicesPath, "reader.internal", true),
			wantStatus: http.StatusForbidden,
			wantReason: AuthFailureForbidden,
		},
		{
			name:       "trusted certificate nobody listed",
			request:    requestWithCertificate(http.MethodGet, servicesPath, "billing.internal", true),
			wantStatus: http.StatusUnauthorized,
			wantReason: AuthFailureInvalid,
		},
		{
			name:       "certificate the handshake did not verify",
			request:    requestWithCertificate(http.MethodGet, servicesPath, "orders.internal", false),
			wantStatus: http.StatusUnauthorized,
			wantReason: AuthFailureMissing,
		},
		{
			name:       "TLS without any certificate",
			request:    requestOverTLSWithoutCertificate(http.MethodGet, servicesPath),
			wantStatus: http.StatusUnauthorized,
			wantReason: AuthFailureMissing,
		},
	}

	for _, testCase := range cases {
		test.Run(testCase.name, func(test *testing.T) {
			router, reasons, _ := guardedRouter(test)

			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, testCase.request)

			if recorder.Code != testCase.wantStatus {
				test.Fatalf("expected status %d, got %d: %s", testCase.wantStatus, recorder.Code, recorder.Body.String())
			}

			if got := strings.Join(*reasons, ","); got != testCase.wantReason {
				test.Errorf("expected failure reason %q, got %q", testCase.wantReason, got)
			}
		})
	}
}

func TestGuardJudgesByTokenWhenHeaderIsPresent(test *testing.T) {
	cases := []struct {
		name          string
		dnsName       string
		authorization string
		wantStatus    int
	}{
		// The certificate alone would be allowed to register, but the
		// header decides, and the reader token may not.
		{name: "weaker token beats stronger certificate", dnsName: "orders.internal", authorization: "Bearer reader-token", wantStatus: http.StatusForbidden},
		{name: "bad token is not rescued by a good certificate", dnsName: "orders.internal", authorization: "Bearer nope", wantStatus: http.StatusUnauthorized},
		{name: "stronger token beats weaker certificate", dnsName: "reader.internal", authorization: "Bearer admin-token", wantStatus: http.StatusBadRequest},
	}

	for _, testCase := range cases {
		test.Run(testCase.name, func(test *testing.T) {
			router, _, _ := guardedRouter(test)

			request := requestWithCertificate(http.MethodPost, servicesPath, testCase.dnsName, true)
			request.Header.Set("Authorization", testCase.authorization)

			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, request)

			if recorder.Code != testCase.wantStatus {
				test.Errorf("expected status %d, got %d: %s", testCase.wantStatus, recorder.Code, recorder.Body.String())
			}
		})
	}
}

func TestGuardLogsNamesOfUnlistedCertificates(test *testing.T) {
	router, _, logs := guardedRouter(test)

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, requestWithCertificate(http.MethodGet, servicesPath, "billing.internal", true))

	if !strings.Contains(logs.String(), "billing.internal") {
		test.Errorf("expected the certificate's names in the log, logs:\n%s", logs.String())
	}

	if detail := decodeError(test, recorder); !strings.Contains(detail.Message, "not listed") {
		test.Errorf("expected the message to say the certificate is not listed, got %q", detail.Message)
	}
}
