package publication

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type certificates struct {
	mu        sync.Mutex
	ca        *x509.Certificate
	key       *ecdsa.PrivateKey
	publicPEM string
	external  *tls.Certificate
	cache     map[string]*tls.Certificate
}
type authorityFile struct {
	Certificate []byte `json:"certificate"`
	Key         []byte `json:"key"`
}

func serial() *big.Int {
	n, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		panic(err)
	}
	return n
}
func newCertificates(dir, mode, certFile, keyFile string) (*certificates, error) {
	c := &certificates{cache: map[string]*tls.Certificate{}}
	if mode == "external" {
		if certFile == "" || keyFile == "" {
			return nil, errors.New("set REMENTOR_PUBLISH_CERT_FILE and REMENTOR_PUBLISH_KEY_FILE on this computer")
		}
		cert, err := tls.LoadX509KeyPair(certFile, keyFile)
		if err != nil {
			return nil, fmt.Errorf("load installed HTTPS certificate: %w", err)
		}
		c.external = &cert
		return c, nil
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, "authority.json")
	info, err := os.Lstat(path)
	if err == nil && (!info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0) {
		return nil, errors.New("publishing CA must be a private regular file (mode 0600)")
	}
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			return nil, err
		}
		template := &x509.Certificate{SerialNumber: serial(), Subject: pkix.Name{CommonName: "Rementor tailnet publishing CA"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().AddDate(5, 0, 0), KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageCRLSign, BasicConstraintsValid: true, IsCA: true, MaxPathLenZero: true}
		der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
		if err != nil {
			return nil, err
		}
		keyDER, err := x509.MarshalPKCS8PrivateKey(key)
		if err != nil {
			return nil, err
		}
		raw, err = json.Marshal(authorityFile{Certificate: der, Key: keyDER})
		if err != nil {
			return nil, err
		}
		// A single exclusive write keeps the certificate/key pair together and
		// never replaces an existing trust root or follows a preexisting symlink.
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			return nil, err
		}
		_, writeErr := f.Write(raw)
		syncErr := f.Sync()
		closeErr := f.Close()
		if err := errors.Join(writeErr, syncErr, closeErr); err != nil {
			return nil, err
		}
	} else if err != nil {
		return nil, err
	}
	var stored authorityFile
	if err := json.Unmarshal(raw, &stored); err != nil {
		return nil, err
	}
	ca, err := x509.ParseCertificate(stored.Certificate)
	if err != nil {
		return nil, err
	}
	key, err := x509.ParsePKCS8PrivateKey(stored.Key)
	if err != nil {
		return nil, err
	}
	private, ok := key.(*ecdsa.PrivateKey)
	if !ok {
		return nil, errors.New("invalid publishing CA key")
	}
	if !ca.IsCA || !ca.BasicConstraintsValid || time.Now().After(ca.NotAfter) || time.Now().Before(ca.NotBefore) || !private.PublicKey.Equal(ca.PublicKey) {
		return nil, errors.New("publishing CA is invalid or expired")
	}
	c.ca, c.key = ca, private
	c.publicPEM = string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: ca.Raw}))
	return c, nil
}
func (c *certificates) get(host string) (*tls.Certificate, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.external != nil {
		leaf, err := x509.ParseCertificate(c.external.Certificate[0])
		if err != nil {
			return nil, err
		}
		if err := leaf.VerifyHostname(host); err != nil {
			return nil, err
		}
		if time.Now().Before(leaf.NotBefore) || time.Now().After(leaf.NotAfter) {
			return nil, errors.New("installed HTTPS certificate expired or not yet valid")
		}
		return c.external, nil
	}
	if cert := c.cache[host]; cert != nil && cert.Leaf.NotAfter.After(time.Now().Add(time.Hour)) {
		return cert, nil
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	expiration := time.Now().Add(24 * time.Hour)
	if c.ca.NotAfter.Before(expiration) {
		expiration = c.ca.NotAfter
	}
	if !expiration.After(time.Now()) {
		return nil, errors.New("publishing CA expired")
	}
	template := &x509.Certificate{SerialNumber: serial(), Subject: pkix.Name{CommonName: host}, DNSNames: []string{host}, NotBefore: time.Now().Add(-5 * time.Minute), NotAfter: expiration, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, template, c.ca, &key.PublicKey, c.key)
	if err != nil {
		return nil, err
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, err
	}
	cert := &tls.Certificate{Certificate: [][]byte{der, c.ca.Raw}, PrivateKey: key, Leaf: leaf}
	if len(c.cache) >= 4096 {
		c.cache = map[string]*tls.Certificate{}
	}
	c.cache[host] = cert
	return cert, nil
}
func (m *Manager) tlsConfig() *tls.Config {
	certs := m.certificates
	return &tls.Config{MinVersion: tls.VersionTLS12, GetCertificate: func(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
		m.mu.RLock()
		enabled := m.state.Configuration.Enabled
		found := false
		for _, s := range m.state.Scopes {
			for _, l := range s.Links {
				if strings.TrimPrefix(l.URL, "https://") == hello.ServerName {
					found = true
				}
			}
		}
		m.mu.RUnlock()
		if !enabled || !found {
			return nil, errors.New("hostname is not published")
		}
		return certs.get(hello.ServerName)
	}}
}
func CertificateFingerprint(pemText string) string {
	block, _ := pem.Decode([]byte(pemText))
	if block == nil {
		return ""
	}
	sum := sha256.Sum256(block.Bytes)
	return fmt.Sprintf("%x", sum)
}
