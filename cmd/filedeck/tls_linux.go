package main

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"io"
	"math/big"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/sys/unix"
)

const selfSignedFile = "tls-self-signed.pem"

// selfSigned loads or creates a certificate for the origin host, stored in the
// private state directory so browsers see the same certificate after restarts.
func selfSigned(state, origin string) (tls.Certificate, string, error) {
	u, err := url.Parse(origin)
	if err != nil || u.Scheme != "https" {
		return tls.Certificate{}, "", errors.New("self-signed TLS requires an https origin")
	}
	host := u.Hostname()
	file := filepath.Join(state, selfSignedFile)
	if cert, ok := loadSelfSigned(file, host); ok {
		return cert, fingerprint(cert), nil
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, "", err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 127))
	if err != nil {
		return tls.Certificate{}, "", err
	}
	now := time.Now()
	template := x509.Certificate{
		SerialNumber: serial, Subject: pkix.Name{CommonName: host, Organization: []string{"Filedeck self-signed"}},
		NotBefore: now.Add(-time.Hour), NotAfter: now.Add(397 * 24 * time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}
	if ip := net.ParseIP(host); ip != nil {
		template.IPAddresses = []net.IP{ip}
	} else {
		template.DNSNames = []string{host}
	}
	der, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if err != nil {
		return tls.Certificate{}, "", err
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return tls.Certificate{}, "", err
	}
	var data bytes.Buffer
	pem.Encode(&data, &pem.Block{Type: "CERTIFICATE", Bytes: der})
	pem.Encode(&data, &pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
	if err = writePrivate(state, selfSignedFile, data.Bytes()); err != nil {
		return tls.Certificate{}, "", err
	}
	cert, err := tls.X509KeyPair(data.Bytes(), data.Bytes())
	if err != nil {
		return tls.Certificate{}, "", err
	}
	return cert, fingerprint(cert), nil
}

func loadSelfSigned(file, host string) (tls.Certificate, bool) {
	f, err := os.OpenFile(file, os.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if err != nil {
		return tls.Certificate{}, false
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() || st.Mode().Perm() != 0600 || st.Size() > 64<<10 {
		return tls.Certificate{}, false
	}
	data, err := io.ReadAll(io.LimitReader(f, 64<<10))
	if err != nil {
		return tls.Certificate{}, false
	}
	cert, err := tls.X509KeyPair(data, data)
	if err != nil {
		return tls.Certificate{}, false
	}
	leaf, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil || time.Now().Add(30*24*time.Hour).After(leaf.NotAfter) || leaf.VerifyHostname(host) != nil {
		return tls.Certificate{}, false
	}
	return cert, true
}

// writePrivate atomically replaces name inside dir with a 0600 file.
func writePrivate(dir, name string, data []byte) (err error) {
	tmp := filepath.Join(dir, "."+name+".tmp")
	_ = os.Remove(tmp)
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL|unix.O_NOFOLLOW, 0600)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			os.Remove(tmp)
		}
	}()
	if _, err = f.Write(data); err == nil {
		err = f.Sync()
	}
	if err = errors.Join(err, f.Close()); err != nil {
		return err
	}
	if err = os.Rename(tmp, filepath.Join(dir, name)); err != nil {
		return err
	}
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	return errors.Join(d.Sync(), d.Close())
}

func fingerprint(cert tls.Certificate) string {
	sum := sha256.Sum256(cert.Certificate[0])
	return hex.EncodeToString(sum[:])
}
