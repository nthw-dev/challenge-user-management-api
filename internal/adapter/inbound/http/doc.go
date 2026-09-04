// Package httpapi is the inbound adapter for REST.
//
// This layer has exactly three jobs — decode the input, call the use case, encode the output.
// There is not one line of business rule in this package — name, email and password are validated in the domain, in one place,
// so REST and gRPC always reject the same input for the same reason.
package httpapi
