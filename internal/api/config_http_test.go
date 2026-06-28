package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"lotsman/internal/model"
	"lotsman/internal/rbac"
)

// TestConfigSecret_ScopedViewerForbidden asserts the CanView gate the
// configmap/secret handlers share with the pod handlers: a viewer scoped to one
// cluster is denied another, which the handlers turn into a 403. (The lean
// default policy mints global viewers, so we exercise the exact enforcer decision
// the handler relies on, as the pod-handler tests do.)
func TestConfigSecret_ScopedViewerForbidden(t *testing.T) {
	enf := rbac.New([]rbac.Binding{{Role: rbac.RoleViewer, Cluster: "prod"}})
	if !enf.CanView("prod", "demo") {
		t.Fatalf("prod-scoped viewer should be allowed prod/demo")
	}
	if enf.CanView("staging", "demo") {
		t.Fatalf("prod-scoped viewer must be denied staging/demo (handler returns 403)")
	}
}

func newReq(t *testing.T, target, cluster, namespace, name, login string) *http.Request {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, target, nil)
	req.SetPathValue("cluster", cluster)
	req.SetPathValue("namespace", namespace)
	if name != "" {
		req.SetPathValue("name", name)
	}
	if login != "" {
		req.AddCookie(mintCookie(t, login))
	}
	return req
}

func TestListConfigMaps_OK_AllNamespaces(t *testing.T) {
	src := &stubClusterSource{configMaps: []model.ConfigMapRef{
		{Cluster: "prod", Namespace: "demo", Name: "app-config", Keys: []string{"a", "b"}},
	}}
	srv := podTestServer(t, testSSOConfig, src)

	req := newReq(t, "/api/v1/clusters/prod/namespaces/_all/configmaps", "prod", "_all", "", "viewer-user")
	rec := httptest.NewRecorder()
	srv.handleListConfigMaps(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, want 200", rec.Code)
	}
	// _all maps to the empty (all-namespaces) value at the adapter.
	if src.lastNamespace != "" {
		t.Errorf("_all should resolve to empty namespace, got %q", src.lastNamespace)
	}
	var got []model.ConfigMapRef
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) != 1 || got[0].Name != "app-config" {
		t.Fatalf("unexpected body: %+v", got)
	}
}

func TestGetConfigMap_OK(t *testing.T) {
	src := &stubClusterSource{configMapDetail: model.ConfigMapDetail{
		Cluster: "prod", Namespace: "demo", Name: "app-config",
		Data: map[string]string{"a.yaml": "v"},
	}}
	srv := podTestServer(t, testSSOConfig, src)

	req := newReq(t, "/api/v1/clusters/prod/namespaces/demo/configmaps/app-config", "prod", "demo", "app-config", "viewer-user")
	rec := httptest.NewRecorder()
	srv.handleGetConfigMap(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, want 200", rec.Code)
	}
	var got model.ConfigMapDetail
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Data["a.yaml"] != "v" {
		t.Fatalf("unexpected data: %+v", got.Data)
	}
}

func TestListConfigMaps_Unauthenticated(t *testing.T) {
	src := &stubClusterSource{}
	srv := podTestServer(t, testSSOConfig, src)

	req := newReq(t, "/api/v1/clusters/prod/namespaces/demo/configmaps", "prod", "demo", "", "")
	rec := httptest.NewRecorder()
	srv.handleListConfigMaps(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("got %d, want 401", rec.Code)
	}
}

func certDetail() *model.CertInfo {
	return &model.CertInfo{SubjectCN: "svc.local", ExpiresInDays: 30}
}

func tlsSecretDetail() model.SecretDetail {
	return model.SecretDetail{
		Cluster: "prod", Namespace: "demo", Name: "tls-cert", Type: "kubernetes.io/tls",
		Entries: []model.SecretEntry{
			{Key: "tls.crt", Value: "-----BEGIN CERTIFICATE-----...", IsCert: true},
			{Key: "tls.key", Value: "PRIVATE", IsCert: false},
		},
		Cert: certDetail(),
	}
}

func TestListSecrets_OK_CarriesCert(t *testing.T) {
	src := &stubClusterSource{secrets: []model.SecretRef{
		{Cluster: "prod", Namespace: "demo", Name: "tls-cert", Type: "kubernetes.io/tls", Keys: []string{"tls.crt", "tls.key"}, IsTLS: true, Cert: certDetail()},
	}}
	srv := podTestServer(t, testSSOConfig, src)

	req := newReq(t, "/api/v1/clusters/prod/namespaces/demo/secrets", "prod", "demo", "", "viewer-user")
	rec := httptest.NewRecorder()
	srv.handleListSecrets(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, want 200", rec.Code)
	}
	var got []model.SecretRef
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) != 1 || !got[0].IsTLS || got[0].Cert == nil || got[0].Cert.SubjectCN != "svc.local" {
		t.Fatalf("unexpected secret list: %+v", got)
	}
}

func TestGetSecret_NonAdminMasksValuesKeepsCert(t *testing.T) {
	src := &stubClusterSource{secretDetail: tlsSecretDetail()}
	srv := podTestServer(t, testSSOConfig, src)

	req := newReq(t, "/api/v1/clusters/prod/namespaces/demo/secrets/tls-cert", "prod", "demo", "tls-cert", "viewer-user")
	rec := httptest.NewRecorder()
	srv.handleGetSecret(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, want 200", rec.Code)
	}
	// Non-admin must NOT request reveal at the adapter.
	if src.lastSecretQuery.Reveal {
		t.Errorf("non-admin must not request Reveal")
	}
	var got model.SecretDetail
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	// Defensive masking: every entry value cleared + Masked set for non-admin.
	for _, e := range got.Entries {
		if e.Value != "" || !e.Masked {
			t.Errorf("entry %q should be masked for non-admin, got %+v", e.Key, e)
		}
	}
	// Cert metadata is public and returned regardless.
	if got.Cert == nil || got.Cert.SubjectCN != "svc.local" {
		t.Fatalf("cert metadata should survive masking: %+v", got.Cert)
	}
}

func TestGetSecret_AdminReveals(t *testing.T) {
	src := &stubClusterSource{secretDetail: tlsSecretDetail()}
	srv := podTestServer(t, testSSOConfig, src)

	req := newReq(t, "/api/v1/clusters/prod/namespaces/demo/secrets/tls-cert", "prod", "demo", "tls-cert", "admin-user")
	rec := httptest.NewRecorder()
	srv.handleGetSecret(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, want 200", rec.Code)
	}
	if !src.lastSecretQuery.Reveal {
		t.Errorf("admin must request Reveal")
	}
	var got model.SecretDetail
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	var key model.SecretEntry
	for _, e := range got.Entries {
		if e.Key == "tls.key" {
			key = e
		}
	}
	if key.Value != "PRIVATE" || key.Masked {
		t.Fatalf("admin should see private key value: %+v", key)
	}
}

func TestGetSecret_AnonymousReveals(t *testing.T) {
	// No SSO: anonymous is a global admin, so values are revealed.
	src := &stubClusterSource{secretDetail: tlsSecretDetail()}
	srv := podTestServer(t, "", src)

	req := newReq(t, "/api/v1/clusters/prod/namespaces/demo/secrets/tls-cert", "prod", "demo", "tls-cert", "")
	rec := httptest.NewRecorder()
	srv.handleGetSecret(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, want 200", rec.Code)
	}
	if !src.lastSecretQuery.Reveal {
		t.Errorf("anonymous admin must request Reveal")
	}
}
