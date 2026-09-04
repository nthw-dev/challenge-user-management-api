package user_test

import (
	"go/build"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestDomainHasNoExternalImports enforces the architecture rule through CI, not through review
//
// If someone accidentally drags mongo-driver or chi into the domain, this test catches it immediately
func TestDomainHasNoExternalImports(t *testing.T) {
	pkg, err := build.ImportDir(".", 0)
	require.NoError(t, err)

	for _, imp := range pkg.Imports {
		require.False(t, strings.Contains(imp, "."),
			"the domain package must import only the standard library, but found %q", imp)
	}
}
