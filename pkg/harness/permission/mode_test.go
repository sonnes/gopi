package permission_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonnes/gopi/pkg/harness/permission"
)

func TestParseMode(t *testing.T) {
	tests := []struct {
		in   string
		want permission.Mode
	}{
		{in: "", want: permission.Default},
		{in: "default", want: permission.Default},
		{in: "acceptEdits", want: permission.AcceptEdits},
		{in: "bypassPermissions", want: permission.Bypass},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got, err := permission.ParseMode(tt.in)
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestParseModeUnknown(t *testing.T) {
	for _, in := range []string{"auto", "Default", "bypass"} {
		t.Run(in, func(t *testing.T) {
			_, err := permission.ParseMode(in)
			require.Error(t, err)
			assert.Contains(t, err.Error(), in)
		})
	}
}

func TestStricter(t *testing.T) {
	tests := []struct {
		a, b permission.Mode
		want permission.Mode
	}{
		{a: permission.Default, b: permission.Bypass, want: permission.Default},
		{a: permission.Bypass, b: permission.Default, want: permission.Default},
		{a: permission.Default, b: permission.AcceptEdits, want: permission.Default},
		{a: permission.AcceptEdits, b: permission.Bypass, want: permission.AcceptEdits},
		{a: permission.Bypass, b: permission.Bypass, want: permission.Bypass},
		{a: "", b: permission.Bypass, want: permission.Default},
		{a: "unknown", b: permission.Bypass, want: permission.Default},
	}
	for _, tt := range tests {
		t.Run(string(tt.a)+"_"+string(tt.b), func(t *testing.T) {
			assert.Equal(t, tt.want, permission.Stricter(tt.a, tt.b))
		})
	}
}
