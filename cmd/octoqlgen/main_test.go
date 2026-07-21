package main

import (
	"runtime/debug"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestMetadataFromBuildInfo(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		info *debug.BuildInfo
		want buildMetadata
	}{
		{
			name: "release",
			info: &debug.BuildInfo{
				Main: debug.Module{Version: "v1.2.3"},
				Settings: []debug.BuildSetting{
					{Key: "vcs.revision", Value: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},
					{Key: "vcs.modified", Value: "false"},
				},
			},
			want: buildMetadata{
				version:  "v1.2.3",
				revision: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			},
		},
		{
			name: "clean development build",
			info: &debug.BuildInfo{
				Main: debug.Module{Version: "(devel)"},
				Settings: []debug.BuildSetting{
					{Key: "vcs.revision", Value: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"},
					{Key: "vcs.modified", Value: "false"},
				},
			},
			want: buildMetadata{
				version:  "dev",
				revision: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
			},
		},
		{
			name: "pseudo version",
			info: &debug.BuildInfo{
				Main: debug.Module{
					Version: "v1.2.4-0.20260721120000-cccccccccccc",
				},
			},
			want: buildMetadata{
				version:  "dev",
				revision: "cccccccccccc",
			},
		},
		{
			name: "dirty build",
			info: &debug.BuildInfo{
				Main: debug.Module{Version: "v1.2.3"},
				Settings: []debug.BuildSetting{
					{Key: "vcs.revision", Value: "dddddddddddddddddddddddddddddddddddddddd"},
					{Key: "vcs.modified", Value: "true"},
				},
			},
			want: buildMetadata{version: "dev"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, test.want, metadataFromBuildInfo(test.info))
		})
	}
}
