package bearer_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nthw-dev/user-management-api/internal/adapter/inbound/bearer"
)

func TestToken(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		in     string
		want   string
		wantOK bool
	}{
		{name: "ordinary form", in: "Bearer abc.def", want: "abc.def", wantOK: true},
		{name: "scheme is case-insensitive", in: "bearer abc", want: "abc", wantOK: true},
		{name: "trailing whitespace is trimmed from the token", in: "Bearer abc  ", want: "abc", wantOK: true},
		{name: "no scheme", in: "abc", wantOK: false},
		{name: "a different scheme", in: "Basic abc", wantOK: false},
		{name: "scheme only", in: "Bearer ", wantOK: false},
		{name: "scheme followed by whitespace alone", in: "Bearer    ", wantOK: false},
		{name: "empty value", in: "", wantOK: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, ok := bearer.Token(tt.in)
			require.Equal(t, tt.wantOK, ok)
			require.Equal(t, tt.want, got)
		})
	}
}
