package trustroots

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestNewLoadsEmbeddedBundleAndMaterializesMergedFile(t *testing.T) {
	stateDir := t.TempDir()

	bundle, err := New(Config{StateDir: stateDir})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if bundle.CertPool() == nil {
		t.Fatalf("CertPool() returned nil")
	}
	if bundle.BundlePath() != filepath.Join(stateDir, "tls", "ca-bundle.pem") {
		t.Fatalf("BundlePath() = %q", bundle.BundlePath())
	}
	body, err := os.ReadFile(bundle.BundlePath())
	if err != nil {
		t.Fatalf("ReadFile(%s) error = %v", bundle.BundlePath(), err)
	}
	if len(body) == 0 {
		t.Fatalf("materialized bundle is empty")
	}
	env := bundle.Environment()
	if env[nodeExtraCACertsEnvKey] != bundle.BundlePath() {
		t.Fatalf("NODE_EXTRA_CA_CERTS = %q", env[nodeExtraCACertsEnvKey])
	}
	if env[npmConfigCAFileEnvKey] != bundle.BundlePath() {
		t.Fatalf("NPM_CONFIG_CAFILE = %q", env[npmConfigCAFileEnvKey])
	}
}

func TestNewAppendsExtraCACertsFile(t *testing.T) {
	stateDir := t.TempDir()
	extraPEM, _, _ := trustRootsTestCA(t, "extra-root")
	extraPath := filepath.Join(t.TempDir(), "extra.pem")
	if err := os.WriteFile(extraPath, extraPEM, 0o644); err != nil {
		t.Fatalf("WriteFile(extra.pem) error = %v", err)
	}

	bundle, err := New(Config{
		StateDir:         stateDir,
		ExtraCACertsFile: extraPath,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	merged := string(bundle.BundlePEM())
	if !strings.Contains(merged, string(strings.TrimSpace(string(extraPEM)))) {
		t.Fatalf("merged bundle does not contain extra PEM")
	}
}

func TestNewRejectsInvalidExtraCACertsFile(t *testing.T) {
	stateDir := t.TempDir()
	extraPath := filepath.Join(t.TempDir(), "bad.pem")
	if err := os.WriteFile(extraPath, []byte("not pem\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(bad.pem) error = %v", err)
	}

	_, err := New(Config{
		StateDir:         stateDir,
		ExtraCACertsFile: extraPath,
	})
	if err == nil || !strings.Contains(err.Error(), "invalid PEM") {
		t.Fatalf("New() error = %v", err)
	}
}

func TestHTTPClientTrustsConfiguredExtraRoot(t *testing.T) {
	stateDir := t.TempDir()
	rootPEM, rootCert, rootKey := trustRootsTestCA(t, "trusted-root")
	serverTLS := trustRootsTestServerTLSConfig(t, rootCert, rootKey)
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	server.TLS = serverTLS
	server.StartTLS()
	defer server.Close()

	extraPath := filepath.Join(t.TempDir(), "extra.pem")
	if err := os.WriteFile(extraPath, rootPEM, 0o644); err != nil {
		t.Fatalf("WriteFile(extra.pem) error = %v", err)
	}
	bundle, err := New(Config{
		StateDir:         stateDir,
		ExtraCACertsFile: extraPath,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	resp, err := bundle.HTTPClient(5 * time.Second).Get(server.URL)
	if err != nil {
		t.Fatalf("HTTPClient().Get() error = %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
}

func TestHTTPClientRejectsUnknownRoot(t *testing.T) {
	stateDir := t.TempDir()
	_, rootCert, rootKey := trustRootsTestCA(t, "untrusted-root")
	serverTLS := trustRootsTestServerTLSConfig(t, rootCert, rootKey)
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	server.TLS = serverTLS
	server.StartTLS()
	defer server.Close()

	bundle, err := New(Config{StateDir: stateDir})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	_, err = bundle.HTTPClient(5 * time.Second).Get(server.URL)
	if err == nil {
		t.Fatalf("HTTPClient().Get() expected error")
	}
}

func trustRootsTestCA(t *testing.T, commonName string) ([]byte, *x509.Certificate, *rsa.PrivateKey) {
	t.Helper()

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("GenerateKey() error = %v", err)
	}
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: commonName},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLen:            1,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("CreateCertificate(root) error = %v", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("ParseCertificate(root) error = %v", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), cert, key
}

func trustRootsTestServerTLSConfig(t *testing.T, rootCert *x509.Certificate, rootKey *rsa.PrivateKey) *tls.Config {
	t.Helper()

	serverKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("GenerateKey(server) error = %v", err)
	}
	serverTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: "127.0.0.1"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
	}
	serverDER, err := x509.CreateCertificate(rand.Reader, serverTemplate, rootCert, &serverKey.PublicKey, rootKey)
	if err != nil {
		t.Fatalf("CreateCertificate(server) error = %v", err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: serverDER})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(serverKey)})
	certPair, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		t.Fatalf("X509KeyPair() error = %v", err)
	}
	return &tls.Config{Certificates: []tls.Certificate{certPair}}
}
