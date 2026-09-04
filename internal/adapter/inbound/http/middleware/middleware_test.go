package middleware_test

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/suite"

	"github.com/nthw-dev/user-management-api/internal/adapter/inbound/actor"
	"github.com/nthw-dev/user-management-api/internal/adapter/inbound/http/middleware"
	"github.com/nthw-dev/user-management-api/internal/adapter/inbound/http/reqctx"
	"github.com/nthw-dev/user-management-api/internal/app/apptest"
	"github.com/nthw-dev/user-management-api/internal/domain/user"
)

// MiddlewareSuite tests every middleware on its own, outside the router: each one is a plain func(http.Handler) http.Handler,
// so it can be wrapped around a one-line handler and asked exactly one question. The suite gives every test a fresh
// JSON log buffer, because half of what a middleware does is what it writes to the log.
type MiddlewareSuite struct {
	suite.Suite
	logs *bytes.Buffer
	log  *slog.Logger
}

func TestMiddleware(t *testing.T) {
	suite.Run(t, new(MiddlewareSuite))
}

func (s *MiddlewareSuite) SetupTest() {
	s.logs = &bytes.Buffer{}
	s.log = slog.New(slog.NewJSONHandler(s.logs, nil))
}

// logEntries decodes every line written to the log so far.
func (s *MiddlewareSuite) logEntries() []map[string]any {
	var entries []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(s.logs.String()), "\n") {
		if line == "" {
			continue
		}
		var entry map[string]any
		s.Require().NoError(json.Unmarshal([]byte(line), &entry))
		entries = append(entries, entry)
	}
	return entries
}

// logEntry finds the most recent line with the given msg — the buffer lives for the whole test method, subtests included.
func (s *MiddlewareSuite) logEntry(msg string) map[string]any {
	entries := s.logEntries()
	for i := len(entries) - 1; i >= 0; i-- {
		if entries[i]["msg"] == msg {
			return entries[i]
		}
	}
	s.Require().Failf("log line not found", "no line with msg=%q in:\n%s", msg, s.logs.String())
	return nil
}

func serve(h http.Handler, req *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func ok(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }

// ---- RequestID ----

func (s *MiddlewareSuite) TestRequestID() {
	echo := func(req *http.Request) (header, inContext string) {
		h := middleware.RequestID(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			inContext = reqctx.RequestID(r.Context())
		}))
		return serve(h, req).Header().Get(middleware.RequestIDHeader), inContext
	}

	s.Run("a well-formed inbound id is kept, echoed back, and put in the context", func() {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set(middleware.RequestIDHeader, "trace-ABC_123")

		header, inContext := echo(req)

		s.Equal("trace-ABC_123", header)
		s.Equal("trace-ABC_123", inContext)
	})

	s.Run("a missing id gets a ULID, the same one in the header and in the context", func() {
		header, inContext := echo(httptest.NewRequest(http.MethodGet, "/", nil))

		s.Len(header, 26)
		s.Equal(header, inContext)
	})

	s.Run("an id of exactly the maximum length is kept", func() {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set(middleware.RequestIDHeader, strings.Repeat("a", 64))

		header, _ := echo(req)

		s.Equal(strings.Repeat("a", 64), header)
	})

	for name, bad := range map[string]string{
		"a character that is unsafe in a log line": "bad id\n",
		"a value longer than the maximum":          strings.Repeat("a", 65),
		"a value with a colon":                     "abc:def",
	} {
		s.Run("is replaced: "+name, func() {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.Header.Set(middleware.RequestIDHeader, bad)

			header, inContext := echo(req)

			s.NotEqual(bad, header)
			s.Len(header, 26)
			s.Equal(header, inContext)
		})
	}
}

// ---- RealIP ----

func (s *MiddlewareSuite) TestRealIP_PrefersTheProxyHeadersInOrder() {
	tests := []struct {
		name, xff, xri, remote, want string
	}{
		{"the first X-Forwarded-For entry is the client", "1.1.1.1, 2.2.2.2", "9.9.9.9", "5.5.5.5:1234", "1.1.1.1"},
		{"a single X-Forwarded-For value is trimmed", "  3.3.3.3  ", "", "5.5.5.5:1234", "3.3.3.3"},
		{"an empty first entry falls through to X-Real-IP", ", 2.2.2.2", "4.4.4.4", "5.5.5.5:1234", "4.4.4.4"},
		{"X-Real-IP when there is no X-Forwarded-For", "", " 4.4.4.4 ", "5.5.5.5:1234", "4.4.4.4"},
		{"RemoteAddr without its port when there is no header", "", "", "5.5.5.5:1234", "5.5.5.5"},
		{"RemoteAddr as it is when it has no port", "", "", "unix-socket", "unix-socket"},
	}

	for _, tt := range tests {
		s.Run(tt.name, func() {
			var got string
			h := middleware.RealIP(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
				got = reqctx.RealIP(r.Context())
			}))
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.RemoteAddr = tt.remote
			if tt.xff != "" {
				req.Header.Set("X-Forwarded-For", tt.xff)
			}
			if tt.xri != "" {
				req.Header.Set("X-Real-IP", tt.xri)
			}

			serve(h, req)

			s.Equal(tt.want, got)
		})
	}
}

// ---- Logging ----

func (s *MiddlewareSuite) TestLogging_WritesOneLinePerRequestOnceEverythingIsKnown() {
	h := middleware.RequestID(middleware.RealIP(middleware.Logging(s.log)(
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte("hello"))
		}))))
	req := httptest.NewRequest(http.MethodPost, "/api/v1/users?token=do-not-log-me", nil)
	req.Header.Set(middleware.RequestIDHeader, "req-1")
	req.RemoteAddr = "10.0.0.1:5555"

	rec := serve(h, req)

	s.Equal(http.StatusCreated, rec.Code)
	entry := s.logEntry("http_request")
	s.Equal("POST", entry["method"])
	s.Equal("/api/v1/users", entry["path"], "the query string must not leak into the log")
	s.EqualValues(201, entry["status"])
	s.EqualValues(5, entry["bytes"])
	s.Equal("req-1", entry["request_id"])
	s.Equal("10.0.0.1", entry["remote_ip"])
	s.Contains(entry, "duration_ms")
	s.NotContains(s.logs.String(), "do-not-log-me")
}

func (s *MiddlewareSuite) TestLogging_StatusDefaultsTo200AndTheFirstWriteHeaderWins() {
	s.Run("a handler that only writes a body is logged as 200", func() {
		h := middleware.Logging(s.log)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte("implicit"))
		}))

		rec := serve(h, httptest.NewRequest(http.MethodGet, "/", nil))

		s.Equal(http.StatusOK, rec.Code)
		s.EqualValues(200, s.logEntry("http_request")["status"])
	})

	s.Run("a second WriteHeader changes nothing, in the response or in the log", func() {
		h := middleware.Logging(s.log)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNotFound)
			w.WriteHeader(http.StatusInternalServerError)
		}))

		rec := serve(h, httptest.NewRequest(http.MethodGet, "/", nil))

		s.Equal(http.StatusNotFound, rec.Code)
		s.EqualValues(404, s.logEntry("http_request")["status"])
	})
}

// deadlineRecorder is a ResponseWriter with one optional capability, to prove the wrapper lets it through via Unwrap.
type deadlineRecorder struct {
	*httptest.ResponseRecorder
	deadline time.Time
}

func (d *deadlineRecorder) SetWriteDeadline(t time.Time) error {
	d.deadline = t
	return nil
}

// The classic mistake of a middleware that wraps a ResponseWriter is to hide what the writer underneath can do.
func (s *MiddlewareSuite) TestLogging_KeepsFlushAndUnwrapWorking() {
	s.Run("Flush reaches the writer underneath", func() {
		h := middleware.Logging(s.log)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			f, ok := w.(http.Flusher)
			s.Require().True(ok, "the wrapper must still be a Flusher")
			f.Flush()
		}))

		rec := serve(h, httptest.NewRequest(http.MethodGet, "/", nil))

		s.True(rec.Flushed)
	})

	s.Run("net/http's ResponseController finds the underlying writer through Unwrap", func() {
		when := time.Now().Add(time.Minute)
		h := middleware.Logging(s.log)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			s.Require().NoError(http.NewResponseController(w).SetWriteDeadline(when))
		}))
		inner := &deadlineRecorder{ResponseRecorder: httptest.NewRecorder()}

		h.ServeHTTP(inner, httptest.NewRequest(http.MethodGet, "/", nil))

		s.Equal(when, inner.deadline)
	})
}

// ---- Metrics ----

func (s *MiddlewareSuite) TestMetrics_LabelsByRoutePatternNotByPath() {
	reg := prometheus.NewRegistry()
	r := chi.NewRouter()
	r.Use(middleware.Metrics(reg))
	r.Get("/users/{id}", ok)

	for _, path := range []string{"/users/1", "/users/2", "/nowhere"} {
		serve(r, httptest.NewRequest(http.MethodGet, path, nil))
	}

	requests := counterValues(s, reg, "http_requests_total")
	s.Equal(2.0, requests["GET|/users/{id}|204"], "two ids, one series — the id must not become a label value")
	s.Equal(1.0, requests["GET|unmatched|404"])
	for series := range requests {
		s.NotContains(series, "/users/1")
	}
	s.EqualValues(2, histogramCount(s, reg, "http_request_duration_seconds", "/users/{id}"))
}

// Registering the same metrics twice on one registry is a wiring bug, and it must surface at boot rather than be swallowed.
func (s *MiddlewareSuite) TestMetrics_RefusesToRegisterTwice() {
	reg := prometheus.NewRegistry()
	middleware.Metrics(reg)

	s.Panics(func() { middleware.Metrics(reg) })
}

// counterValues reads a counter family off the registry as "method|route|status" → value.
func counterValues(s *MiddlewareSuite, reg *prometheus.Registry, family string) map[string]float64 {
	families, err := reg.Gather()
	s.Require().NoError(err)

	out := map[string]float64{}
	for _, f := range families {
		if f.GetName() != family {
			continue
		}
		for _, m := range f.GetMetric() {
			labels := map[string]string{}
			for _, l := range m.GetLabel() {
				labels[l.GetName()] = l.GetValue()
			}
			out[labels["method"]+"|"+labels["route"]+"|"+labels["status"]] = m.GetCounter().GetValue()
		}
	}
	return out
}

func histogramCount(s *MiddlewareSuite, reg *prometheus.Registry, family, route string) uint64 {
	families, err := reg.Gather()
	s.Require().NoError(err)

	for _, f := range families {
		if f.GetName() != family {
			continue
		}
		for _, m := range f.GetMetric() {
			for _, l := range m.GetLabel() {
				if l.GetName() == "route" && l.GetValue() == route {
					return m.GetHistogram().GetSampleCount()
				}
			}
		}
	}
	return 0
}

// ---- Recoverer ----

func (s *MiddlewareSuite) TestRecoverer_TurnsAPanicIntoA500AndLogsTheStack() {
	h := middleware.RequestID(middleware.Recoverer(s.log)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("something went badly wrong")
	})))

	rec := serve(h, httptest.NewRequest(http.MethodGet, "/api/v1/users", nil))

	s.Equal(http.StatusInternalServerError, rec.Code)
	s.Equal("application/json; charset=utf-8", rec.Header().Get("Content-Type"))
	s.Contains(rec.Body.String(), `"code":"INTERNAL"`)
	s.NotContains(rec.Body.String(), "badly wrong", "the panic's text must not reach the caller")

	entry := s.logEntry("panic_recovered")
	s.Equal("something went badly wrong", entry["panic"])
	s.Equal("/api/v1/users", entry["path"])
	s.Contains(entry["stack"], "middleware_test", "the stack must point at where the panic came from")
	s.NotEmpty(entry["request_id"])
}

// http.ErrAbortHandler is how a handler says "the client is gone" — net/http handles it, and we must not eat it.
func (s *MiddlewareSuite) TestRecoverer_LetsAnAbortedConnectionPropagate() {
	h := middleware.Recoverer(s.log)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic(http.ErrAbortHandler)
	}))

	s.PanicsWithValue(http.ErrAbortHandler, func() { serve(h, httptest.NewRequest(http.MethodGet, "/", nil)) })
	s.Empty(s.logs.String(), "not a failure of ours, so nothing to log")
}

// ---- MaxBytes ----

func (s *MiddlewareSuite) TestMaxBytes() {
	const limit = 8

	readAll := func(req *http.Request) (n int, err error) {
		h := middleware.MaxBytes(limit)(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			if r.Body == nil {
				return
			}
			var b []byte
			b, err = io.ReadAll(r.Body)
			n = len(b)
		}))
		serve(h, req)
		return n, err
	}

	s.Run("a body within the limit is read whole", func() {
		n, err := readAll(httptest.NewRequest(http.MethodPost, "/", strings.NewReader("12345678")))

		s.NoError(err)
		s.Equal(limit, n)
	})

	s.Run("a body over the limit fails the read with the error the decoder translates into a 413", func() {
		_, err := readAll(httptest.NewRequest(http.MethodPost, "/", strings.NewReader("123456789")))

		var tooBig *http.MaxBytesError
		s.ErrorAs(err, &tooBig)
		s.EqualValues(limit, tooBig.Limit)
	})

	s.Run("a request with no body at all is passed through untouched", func() {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Body = nil

		s.NotPanics(func() { _, _ = readAll(req) })
	})
}

// ---- Authenticate ----

func (s *MiddlewareSuite) TestAuthenticate() {
	tests := []struct {
		name     string
		header   string
		verifier apptest.Verifier
		want     int
	}{
		{"no header at all", "", apptest.Verifier{}, http.StatusUnauthorized},
		{"the wrong scheme", "Basic dXNlcjpwYXNz", apptest.Verifier{}, http.StatusUnauthorized},
		{"the scheme with no token", "Bearer ", apptest.Verifier{}, http.StatusUnauthorized},
		{"a token the verifier rejects", "Bearer bad", apptest.Verifier{Err: user.ErrUnauthorized}, http.StatusUnauthorized},
		{"a token the verifier accepts", "Bearer good", apptest.Verifier{}, http.StatusNoContent},
	}

	for _, tt := range tests {
		s.Run(tt.name, func() {
			h := middleware.Authenticate(tt.verifier, s.log)(http.HandlerFunc(ok))
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			if tt.header != "" {
				req.Header.Set("Authorization", tt.header)
			}

			rec := serve(h, req)

			s.Equal(tt.want, rec.Code)
			if tt.want == http.StatusUnauthorized {
				s.Equal(`Bearer realm="user-service"`, rec.Header().Get("WWW-Authenticate"))
				s.Contains(rec.Body.String(), `"code":"UNAUTHORIZED"`)
				s.NotContains(rec.Body.String(), "signature", "why the token failed must not be revealed")
			}
		})
	}

	s.Run("the verified subject reaches the handler as the actor", func() {
		var seen string
		h := middleware.Authenticate(apptest.Verifier{}, s.log)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			seen = actor.ID(r.Context())
			w.WriteHeader(http.StatusNoContent)
		}))
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("Authorization", "Bearer good")

		serve(h, req)

		s.Equal(apptest.SeededID, seen)
	})
}

// The request line is written by Logging, which sits OUTSIDE Authenticate — a context value set further in would be
// invisible to it. The slot Logging reserves is what carries the actor back out.
func (s *MiddlewareSuite) TestLogging_PrintsTheActorWhenAuthenticateRanInsideIt() {
	s.Run("an authenticated request carries actor_id", func() {
		h := middleware.Logging(s.log)(middleware.Authenticate(apptest.Verifier{}, s.log)(http.HandlerFunc(ok)))
		req := httptest.NewRequest(http.MethodGet, "/api/v1/users", nil)
		req.Header.Set("Authorization", "Bearer good")

		serve(h, req)

		s.Equal(apptest.SeededID, s.logEntry("http_request")["actor_id"])
	})

	s.Run("a public route's line has no actor_id key at all", func() {
		h := middleware.Logging(s.log)(http.HandlerFunc(ok))

		serve(h, httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", nil))

		s.NotContains(s.logEntry("http_request"), "actor_id")
	})

	s.Run("a rejected token leaves no actor on the line", func() {
		h := middleware.Logging(s.log)(middleware.Authenticate(apptest.Verifier{Err: user.ErrUnauthorized}, s.log)(http.HandlerFunc(ok)))
		req := httptest.NewRequest(http.MethodGet, "/api/v1/users", nil)
		req.Header.Set("Authorization", "Bearer bad")

		serve(h, req)

		entry := s.logEntry("http_request")
		s.EqualValues(401, entry["status"])
		s.NotContains(entry, "actor_id")
	})
}
