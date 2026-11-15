package remote

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"os"
	"strings"
	"testing"
	"time"
)

// pemPair is a self-signed certificate and key for host, valid until
// notAfter, as one PEM stream the way `tailscale cert` prints it.
func pemPair(t *testing.T, host string, notAfter time.Time) []byte {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: host},
		DNSNames:     []string{host},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     notAfter,
	}

	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}

	kder, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer

	_ = pem.Encode(&out, &pem.Block{Type: "CERTIFICATE", Bytes: der})
	_ = pem.Encode(&out, &pem.Block{Type: "EC PRIVATE KEY", Bytes: kder})

	return out.Bytes()
}

// fakeTailscale answers status and cert the way the CLI does, counting
// the cert calls.
type fakeTailscale struct {
	pair  []byte
	fail  error
	certs int
}

func (f *fakeTailscale) run(_ context.Context, _ string, args ...string) ([]byte, error) {
	switch args[0] {
	case "status":
		return []byte(`{"Self":{"DNSName":"box.tail.ts.net."}}`), nil
	case "cert":
		f.certs++

		if strings.Join(args[1:5], " ") != "--cert-file - --key-file -" {
			return nil, errors.New("the pair must come on stdout")
		}

		return f.pair, f.fail
	}

	return nil, errors.New("unexpected " + args[0])
}

// The pair comes from stdout into the files f10 owns, is served, and is not
// fetched again while it is fresh.
func TestCertsFetchFromStdout(t *testing.T) {
	t.Parallel()

	ts := &fakeTailscale{pair: pemPair(t, "box.tail.ts.net", time.Now().Add(90*24*time.Hour))}
	c := &Certs{Dir: t.TempDir(), Run: ts.run, CLI: func() string { return "tailscale" }, Wait: true}

	cfg, host, why := c.Config()
	if cfg == nil || host != "box.tail.ts.net" || why != "" {
		t.Fatalf("config = %v, %q, %q; want the fetched certificate", cfg, host, why)
	}

	if fi, err := os.Stat(c.keyPath()); err != nil || fi.Mode().Perm() != 0o600 {
		t.Errorf("key file = %v, %v; want 0600", fi, err)
	}

	if _, _, _ = c.Config(); ts.certs != 1 {
		t.Errorf("fetched %d times, want once while the certificate is fresh", ts.certs)
	}
}

// A certificate inside the renewal window is fetched again, and served
// until the new one lands.
func TestCertsRenewNearExpiry(t *testing.T) {
	t.Parallel()

	ts := &fakeTailscale{pair: pemPair(t, "box.tail.ts.net", time.Now().Add(10*24*time.Hour))}
	c := &Certs{Dir: t.TempDir(), Run: ts.run, CLI: func() string { return "tailscale" }, Wait: true}

	if cfg, _, _ := c.Config(); cfg == nil {
		t.Fatal("a valid certificate near expiry is not served")
	}

	// an hour on, the retry window has passed
	c.Now = func() time.Time { return time.Now().Add(2 * time.Hour) }

	ts.pair = pemPair(t, "box.tail.ts.net", time.Now().Add(90*24*time.Hour))

	// the new file needs a different mtime than the first
	time.Sleep(20 * time.Millisecond)

	if _, _, _ = c.Config(); ts.certs != 2 {
		t.Errorf("fetched %d times, want a renewal", ts.certs)
	}
}

// A tailnet with HTTPS off leaves plain HTTP with the reason, and is not
// asked again within the hour.
func TestCertsReportAFailedFetch(t *testing.T) {
	t.Parallel()

	ts := &fakeTailscale{fail: errors.New("your tailnet does not have HTTPS enabled")}
	c := &Certs{Dir: t.TempDir(), Run: ts.run, CLI: func() string { return "tailscale" }, Wait: true}

	cfg, _, why := c.Config()
	if cfg != nil || !strings.Contains(why, "HTTPS certificates enabled") {
		t.Fatalf("config = %v, %q; want none, naming the admin console", cfg, why)
	}

	if _, _, _ = c.Config(); ts.certs != 1 {
		t.Errorf("asked %d times within the hour, want once", ts.certs)
	}

	none := &Certs{Dir: t.TempDir(), CLI: func() string { return "" }, Wait: true}
	if _, _, why := none.Config(); !strings.Contains(why, "no tailscale CLI") {
		t.Errorf("no CLI = %q", why)
	}
}
