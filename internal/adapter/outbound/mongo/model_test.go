package mongostore

import (
	"reflect"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// A struct tag cannot reference a constant, so the two are written twice — this is what keeps them from drifting.
func TestFieldConstantsMatchTags(t *testing.T) {
	t.Parallel()

	tests := []struct {
		doc   any
		field string
		want  string
	}{
		{userDocument{}, "ID", fieldID},
		{userDocument{}, "Name", fieldName},
		{userDocument{}, "Email", fieldEmail},
		{userDocument{}, "PasswordHash", fieldPasswordHash},
		{userDocument{}, "CreatedAt", fieldCreatedAt},
		{userDocument{}, "UpdatedAt", fieldUpdatedAt},
		{refreshTokenDocument{}, "ID", fieldID},
		{refreshTokenDocument{}, "UserID", fieldUserID},
		{refreshTokenDocument{}, "TokenHash", fieldTokenHash},
		{refreshTokenDocument{}, "CreatedAt", fieldCreatedAt},
		{refreshTokenDocument{}, "ExpiresAt", fieldExpiresAt},
		{refreshTokenDocument{}, "RevokedAt", fieldRevokedAt},
	}

	for _, tt := range tests {
		typ := reflect.TypeOf(tt.doc)
		t.Run(typ.Name()+"."+tt.field, func(t *testing.T) {
			t.Parallel()

			f, ok := typ.FieldByName(tt.field)
			require.True(t, ok)

			tag, _, _ := strings.Cut(f.Tag.Get("bson"), ",")
			require.Equal(t, tt.want, tag)
		})
	}
}
