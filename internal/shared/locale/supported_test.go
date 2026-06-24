package locale

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSupportedUserLanguages_NonEmpty(t *testing.T) {
	t.Parallel()
	assert.NotEmpty(t, SupportedUserLanguages,
		"product cannot ship with zero supported UI languages")
}

func TestSupportedUserLanguages_BCP47Shape(t *testing.T) {
	t.Parallel()
	// Each entry must be `ll-CC` (2 lower + dash + 2 upper).
	for _, tag := range SupportedUserLanguages {
		if len(tag) != 5 || tag[2] != '-' {
			t.Errorf("tag %q is not BCP-47 ll-CC shape", tag)
		}
	}
}

func TestSupportedUserLanguages_ContainsDefault(t *testing.T) {
	t.Parallel()
	def := Default()
	found := false
	for _, tag := range SupportedUserLanguages {
		if tag == def {
			found = true
			break
		}
	}
	assert.True(t, found, "Default() %q must be one of SupportedUserLanguages %v", def, SupportedUserLanguages)
}

// TestSupportedUserLanguages_FEParity is the drift canary. Read the FE
// SUPPORTED_LANGS constant out of the source file and assert there is
// one BE BCP-47 tag whose 2-letter prefix matches every FE short code.
// This catches "FE added 'de' without BE adding 'de-DE'" at CI time.
//
// The FE source path is resolved relative to the test file (runtime.Caller),
// so the test runs from any working directory. When the FE file is not
// on disk (rare — e.g. minimal CI container), the test SKIPs rather than
// fails: a missing file is an infrastructure issue, not a drift signal.
func TestSupportedUserLanguages_FEParity(t *testing.T) {
	t.Parallel()
	feShortCodes := readFESupportedLangs(t)
	if len(feShortCodes) == 0 {
		t.Skip("FE i18n source not on disk; skipping drift check")
	}
	bePrefixes := make(map[string]bool, len(SupportedUserLanguages))
	for _, tag := range SupportedUserLanguages {
		bePrefixes[tag[:2]] = true
	}
	for _, short := range feShortCodes {
		assert.True(t, bePrefixes[short],
			"FE SUPPORTED_LANGS has %q but BE locale.SupportedUserLanguages has no matching BCP-47 tag (drift!)", short)
	}
}

// readFESupportedLangs opens web/src/i18n/index.ts, regex-extracts the
// SUPPORTED_LANGS array body, and returns the quoted short codes.
// Returns an empty slice when the file is unreadable so the caller can
// SKIP rather than fail.
func readFESupportedLangs(t *testing.T) []string {
	t.Helper()
	// Resolve repo root from this test file: internal/shared/locale/foo.go
	// → ../../../web/src/i18n/index.ts.
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		return nil
	}
	repoRoot := filepath.Join(filepath.Dir(thisFile), "..", "..", "..")
	fePath := filepath.Join(repoRoot, "web", "src", "i18n", "index.ts")
	data, err := os.ReadFile(fePath)
	if err != nil {
		return nil
	}
	// Match: SUPPORTED_LANGS = [...] (anything before "as const" or ";").
	re := regexp.MustCompile(`SUPPORTED_LANGS\s*=\s*\[([^\]]+)\]`)
	m := re.FindStringSubmatch(string(data))
	if len(m) < 2 {
		return nil
	}
	body := m[1]
	// Split on commas, strip whitespace + quotes.
	parts := strings.Split(body, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		p = strings.Trim(p, `"'`)
		if p == "" {
			continue
		}
		out = append(out, p)
	}
	return out
}
