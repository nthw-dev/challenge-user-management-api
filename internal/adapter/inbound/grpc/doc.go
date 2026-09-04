// Package grpcapi is the second inbound adapter, on top of the same core as REST.
//
// Every rule — duplicate emails, password hashing, the user invariants, the limit cap — lives in the domain/app layers
// and is shared with REST. This package holds only the protobuf conversions, in both directions,
// plus the mapping from the shared error vocabulary (apierr) onto gRPC status codes.
package grpcapi
