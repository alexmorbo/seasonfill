package externalservices

import "testing"

func TestMergeWithSource(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		svc       Service
		db        Settings
		env       map[string]string
		wantKey   string
		wantSrc   SourceMap
		wantEnabl bool
	}{
		{
			name:      "fresh install (db empty, env empty)",
			svc:       ServiceTMDB,
			wantSrc:   SourceMap{},
			wantEnabl: false,
		},
		{
			name:      "fresh install + env token (fallback)",
			svc:       ServiceTMDB,
			env:       map[string]string{"SEASONFILL_TMDB_TOKEN": "env-key"},
			wantKey:   "env-key",
			wantSrc:   SourceMap{APIKey: FieldSourceEnv},
			wantEnabl: true,
		},
		{
			name:      "db has key, env empty (db wins)",
			svc:       ServiceOMDB,
			db:        Settings{APIKey: "db-key", APIKeyLast4: "-key", Enabled: true},
			wantKey:   "db-key",
			wantSrc:   SourceMap{APIKey: FieldSourceDB},
			wantEnabl: true,
		},
		{
			name:      "db + env both set (env overrides per PRD §10.4.4)",
			svc:       ServiceOMDB,
			db:        Settings{APIKey: "db-key", APIKeyLast4: "-key", Enabled: true},
			env:       map[string]string{"SEASONFILL_OMDB_TOKEN": "env-key"},
			wantKey:   "env-key",
			wantSrc:   SourceMap{APIKey: FieldSourceEnv},
			wantEnabl: true,
		},
		{
			name:    "proxy fields from db, token from env",
			svc:     ServiceTMDB,
			db:      Settings{ProxyURL: "http://db:1", ProxyUsername: "u", ProxyPassword: "p"},
			env:     map[string]string{"SEASONFILL_TMDB_TOKEN": "env-key"},
			wantKey: "env-key",
			wantSrc: SourceMap{
				APIKey:        FieldSourceEnv,
				ProxyURL:      FieldSourceDB,
				ProxyUsername: FieldSourceDB,
				ProxyPassword: FieldSourceDB,
			},
			wantEnabl: true,
		},
		{
			name: "all env, no db",
			svc:  ServiceTVDB,
			env: map[string]string{
				"SEASONFILL_TVDB_TOKEN":      "k",
				"SEASONFILL_TVDB_PROXY_URL":  "http://env:2",
				"SEASONFILL_TVDB_PROXY_USER": "eu",
				"SEASONFILL_TVDB_PROXY_PASS": "ep",
			},
			wantKey: "k",
			wantSrc: SourceMap{
				APIKey:        FieldSourceEnv,
				ProxyURL:      FieldSourceEnv,
				ProxyUsername: FieldSourceEnv,
				ProxyPassword: FieldSourceEnv,
			},
			wantEnabl: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			env := func(name string) string { return tt.env[name] }
			got, src := MergeWithSource(tt.svc, tt.db, env)
			if got.APIKey != tt.wantKey {
				t.Fatalf("APIKey = %q want %q", got.APIKey, tt.wantKey)
			}
			if got.Enabled != tt.wantEnabl {
				t.Fatalf("Enabled = %v want %v", got.Enabled, tt.wantEnabl)
			}
			if src != tt.wantSrc {
				t.Fatalf("SourceMap = %+v want %+v", src, tt.wantSrc)
			}
		})
	}
}

// TestMerge_BackwardCompatibility guards the public Merge signature
// against drift. The thin wrapper must produce byte-identical Settings
// to MergeWithSource for every input.
func TestMerge_BackwardCompatibility(t *testing.T) {
	t.Parallel()
	env := func(string) string { return "x" }
	a := Merge(ServiceTMDB, Settings{APIKey: "db"}, env)
	b, _ := MergeWithSource(ServiceTMDB, Settings{APIKey: "db"}, env)
	if a != b {
		t.Fatalf("Merge drift: %+v != %+v", a, b)
	}
}
