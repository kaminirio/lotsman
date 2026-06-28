package ui

import (
	"testing"
	"testing/fstest"
)

func TestResolve(t *testing.T) {
	fsys := fstest.MapFS{
		"index.html":          {Data: []byte("home")},
		"catalog.html":        {Data: []byte("catalog")},
		"settings/index.html": {Data: []byte("settings")}, // trailing-slash style
		"_next/static/app.js": {Data: []byte("js")},
		"favicon.ico":         {Data: []byte("ico")},
	}

	cases := []struct {
		path   string
		want   string
		wantOK bool
	}{
		{"/", "/", true},                                       // root -> index.html via file server
		{"/catalog", "/catalog.html", true},                    // Next per-route export
		{"/catalog.html", "/catalog.html", true},               // exact file
		{"/settings", "/settings/index.html", true},            // trailing-slash export
		{"/_next/static/app.js", "/_next/static/app.js", true}, // asset, exact
		{"/favicon.ico", "/favicon.ico", true},
		{"/does-not-exist", "", false},    // unknown -> SPA fallback
		{"/catalog/deep/link", "", false}, // client-only route -> fallback
	}
	for _, c := range cases {
		got, ok := resolve(fsys, c.path)
		if got != c.want || ok != c.wantOK {
			t.Errorf("resolve(%q) = (%q, %v), want (%q, %v)", c.path, got, ok, c.want, c.wantOK)
		}
	}
}
