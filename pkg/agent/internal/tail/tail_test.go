package tail_test

import (
	"io"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/sonnes/gopi/pkg/agent/internal/tail"
)

func TestText(t *testing.T) {
	tests := []struct {
		name string
		in   string
		max  int
		want string
	}{
		{
			name: "short text",
			in:   "error: bad flag\n",
			max:  64,
			want: "error: bad flag",
		},
		{
			name: "keeps the last bytes",
			in:   "0123456789",
			max:  4,
			want: "6789",
		},
		{
			name: "strips color codes",
			in:   "\x1b[2m2026\x1b[0m \x1b[31mERROR\x1b[0m failed",
			max:  64,
			want: "2026 ERROR failed",
		},
		{
			name: "empty",
			in:   "",
			max:  64,
			want: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tl := tail.Read(strings.NewReader(tt.in), tt.max)
			assert.Equal(t, tt.want, tl.Text(time.Second))
		})
	}
}

func TestTextDoesNotWaitPastGrace(t *testing.T) {
	r, w := io.Pipe()
	defer w.Close()
	tl := tail.Read(r, 64)
	_, err := w.Write([]byte("partial"))
	assert.NoError(t, err)

	start := time.Now()
	assert.Equal(t, "partial", tl.Text(10*time.Millisecond))
	assert.Less(t, time.Since(start), time.Second)
}
