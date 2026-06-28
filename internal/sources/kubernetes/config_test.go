package kubernetes

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"lotsman/internal/model"
	"lotsman/internal/sources"
)

// genSelfSignedCert builds a self-signed ECDSA certificate for tests with the
// given CN, SANs and NotAfter, returning the PEM-encoded certificate bytes.
func genSelfSignedCert(t *testing.T, cn string, dnsNames []string, notBefore, notAfter time.Time) []byte {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("genkey: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(4242),
		Subject:      pkix.Name{CommonName: cn},
		// Self-signed: x509 derives the issuer from the (parent==self) template's
		// Subject, so IssuerCN will equal cn. The explicit Issuer below is ignored
		// for a self-signed cert but documents intent.
		Issuer:                pkix.Name{CommonName: cn},
		NotBefore:             notBefore,
		NotAfter:              notAfter,
		DNSNames:              dnsNames,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("createcert: %v", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}

func TestListConfigMaps(t *testing.T) {
	cs := fake.NewSimpleClientset(
		&corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: "app-config", Namespace: "team-a"},
			Data:       map[string]string{"b.yaml": "x", "a.yaml": "y"},
			BinaryData: map[string][]byte{"blob.bin": {0xff, 0x00}},
		},
		&corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: "other", Namespace: "team-b"},
			Data:       map[string]string{"k": "v"},
		},
	)
	c := newWithClient("local", cs)

	all, err := c.ListConfigMaps(context.Background(), "")
	if err != nil {
		t.Fatalf("ListConfigMaps all: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("want 2 configmaps across all namespaces, got %d", len(all))
	}

	one, err := c.ListConfigMaps(context.Background(), "team-a")
	if err != nil {
		t.Fatalf("ListConfigMaps team-a: %v", err)
	}
	if len(one) != 1 || one[0].Name != "app-config" {
		t.Fatalf("unexpected team-a configmaps: %+v", one)
	}
	if one[0].Cluster != "local" || one[0].Namespace != "team-a" {
		t.Fatalf("unexpected identity: %+v", one[0])
	}
	// Keys: sorted union of Data + BinaryData.
	want := []string{"a.yaml", "b.yaml", "blob.bin"}
	if len(one[0].Keys) != len(want) {
		t.Fatalf("keys = %v, want %v", one[0].Keys, want)
	}
	for i := range want {
		if one[0].Keys[i] != want[i] {
			t.Fatalf("keys = %v, want %v", one[0].Keys, want)
		}
	}
}

func TestGetConfigMap(t *testing.T) {
	cs := fake.NewSimpleClientset(&corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "app-config", Namespace: "team-a"},
		Data:       map[string]string{"a.yaml": "hello"},
		BinaryData: map[string][]byte{"blob.bin": {0x00, 0x01}},
	})
	c := newWithClient("local", cs)

	d, err := c.GetConfigMap(context.Background(), model.ResourceRef{Namespace: "team-a", Name: "app-config"})
	if err != nil {
		t.Fatalf("GetConfigMap: %v", err)
	}
	if d.Data["a.yaml"] != "hello" {
		t.Fatalf("Data[a.yaml] = %q, want hello", d.Data["a.yaml"])
	}
	if d.Data["blob.bin"] != "<binary>" {
		t.Fatalf("binary entry = %q, want <binary>", d.Data["blob.bin"])
	}
}

// tlsSecret builds a kubernetes.io/tls secret carrying the given cert PEM and a
// placeholder private key.
func tlsSecret(ns, name string, certPEM []byte) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Type:       corev1.SecretTypeTLS,
		Data: map[string][]byte{
			corev1.TLSCertKey:       certPEM,
			corev1.TLSPrivateKeyKey: []byte("-----BEGIN PRIVATE KEY-----\nNOTREAL\n-----END PRIVATE KEY-----\n"),
		},
	}
}

func TestListSecrets(t *testing.T) {
	notAfter := time.Now().Add(10 * 24 * time.Hour)
	certPEM := genSelfSignedCert(t, "example.com", []string{"example.com", "www.example.com"}, time.Now().Add(-time.Hour), notAfter)
	cs := fake.NewSimpleClientset(
		tlsSecret("team-a", "tls-cert", certPEM),
		&corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "db-creds", Namespace: "team-a"},
			Type:       corev1.SecretTypeOpaque,
			Data:       map[string][]byte{"password": []byte("hunter2"), "user": []byte("admin")},
		},
	)
	c := newWithClient("local", cs)

	secrets, err := c.ListSecrets(context.Background(), "team-a")
	if err != nil {
		t.Fatalf("ListSecrets: %v", err)
	}
	if len(secrets) != 2 {
		t.Fatalf("want 2 secrets, got %d", len(secrets))
	}

	var tls, opaque *model.SecretRef
	for i := range secrets {
		switch secrets[i].Name {
		case "tls-cert":
			tls = &secrets[i]
		case "db-creds":
			opaque = &secrets[i]
		}
	}
	if tls == nil || opaque == nil {
		t.Fatalf("missing expected secrets: %+v", secrets)
	}

	// A listing never exposes values; SecretRef has no value fields by design.
	if !tls.IsTLS || tls.Type != string(corev1.SecretTypeTLS) {
		t.Fatalf("tls secret flags wrong: %+v", tls)
	}
	wantKeys := []string{corev1.TLSCertKey, corev1.TLSPrivateKeyKey} // sorted: tls.crt, tls.key
	if len(tls.Keys) != len(wantKeys) || tls.Keys[0] != wantKeys[0] || tls.Keys[1] != wantKeys[1] {
		t.Fatalf("tls keys = %v, want %v", tls.Keys, wantKeys)
	}
	if tls.Cert == nil {
		t.Fatal("expected parsed Cert on TLS secret")
	}
	if tls.Cert.SubjectCN != "example.com" {
		t.Fatalf("SubjectCN = %q, want example.com", tls.Cert.SubjectCN)
	}
	// x509 stores times at whole-second resolution, so compare truncated.
	if !tls.Cert.NotAfter.Equal(notAfter.UTC().Truncate(time.Second)) {
		t.Fatalf("NotAfter = %v, want ~%v", tls.Cert.NotAfter, notAfter.UTC().Truncate(time.Second))
	}
	if tls.Cert.Expired {
		t.Fatal("cert should not be expired")
	}
	// 10 days out, but minus a sliver of elapsed time -> floor is 9.
	if tls.Cert.ExpiresInDays != 9 && tls.Cert.ExpiresInDays != 10 {
		t.Fatalf("ExpiresInDays = %d, want 9 or 10", tls.Cert.ExpiresInDays)
	}

	if opaque.IsTLS || opaque.Cert != nil {
		t.Fatalf("opaque secret should not be TLS / have cert: %+v", opaque)
	}
}

func TestGetSecretMaskingAndReveal(t *testing.T) {
	certPEM := genSelfSignedCert(t, "svc.local", nil, time.Now().Add(-time.Hour), time.Now().Add(48*time.Hour))
	sec := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "tls-cert", Namespace: "team-a"},
		Type:       corev1.SecretTypeTLS,
		Data: map[string][]byte{
			corev1.TLSCertKey:       certPEM,
			corev1.TLSPrivateKeyKey: []byte("PRIVATE-KEY-MATERIAL"),
		},
	}
	c := newWithClient("local", fake.NewSimpleClientset(sec))

	entryByKey := func(d model.SecretDetail, key string) model.SecretEntry {
		for _, e := range d.Entries {
			if e.Key == key {
				return e
			}
		}
		t.Fatalf("entry %q not found in %+v", key, d.Entries)
		return model.SecretEntry{}
	}

	// Reveal=false: cert is public (shown), private key is masked, cert metadata present.
	masked, err := c.GetSecret(context.Background(), sources.SecretQuery{
		Resource: model.ResourceRef{Namespace: "team-a", Name: "tls-cert"},
		Reveal:   false,
	})
	if err != nil {
		t.Fatalf("GetSecret reveal=false: %v", err)
	}
	if masked.Cert == nil || masked.Cert.SubjectCN != "svc.local" {
		t.Fatalf("cert metadata missing when masked: %+v", masked.Cert)
	}
	crt := entryByKey(masked, corev1.TLSCertKey)
	if crt.Value == "" || crt.Masked || !crt.IsCert {
		t.Fatalf("tls.crt should be public+revealed even when masked: %+v", crt)
	}
	key := entryByKey(masked, corev1.TLSPrivateKeyKey)
	if key.Value != "" || !key.Masked {
		t.Fatalf("tls.key must be masked when reveal=false: %+v", key)
	}

	// Reveal=true: everything in the clear.
	revealed, err := c.GetSecret(context.Background(), sources.SecretQuery{
		Resource: model.ResourceRef{Namespace: "team-a", Name: "tls-cert"},
		Reveal:   true,
	})
	if err != nil {
		t.Fatalf("GetSecret reveal=true: %v", err)
	}
	rkey := entryByKey(revealed, corev1.TLSPrivateKeyKey)
	if rkey.Value != "PRIVATE-KEY-MATERIAL" || rkey.Masked {
		t.Fatalf("tls.key should be revealed: %+v", rkey)
	}
	if revealed.Cert == nil {
		t.Fatal("cert metadata should still be present when revealed")
	}
}

func TestParseCertPEM(t *testing.T) {
	notAfter := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	certPEM := genSelfSignedCert(t, "leaf.example", []string{"a.example", "b.example"}, time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC), notAfter)

	now := time.Date(2029, 1, 1, 0, 0, 0, 0, time.UTC) // 365 days before expiry
	info := parseCertPEM(certPEM, now)
	if info == nil {
		t.Fatal("parseCertPEM returned nil for a valid cert")
	}
	if info.SubjectCN != "leaf.example" {
		t.Fatalf("SubjectCN = %q", info.SubjectCN)
	}
	// Self-signed: issuer CN equals subject CN.
	if info.IssuerCN != "leaf.example" {
		t.Fatalf("IssuerCN = %q, want leaf.example (self-signed)", info.IssuerCN)
	}
	if len(info.DNSNames) != 2 || info.DNSNames[0] != "a.example" {
		t.Fatalf("DNSNames = %v", info.DNSNames)
	}
	if info.Serial != "4242" {
		t.Fatalf("Serial = %q, want 4242", info.Serial)
	}
	if info.KeyAlgorithm != "ECDSA" {
		t.Fatalf("KeyAlgorithm = %q, want ECDSA", info.KeyAlgorithm)
	}
	if info.Expired {
		t.Fatal("should not be expired at now=2029")
	}
	if info.ExpiresInDays != 365 {
		t.Fatalf("ExpiresInDays = %d, want 365", info.ExpiresInDays)
	}

	// Past expiry -> Expired true, negative days.
	past := parseCertPEM(certPEM, time.Date(2031, 1, 1, 0, 0, 0, 0, time.UTC))
	if !past.Expired || past.ExpiresInDays >= 0 {
		t.Fatalf("expected expired with negative days, got Expired=%v days=%d", past.Expired, past.ExpiresInDays)
	}

	// Garbage input degrades gracefully.
	if parseCertPEM([]byte("not a pem"), now) != nil {
		t.Fatal("parseCertPEM should return nil on garbage")
	}
}
