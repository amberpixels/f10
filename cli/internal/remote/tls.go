package remote

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// renewBefore is how long before expiry a certificate is renewed; Tailscale
// issues 90-day certificates.
const renewBefore = 30 * 24 * time.Hour

// retryAfter spaces failed fetches, so a tailnet with HTTPS off is not asked
// every tick.
const retryAfter = time.Hour

// fetchTimeout bounds one fetch: issuing a certificate can take a minute.
const fetchTimeout = 3 * time.Minute

// macCLI is the CLI inside the macOS app, which puts nothing on PATH.
const macCLI = "/Applications/Tailscale.app/Contents/MacOS/Tailscale"

// Runner runs a command and returns its stdout.
type Runner func(ctx context.Context, name string, args ...string) ([]byte, error)

// Certs is the machine's Tailscale HTTPS certificate, fetched with the
// tailscale CLI into Dir and renewed in the background before it expires.
type Certs struct {
	Dir  string
	Run  Runner           // exec by default
	CLI  func() string    // the tailscale CLI's path, "" for none
	Now  func() time.Time // time.Now by default
	Wait bool             // fetch in the caller, for tests

	mu       sync.Mutex
	cert     *tls.Certificate
	mtime    time.Time
	fetching bool
	tried    time.Time
	why      string
}

// Config is the TLS config to serve with and the host its certificate
// names. With no usable certificate the config is nil and why says what is
// missing; a fetch then runs in the background, at most once an hour.
func (c *Certs) Config() (*tls.Config, string, string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.load()

	fresh := c.cert != nil && c.cert.Leaf.NotAfter.Sub(c.now()) > renewBefore
	if !fresh && !c.fetching && c.now().Sub(c.tried) >= retryAfter {
		c.fetching, c.tried = true, c.now()

		if c.Wait {
			c.mu.Unlock()
			c.fetch()
			c.mu.Lock()
			c.load()
		} else {
			go c.fetch()
		}
	}

	if c.cert == nil || !c.cert.Leaf.NotAfter.After(c.now()) {
		why := c.why
		if why == "" {
			why = "fetching the machine's Tailscale certificate"
		}

		return nil, "", why
	}

	host := ""
	if len(c.cert.Leaf.DNSNames) > 0 {
		host = c.cert.Leaf.DNSNames[0]
	}

	return &tls.Config{MinVersion: tls.VersionTLS12, GetCertificate: c.get}, host, ""
}

func (c *Certs) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}

	return time.Now()
}

func (c *Certs) certPath() string { return filepath.Join(c.Dir, "cert.pem") }
func (c *Certs) keyPath() string  { return filepath.Join(c.Dir, "key.pem") }

// get serves the newest certificate, so a renewal applies without a rebind.
func (c *Certs) get(*tls.ClientHelloInfo) (*tls.Certificate, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.cert == nil {
		return nil, errors.New("no certificate")
	}

	return c.cert, nil
}

// load reads the pair from Dir when the certificate file changed; the
// caller holds mu.
func (c *Certs) load() {
	fi, err := os.Stat(c.certPath())
	if err != nil || fi.ModTime().Equal(c.mtime) {
		return
	}

	pair, err := tls.LoadX509KeyPair(c.certPath(), c.keyPath())
	if err == nil && pair.Leaf == nil && len(pair.Certificate) > 0 {
		pair.Leaf, err = x509.ParseCertificate(pair.Certificate[0])
	}

	if err != nil {
		c.why = "the stored certificate does not load: " + err.Error()

		return
	}

	c.cert, c.mtime = &pair, fi.ModTime()
}

// fetch asks Tailscale for the machine's certificate. The pair comes back
// on stdout, because the macOS App Store build's sandboxed CLI cannot write
// files outside its own container.
func (c *Certs) fetch() {
	why := c.fetchPair()

	c.mu.Lock()
	defer c.mu.Unlock()

	c.fetching, c.why = false, why
}

func (c *Certs) fetchPair() string {
	find := c.CLI
	if find == nil {
		find = findCLI
	}

	cli := find()

	if cli == "" {
		return "no tailscale CLI on PATH or in /Applications/Tailscale.app"
	}

	run := c.Run
	if run == nil {
		run = execRun
	}

	ctx, cancel := context.WithTimeout(context.Background(), fetchTimeout)
	defer cancel()

	host, err := dnsName(ctx, run, cli)
	if err != nil {
		return err.Error()
	}

	out, err := run(ctx, cli, "cert", "--cert-file", "-", "--key-file", "-", host)
	if err != nil {
		return fmt.Sprintf(
			"tailscale cert %s failed (are HTTPS certificates enabled in the tailnet's admin console?): %v",
			host,
			err,
		)
	}

	certPEM, keyPEM := splitPEM(out)
	if _, err := tls.X509KeyPair(certPEM, keyPEM); err != nil {
		return "tailscale cert printed no usable pair: " + err.Error()
	}

	if err := writePrivate(c.certPath(), certPEM); err != nil {
		return err.Error()
	}

	if err := writePrivate(c.keyPath(), keyPEM); err != nil {
		return err.Error()
	}

	return ""
}

// dnsName is the machine's MagicDNS name, from `tailscale status --json`.
func dnsName(ctx context.Context, run Runner, cli string) (string, error) {
	out, err := run(ctx, cli, "status", "--json")
	if err != nil {
		return "", fmt.Errorf("tailscale status: %w", err)
	}

	var st struct {
		Self struct {
			DNSName string `json:"DNSName"`
		} `json:"Self"`
	}

	if err := json.Unmarshal(out, &st); err != nil {
		return "", fmt.Errorf("tailscale status --json: %w", err)
	}

	host := strings.TrimSuffix(st.Self.DNSName, ".")
	if host == "" {
		return "", errors.New("tailscale reports no MagicDNS name for this machine: is MagicDNS on?")
	}

	return host, nil
}

// splitPEM separates the certificate chain from the private key in one
// stream of PEM blocks.
func splitPEM(raw []byte) ([]byte, []byte) {
	var certs, key bytes.Buffer

	for {
		var b *pem.Block

		b, raw = pem.Decode(raw)
		if b == nil {
			break
		}

		if strings.Contains(b.Type, "PRIVATE KEY") {
			_ = pem.Encode(&key, b)
		} else if b.Type == "CERTIFICATE" {
			_ = pem.Encode(&certs, b)
		}
	}

	return certs.Bytes(), key.Bytes()
}

func writePrivate(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}

	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}

	return os.Rename(tmp, path)
}

func findCLI() string {
	if p, err := exec.LookPath("tailscale"); err == nil {
		return p
	}

	if _, err := os.Stat(macCLI); err == nil {
		return macCLI
	}

	return ""
}

func execRun(ctx context.Context, name string, args ...string) ([]byte, error) {
	var stderr bytes.Buffer

	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stderr = &stderr

	out, err := cmd.Output()
	if err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return nil, fmt.Errorf("%w: %s", err, msg)
		}

		return nil, err
	}

	return out, nil
}
