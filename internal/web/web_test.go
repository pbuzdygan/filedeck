package web

import (
	"encoding/json"
	"io/fs"
	"os"
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
	html, _ := fs.ReadFile(files, "static/index.html")
	for _, m := range regexp.MustCompile(`data-(?:i18n(?:-[a-z-]+)?|tip-i18n)="([^"]+)"`).FindAllStringSubmatch(string(html), -1) {
		if _, ok := en[m[1]]; !ok {
			t.Errorf("index.html uses missing key %q", m[1])
		}
	}
	js, _ := fs.ReadFile(files, "static/app.js")
	namespaces := map[string]bool{}
	for k := range en {
		namespaces[strings.SplitN(k, ".", 2)[0]] = true
	}
	// Any quoted "namespace.key" literal (t('x'), ternaries, tables) must exist.
	for _, m := range regexp.MustCompile(`'([a-z]+)\.([a-z0-9_]+)'`).FindAllStringSubmatch(string(js), -1) {
		if namespaces[m[1]] {
			if _, ok := en[m[1]+"."+m[2]]; !ok {
				t.Errorf("app.js uses missing key %q", m[1]+"."+m[2])
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
	api, err := os.ReadFile("../api/http_linux.go")
	if err != nil {
		t.Fatal(err)
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
