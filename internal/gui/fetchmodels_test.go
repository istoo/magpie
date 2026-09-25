package gui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yetone/magpie/internal/catalog"
	"github.com/yetone/magpie/internal/provider"
)

// The editor's Fetch models asks the vendor with what the form holds, as
// CC Switch does: a provider being added gets its list before it is saved,
// and nothing is written; a saved one asked as saved keeps the list; one
// whose URL the form changed only shows it, until Save asks again.
func TestEditorFetchModels(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, ".cache"))
	for _, v := range []string{"CLAUDE_CONFIG_DIR", "CODEX_HOME"} {
		t.Setenv(v, "")
	}
	var auth string
	// lists its models only under /v1, like a relay given without it
	vendor := func(ids ...string) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/v1/models" {
				http.NotFound(w, r)
				return
			}
			auth = r.Header.Get("Authorization")
			var data []map[string]string
			for _, id := range ids {
				data = append(data, map[string]string{"id": id})
			}
			json.NewEncoder(w).Encode(map[string]any{"data": data})
		}))
	}
	a, b := vendor("model-a1", "model-a2"), vendor("model-b1")
	defer a.Close()
	defer b.Close()

	mux := http.NewServeMux()
	providerRoutes(mux, nil, nil)
	post := func(action string, body any) (int, map[string]any) {
		t.Helper()
		js, _ := json.Marshal(body)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/provider/"+action, strings.NewReader(string(js))))
		var out map[string]any
		json.Unmarshal(rec.Body.Bytes(), &out)
		return rec.Code, out
	}
	ids := func(out map[string]any) (got []string) {
		ms, _ := out["models"].([]any)
		for _, m := range ms {
			got = append(got, m.(map[string]any)["id"].(string))
		}
		return got
	}

	// being added: listed with the key typed, and nothing saved
	code, out := post("fetch", map[string]any{"id": "relay", "name": "Relay", "key": "sk-new", "chat": a.URL, "new": true})
	if code != 200 || strings.Join(ids(out), ",") != "model-a1,model-a2" || out["kept"] != false {
		t.Fatalf("new: %d %v", code, out)
	}
	if auth != "Bearer sk-new" {
		t.Errorf("new: asked with %q", auth)
	}
	if _, err := provider.Find("relay"); err == nil {
		t.Error("new: the provider was saved by a fetch")
	}
	if _, err := os.Stat(catalog.LivePath("relay")); err == nil {
		t.Error("new: the list was kept before the provider was saved")
	}

	// no URL to ask at: an error, not an empty list
	if code, _ := post("fetch", map[string]any{"id": "x", "name": "X", "new": true}); code == 200 {
		t.Error("no URL: fetched")
	}

	if err := provider.Save(provider.Provider{ID: "relay", Name: "Relay", Key: "sk-saved", Chat: a.URL}); err != nil {
		t.Fatal(err)
	}

	// saved, asked as saved (the form's key blank): the list is kept
	code, out = post("fetch", map[string]any{"id": "relay", "name": "Relay", "chat": a.URL, "headers": map[string]string{}})
	if code != 200 || len(ids(out)) != 2 || out["kept"] != true {
		t.Fatalf("saved: %d %v", code, out)
	}
	if auth != "Bearer sk-saved" {
		t.Errorf("saved: asked with %q", auth)
	}
	if live, _, ok := catalog.Live("relay"); !ok || len(live) != 2 {
		t.Errorf("saved: live list %v", live)
	}

	// (and, as Refresh always did, the /v1 the saved URL lacked is added)
	if p, _ := provider.Find("relay"); p.Chat != a.URL+"/v1" {
		t.Errorf("saved: chat %q, want …/v1", p.Chat)
	}

	// the form's URL changed: shown, not kept, and the saved URL untouched
	code, out = post("fetch", map[string]any{"id": "relay", "name": "Relay", "chat": b.URL})
	if code != 200 || strings.Join(ids(out), ",") != "model-b1" || out["kept"] != false {
		t.Fatalf("changed: %d %v", code, out)
	}
	if live, _, _ := catalog.Live("relay"); len(live) != 2 {
		t.Errorf("changed: live list replaced by %v", live)
	}
	if p, _ := provider.Find("relay"); p.Chat != a.URL+"/v1" {
		t.Errorf("changed: saved URL now %q", p.Chat)
	}

	// Save with the new URL asks the vendor again, the key unchanged
	if code, out := post("save", map[string]any{"id": "relay", "name": "Relay", "chat": b.URL, "models": []string{"model-b1"}}); code != 200 {
		t.Fatalf("save: %d %v", code, out)
	}
	if live, _, _ := catalog.Live("relay"); len(live) != 1 || live[0].ID != "model-b1" {
		t.Errorf("save: live list %v", live)
	}
	if p, _ := provider.Find("relay"); p.Chat != b.URL+"/v1" || p.Key != "sk-saved" {
		t.Errorf("save: chat %q key %q", p.Chat, p.Key)
	}
}
