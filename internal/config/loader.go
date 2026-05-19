package config

import (
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/knadh/koanf/parsers/yaml"
	"github.com/knadh/koanf/providers/rawbytes"
	"github.com/knadh/koanf/v2"
)

var envPattern = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)(?::-([^}]*))?\}`)

func InterpolateEnv(raw []byte) []byte {
	return envPattern.ReplaceAllFunc(raw, func(match []byte) []byte {
		groups := envPattern.FindSubmatch(match)
		if len(groups) < 2 {
			return match
		}
		name := string(groups[1])
		def := ""
		if len(groups) >= 3 {
			def = string(groups[2])
		}
		if v, ok := os.LookupEnv(name); ok && v != "" {
			return []byte(v)
		}
		return []byte(def)
	})
}

func Load(path string) (*Config, error) {
	if path == "" {
		return nil, fmt.Errorf("config path is empty")
	}

	raw, err := os.ReadFile(path) //nolint:gosec // path is operator-supplied
	if err != nil {
		return nil, fmt.Errorf("read config %s: %w", path, err)
	}

	return LoadFromBytes(raw)
}

func LoadFromBytes(raw []byte) (*Config, error) {
	interpolated := InterpolateEnv(raw)

	k := koanf.New(".")
	if err := k.Load(rawbytes.Provider(interpolated), yaml.Parser()); err != nil {
		return nil, fmt.Errorf("parse yaml: %w", err)
	}

	cfg := Defaults()
	if err := k.UnmarshalWithConf("", cfg, koanf.UnmarshalConf{Tag: "koanf"}); err != nil {
		return nil, fmt.Errorf("unmarshal: %w", err)
	}

	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("validate: %w", err)
	}

	return cfg, nil
}

func MustExpandHome(p string) string {
	if !strings.HasPrefix(p, "~/") {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return p
	}
	return home + p[1:]
}
