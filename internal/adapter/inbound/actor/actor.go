// Package actor carries the authenticated caller's id through a request, on either transport.
//
// The auth layer of each transport — Authenticate on REST, authUnary on gRPC — verifies the token and Sets the id;
// a handler reads it with ID and hands it to the use case as an explicit argument. The core never reads a context
// value: who is calling is part of the use case's signature, not something fished out of the request on the way.
//
// The one wrinkle is logging. The request log line is written by the outermost layer, and a context value set by an
// inner layer is invisible to it — context values only flow downward. So the logging layer Reserves a slot first,
// and Set fills the slot as well as the context; ID reads whichever is there.
package actor

import (
	"context"
	"sync"
)

type (
	slotKey struct{}
	idKey   struct{}
)

// slot is what the outermost layer reserves so an inner layer's Set can reach it.
type slot struct {
	mu sync.Mutex
	id string
}

// Reserve makes room for an id that a layer further in will set — for the logging layer, so the id shows up in its line.
func Reserve(ctx context.Context) context.Context {
	return context.WithValue(ctx, slotKey{}, &slot{})
}

// Set records the verified caller: in the context for everything downstream, and in the reserved slot, if any, for the layer that reserved it.
func Set(ctx context.Context, id string) context.Context {
	if s, ok := ctx.Value(slotKey{}).(*slot); ok {
		s.mu.Lock()
		s.id = id
		s.mu.Unlock()
	}
	return context.WithValue(ctx, idKey{}, id)
}

// ID returns the verified caller's id, or "" when no authentication layer has run — on a public route, for instance.
func ID(ctx context.Context) string {
	if id, ok := ctx.Value(idKey{}).(string); ok && id != "" {
		return id
	}
	if s, ok := ctx.Value(slotKey{}).(*slot); ok {
		s.mu.Lock()
		defer s.mu.Unlock()
		return s.id
	}
	return ""
}
