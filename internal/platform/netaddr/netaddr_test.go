package netaddr

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDialable(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		addr string
		want string
	}{
		{"a host-less address must get localhost filled in so it is dialable", ":9090", "localhost:9090"},
		{"0.0.0.0 binds but cannot be dialed, so it must become localhost", "0.0.0.0:9090", "localhost:9090"},
		{"the IPv6 all-interfaces form must be rewritten too", "[::]:9090", "localhost:9090"},
		{"an explicit host must be left alone", "grpc.internal:9090", "grpc.internal:9090"},
		{"127.0.0.1 is already dialable, so leave it as is", "127.0.0.1:19090", "127.0.0.1:19090"},
		{"a form whose port cannot be split out is passed through for the caller to decide", "unix:///tmp/grpc.sock", "unix:///tmp/grpc.sock"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.want, Dialable(tc.addr))
		})
	}
}

func TestLocalURL(t *testing.T) {
	t.Parallel()

	t.Run("must return a full URL so it can be clicked from a terminal", func(t *testing.T) {
		t.Parallel()
		require.Equal(t, "http://localhost:8080", LocalURL(":8080"))
	})
}
