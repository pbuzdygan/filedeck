package web

import (
	"encoding/json"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

func loadLang(t *testing.T, name string) map[string]string {
	t.Helper()
	data, err := fs.ReadFile(files, "static/lang-"+name+".json")
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]string
	if err = json.Unmarshal(data, &m); err != nil {
		t.Fatal(name, err)
	}
	return m
}

var placeholder = regexp.MustCompile(`\{\w+\}`)

func vars(s string) string {
	found := placeholder.FindAllString(s, -1)
	sort.Strings(found)
	return strings.Join(found, ",")
}

// TestTranslations keeps English (default) and every other language complete:
// same keys and placeholders, and every key used by the interface exists.
func TestTranslations(t *testing.T) {
	en := loadLang(t, "en")
	for _, other := range []string{"pl"} {
		tr := loadLang(t, other)
		for k, v := range en {
			w, ok := tr[k]
			switch {
			case !ok:
				t.Errorf("%s: missing %q", other, k)
			case strings.TrimSpace(w) == "":
				t.Errorf("%s: empty %q", other, k)
			case vars(v) != vars(w):
				t.Errorf("%s: %q placeholders %s, English has %s", other, k, vars(w), vars(v))
			}
		}
		for k := range tr {
			if _, ok := en[k]; !ok {
				t.Errorf("%s: key %q not in English", other, k)
			}
		}
	}
	for k, v := range en {
		if strings.TrimSpace(v) == "" {
			t.Errorf("en: empty %q", k)
		}
	}
	for _, page := range []string{"index.html", "share.html"} {
		html, err := fs.ReadFile(files, "static/"+page)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range regexp.MustCompile(`data-(?:i18n(?:-[a-z-]+)?|tip-i18n)="([^"]+)"`).FindAllStringSubmatch(string(html), -1) {
			if _, ok := en[m[1]]; !ok {
				t.Errorf("%s uses missing key %q", page, m[1])
			}
		}
	}
	namespaces := map[string]bool{}
	for k := range en {
		namespaces[strings.SplitN(k, ".", 2)[0]] = true
	}
	for _, script := range []string{"app.js", "share.js"} {
		js, err := fs.ReadFile(files, "static/"+script)
		if err != nil {
			t.Fatal(err)
		}
		// Any quoted "namespace.key" literal (t('x'), ternaries, tables) must exist.
		for _, m := range regexp.MustCompile(`'([a-z]+)\.([a-z0-9_]+)'`).FindAllStringSubmatch(string(js), -1) {
			if namespaces[m[1]] {
				if _, ok := en[m[1]+"."+m[2]]; !ok {
					t.Errorf("%s uses missing key %q", script, m[1]+"."+m[2])
				}
			}
		}
	}
	for _, name := range []string{"auto", "light", "dark"} {
		for _, k := range []string{"theme." + name, "theme." + name + "_long"} {
			if _, ok := en[k]; !ok {
				t.Errorf("missing %q", k)
			}
		}
	}
	// Every error code the API can send is translated.
	sources, err := filepath.Glob("../api/*.go")
	if err != nil {
		t.Fatal(err)
	}
	var api []byte
	for _, name := range sources {
		if !strings.HasSuffix(name, "_test.go") {
			data, err := os.ReadFile(name)
			if err != nil {
				t.Fatal(err)
			}
			api = append(api, data...)
		}
	}
	codes := regexp.MustCompile(`(?:fail\(w, \d+, |return \d+, )"([a-z_]+)"`).FindAllStringSubmatch(string(api), -1)
	if len(codes) < 20 {
		t.Fatal("error codes not found in API source")
	}
	for _, m := range codes {
		if _, ok := en["err."+m[1]]; !ok {
			t.Errorf("API error %q has no err.%s translation", m[1], m[1])
		}
	}
}

// TestIconsAndManifest keeps the PWA manifest valid and every icon it or the
// pages reference embedded and served with a known type.
func TestIconsAndManifest(t *testing.T) {
	data, err := fs.ReadFile(files, "static/manifest.webmanifest")
	if err != nil {
		t.Fatal(err)
	}
	var m struct {
		Name  string `json:"name"`
		Icons []struct {
			Src     string `json:"src"`
			Purpose string `json:"purpose"`
		} `json:"icons"`
	}
	if err = json.Unmarshal(data, &m); err != nil || m.Name == "" || len(m.Icons) < 3 {
		t.Fatalf("manifest %+v: %v", m, err)
	}
	refs := []string{"/favicon.ico"}
	for _, i := range m.Icons {
		refs = append(refs, i.Src)
	}
	for _, page := range []string{"index.html", "share.html"} {
		html, _ := fs.ReadFile(files, "static/"+page)
		for _, r := range regexp.MustCompile(`(?:href|src)="(/(?:assets/[^"]+|favicon\.ico))"`).FindAllStringSubmatch(string(html), -1) {
			refs = append(refs, r[1])
		}
	}
	for _, r := range refs {
		name := strings.TrimPrefix(strings.TrimPrefix(r, "/assets/"), "/")
		if _, err := fs.Stat(files, "static/"+name); err != nil {
			t.Errorf("%s is referenced but not embedded", r)
		}
		if _, ok := types[path.Ext(name)]; !ok {
			t.Errorf("%s has no content type", r)
		}
	}
}

// TestIconsHaveRules catches icons used by the interface without a CSS mask
// rule (they render as solid squares) and rules pointing to missing files.
func TestIconsHaveRules(t *testing.T) {
	css, _ := fs.ReadFile(files, "static/app.css")
	rules := map[string]bool{}
	for _, m := range regexp.MustCompile(`\.ic-([a-z0-9-]+) \{[^}]*url\(/assets/(ti-[a-z0-9-]+\.svg)\)`).FindAllStringSubmatch(string(css), -1) {
		rules[m[1]] = true
		if _, err := fs.Stat(files, "static/"+m[2]); err != nil {
			t.Errorf("rule .ic-%s points to missing %s", m[1], m[2])
		}
	}
	used := regexp.MustCompile(`\bic ic-([a-z0-9-]+)|\bicon\('([a-z0-9-]+)'\)|iconButton\('([a-z0-9-]+)'|icon: '([a-z0-9-]+)'`)
	for _, name := range []string{"index.html", "share.html", "app.js", "share.js"} {
		src, _ := fs.ReadFile(files, "static/"+name)
		for _, m := range used.FindAllStringSubmatch(string(src), -1) {
			icon := m[1] + m[2] + m[3] + m[4]
			if !rules[icon] {
				t.Errorf("%s uses icon %q without a .ic-%s rule", name, icon, icon)
			}
		}
	}
}
