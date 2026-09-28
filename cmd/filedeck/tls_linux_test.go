package main

import (
	"crypto/x509"
	"os"
	"path/filepath"
	"testing"
)

func TestSelfSignedCertificateIsReusedAndBoundToHost(t *testing.T) {
	state := t.TempDir()
	first, sum, err := selfSigned(state, "https://localhost:8443")
	if err != nil {
		t.Fatal(err)
	}
	st, err := os.Lstat(filepath.Join(state, selfSignedFile))
	if err != nil || st.Mode().Perm() != 0600 || !st.Mode().IsRegular() {
		t.Fatal(st, err)
	}
	_, again, err := selfSigned(state, "https://localhost:9443")
	if err != nil || again != sum {
		t.Fatal("certificate not reused after restart", err)
	}
	leaf, _ := x509.ParseCertificate(first.Certificate[0])
	if leaf.VerifyHostname("localhost") != nil {
		t.Fatal("wrong host")
	}
	other, changed, err := selfSigned(state, "https://192.168.1.20:8443")
	if err != nil || changed == sum {
		t.Fatal("certificate not replaced for a new host", err)
	}
	leaf, _ = x509.ParseCertificate(other.Certificate[0])
	if leaf.VerifyHostname("192.168.1.20") != nil {
		t.Fatal("IP SAN missing")
	}
	if _, _, err = selfSigned(state, "http://localhost:8080"); err == nil {
		t.Fatal("http origin accepted")
	}
}
