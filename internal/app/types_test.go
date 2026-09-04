package app_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nthw-dev/user-management-api/internal/app"
	"github.com/nthw-dev/user-management-api/internal/domain/user"
)

func TestListFilter_Resolve(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		in      *int
		want    int
		wantErr bool
	}{
		{name: "not sent uses the default", in: nil, want: app.DefaultListLimit},
		{name: "an explicitly sent zero must not pass", in: ptr(0), wantErr: true},
		{name: "a negative value must not pass", in: ptr(-1), wantErr: true},
		{name: "above the cap must not pass", in: ptr(app.MaxListLimit + 1), wantErr: true},
		{name: "the minimum passes", in: ptr(1), want: 1},
		{name: "exactly the cap passes", in: ptr(app.MaxListLimit), want: app.MaxListLimit},
		{name: "an ordinary value is used as sent", in: ptr(50), want: 50},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			q, err := app.ListFilter{Limit: tt.in, Cursor: "c", Query: "q"}.Resolve()

			if tt.wantErr {
				var invalid user.ErrValidation
				require.ErrorAs(t, err, &invalid)
				require.Equal(t, "limit", invalid.Field)
				return
			}
			require.NoError(t, err)
			require.Equal(t, app.ListQuery{Limit: tt.want, Cursor: "c", Query: "q"}, q)
		})
	}
}
