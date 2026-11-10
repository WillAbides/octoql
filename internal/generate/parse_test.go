package generate

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const expandFilenamesDir = "testdata/expandFilenames"

func filepathJoinAll(a string, bs []string) []string {
	ret := make([]string, len(bs))
	for i, b := range bs {
		ret[i] = filepath.Join(a, b)
	}
	return ret
}

func TestExpandFilenames(t *testing.T) {
	tests := []struct {
		name  string
		globs []string
		files []string
		err   bool
	}{
		{"SingleFile", []string{"a/b/c"}, []string{"a/b/c"}, false},
		{"OneStar", []string{"a/*/c"}, []string{"a/b/c"}, false},
		{"StarExt", []string{"a/b/*"}, []string{"a/b/c", "a/b/c.d"}, false},
		{"TwoStar", []string{"**/c"}, []string{"a/b/c"}, false},
		{"TwoStarSuffix", []string{"**/*"}, []string{"a/b/c", "a/b/c.d"}, false},
		{"Repeated", []string{"a/b/c", "a/b/*"}, []string{"a/b/c", "a/b/c.d"}, false},
		{"Empty", []string{"bogus/*"}, nil, true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			files, err := expandFilenames(filepathJoinAll(expandFilenamesDir, test.globs))
			if test.err {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.ElementsMatch(t, filepathJoinAll(expandFilenamesDir, test.files), files)
		})
	}
}
