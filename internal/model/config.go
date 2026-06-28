package model

import "time"

// ConfigMapRef is the neutral list-view summary of a ConfigMap: its identity plus
// the sorted set of data keys it carries (values are not exposed by a listing).
type ConfigMapRef struct {
	Cluster   string   `json:"cluster"`
	Namespace string   `json:"namespace"`
	Name      string   `json:"name"`
	Keys      []string `json:"keys"`
}

// ConfigMapDetail is a single ConfigMap's full data. Binary (non-UTF8) entries
// are surfaced with the sentinel "<binary>" rather than raw bytes. ConfigMap data
// is not secret, so values are always shown when the object is readable.
type ConfigMapDetail struct {
	Cluster   string            `json:"cluster"`
	Namespace string            `json:"namespace"`
	Name      string            `json:"name"`
	Data      map[string]string `json:"data"`
}

// SecretEntry is one key of a Secret. Value is populated only when the request is
// authorized to reveal it (or for public certificate entries); otherwise Masked
// is set and Value is empty. IsCert flags entries whose value is a PEM
// certificate (tls.crt / ca.crt / a PEM-looking value).
type SecretEntry struct {
	Key    string `json:"key"`
	Value  string `json:"value,omitempty"`
	Masked bool   `json:"masked,omitempty"`
	IsCert bool   `json:"is_cert,omitempty"`
}

// CertInfo is parsed, PUBLIC metadata of an X.509 certificate (the leaf of a
// kubernetes.io/tls secret's tls.crt). It is always safe to return when the
// secret is readable — none of these fields disclose private key material.
type CertInfo struct {
	SubjectCN     string    `json:"subject_cn"`
	IssuerCN      string    `json:"issuer_cn"`
	NotBefore     time.Time `json:"not_before"`
	NotAfter      time.Time `json:"not_after"`
	DNSNames      []string  `json:"dns_names,omitempty"`
	Serial        string    `json:"serial,omitempty"`
	IsCA          bool      `json:"is_ca,omitempty"`
	KeyAlgorithm  string    `json:"key_algorithm,omitempty"`
	Expired       bool      `json:"expired"`
	ExpiresInDays int       `json:"expires_in_days"`
}

// SecretRef is the neutral list-view summary of a Secret: identity, type, sorted
// keys, and — for kubernetes.io/tls secrets — parsed public certificate metadata.
// A listing never exposes secret values.
type SecretRef struct {
	Cluster   string    `json:"cluster"`
	Namespace string    `json:"namespace"`
	Name      string    `json:"name"`
	Type      string    `json:"type"`
	Keys      []string  `json:"keys"`
	IsTLS     bool      `json:"is_tls,omitempty"`
	Cert      *CertInfo `json:"cert,omitempty"`
}

// SecretDetail is a single Secret's entries plus, for a TLS secret, its public
// certificate metadata. Whether entry values are populated depends on the
// reveal/masking policy applied by the adapter and API.
type SecretDetail struct {
	Cluster   string        `json:"cluster"`
	Namespace string        `json:"namespace"`
	Name      string        `json:"name"`
	Type      string        `json:"type"`
	Entries   []SecretEntry `json:"entries"`
	Cert      *CertInfo     `json:"cert,omitempty"`
}
