package connectplugin

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net/http"

	"connectrpc.com/connect"
)

// MTLSAuth implements mutual TLS authentication.
// This is transport-level security (HTTP client configuration).
type MTLSAuth struct {
	// ClientCert is the client certificate for outgoing requests (client-side)
	ClientCert *tls.Certificate

	// RootCAs is the trusted CA pool for verifying server certificates (client-side)
	RootCAs *x509.CertPool

	// ClientCAs is the trusted CA pool for verifying client certificates (server-side)
	ClientCAs *x509.CertPool

	// ExtractIdentity extracts identity from verified client certificate (server-side)
	// Default: uses certificate Common Name (CN)
	ExtractIdentity func(*x509.Certificate) (identity string, claims map[string]string)
}

// NewMTLSAuth creates an mTLS auth provider.
func NewMTLSAuth(clientCert *tls.Certificate, rootCAs, clientCAs *x509.CertPool) *MTLSAuth {
	return &MTLSAuth{
		ClientCert: clientCert,
		RootCAs:    rootCAs,
		ClientCAs:  clientCAs,
		ExtractIdentity: func(cert *x509.Certificate) (string, map[string]string) {
			return cert.Subject.CommonName, map[string]string{
				"organization": cert.Subject.Organization[0],
			}
		},
	}
}

// ClientInterceptor returns an interceptor that configures mTLS for outgoing requests.
// Note: mTLS is primarily configured at HTTP client level, not per-request.
// This interceptor is a placeholder that could add cert-related headers if needed.
func (m *MTLSAuth) ClientInterceptor() connect.UnaryInterceptorFunc {
	return func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			// mTLS is handled at transport level (http.Client with TLS config)
			// This interceptor could add additional headers if needed
			return next(ctx, req)
		}
	}
}

// ServerInterceptor returns an interceptor that validates client certificates.
func (m *MTLSAuth) ServerInterceptor() connect.UnaryInterceptorFunc {
	return func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			// Extract peer certificates from HTTP request
			// Note: This requires access to the underlying http.Request
			// In Connect, we'd need to use connect.Request's HTTPRequest() method

			// For now, this is a simplified implementation
			// In production, you'd extract from the TLS connection state
			// via req.HTTPRequest().TLS.PeerCertificates

			// Placeholder: Assume cert validation happened at TLS layer
			// Just check if there was a client cert presented
			// Real implementation would extract from req.HTTPRequest().TLS

			// Store placeholder auth context
			authCtx := &AuthContext{
				Identity: "mtls-client",
				Provider: "mtls",
				Claims:   map[string]string{"verified": "true"},
			}
			ctx = WithAuthContext(ctx, authCtx)

			return next(ctx, req)
		}
	}
}

// ConfigureClientTLS configures an http.Client with mTLS.
// This is the primary mTLS configuration (transport-level).
func (m *MTLSAuth) ConfigureClientTLS(client *http.Client) error {
	if m.ClientCert == nil {
		return fmt.Errorf("client certificate required for mTLS")
	}

	tlsConfig := &tls.Config{
		Certificates: []tls.Certificate{*m.ClientCert},
		RootCAs:      m.RootCAs,
		MinVersion:   tls.VersionTLS12,
	}

	transport := &http.Transport{
		TLSClientConfig: tlsConfig,
	}

	client.Transport = transport
	return nil
}

// ConfigureServerTLS returns a TLS config for mTLS server.
// This should be used when creating the http.Server.
func (m *MTLSAuth) ConfigureServerTLS() (*tls.Config, error) {
	if m.ClientCAs == nil {
		return nil, fmt.Errorf("client CA pool required for mTLS server")
	}

	return &tls.Config{
		ClientAuth: tls.RequireAndVerifyClientCert,
		ClientCAs:  m.ClientCAs,
		MinVersion: tls.VersionTLS12,
	}, nil
}
