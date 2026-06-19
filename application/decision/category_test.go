package decision

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestClassify_EveryReason(t *testing.T) {
	t.Parallel()
	for reason, want := range reasonCategory {
		t.Run(string(reason), func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, want, Classify(string(reason)))
		})
	}
}

func TestClassify_EmptyAndUnknownFallback(t *testing.T) {
	t.Parallel()
	assert.Equal(t, CategoryUnknown, Classify(""))
	assert.Equal(t, CategoryUnknown, Classify("some_future_unmapped_reason"))
}

// Asserts reasonCategory covers every Reason constant in reasons.go.
func TestClassify_NoDriftFromReasonsFile(t *testing.T) {
	t.Parallel()
	_, thisFile, _, ok := runtime.Caller(0)
	require.True(t, ok)
	path := filepath.Join(filepath.Dir(thisFile), "..", "..", "domain", "decision", "reasons.go")
	f, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	require.NoError(t, err)
	count := 0
	for _, d := range f.Decls {
		if gd, ok := d.(*ast.GenDecl); ok && gd.Tok == token.CONST {
			for _, s := range gd.Specs {
				if vs, ok := s.(*ast.ValueSpec); ok {
					count += len(vs.Names)
				}
			}
		}
	}
	assert.Equal(t, count, len(reasonCategory),
		"reasons.go declares %d consts but reasonCategory has %d — add new Reasons to category.go",
		count, len(reasonCategory))
}
