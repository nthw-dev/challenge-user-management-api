package user_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nthw-dev/user-management-api/internal/domain/user"
)

func TestNewEmail(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		in      string
		want    string
		wantErr bool
	}{
		{name: "ordinary form", in: "a@example.com", want: "a@example.com"},
		{name: "lowercased", in: "A@Example.COM", want: "a@example.com"},
		{name: "surrounding whitespace trimmed", in: "  a@example.com  ", want: "a@example.com"},
		{name: "dots and plus signs allowed in the local part", in: "a.b+tag@example.co.th", want: "a.b+tag@example.co.th"},
		{name: "empty value rejected", in: "   ", wantErr: true},
		{name: "missing @ rejected", in: "aexample.com", wantErr: true},
		{name: "domain without a dot rejected", in: "a@example", wantErr: true},
		{name: "display-name form rejected", in: `"Somchai" <a@example.com>`, wantErr: true},
		{name: "whitespace in the middle rejected", in: "a b@example.com", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := user.NewEmail(tt.in)

			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.want, got.String())
		})
	}
}
