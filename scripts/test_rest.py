#!/usr/bin/env python3
"""Exercises every REST endpoint in the order they are really used, asserting each result.

Every request/response is recorded in full (headers + body) into a log file.
The file path is printed both at the start and at the end, so it can be reviewed
after a failing case.

    ./scripts/test_rest.py
    ./scripts/test_rest.py --base http://localhost:8080 --log-dir /tmp/api-log
    BASE=http://localhost:8080 LOG_DIR=/tmp/api-log ./scripts/test_rest.py

Exits 0 when every case passes and 1 when any case fails — so it drops straight into CI.
Only the standard library plus `rich` is needed: see scripts/requirements.txt.
"""

from __future__ import annotations

import argparse
import base64
import json
import os
import sys
import time
import urllib.error
import urllib.parse
import urllib.request
from dataclasses import dataclass, field
from datetime import datetime
from pathlib import Path
from typing import Any

sys.path.insert(0, str(Path(__file__).resolve().parent))
from testkit import Reporter, jget, pretty  # noqa: E402

ROOT = Path(__file__).resolve().parent.parent

# An id that is a well-formed ObjectId but has no row behind it — used to tell a 404 apart from a 422.
MISSING_ID = "ffffffffffffffffffffffff"


@dataclass
class Response:
    status: int
    body: str
    headers: list[tuple[str, str]] = field(default_factory=list)
    elapsed: float = 0.0

    @property
    def json(self) -> Any:
        try:
            return json.loads(self.body)
        except ValueError:
            return None

    def header(self, name: str) -> str:
        """The last value sent for a header, matched case-insensitively as HTTP requires."""
        found = [v for k, v in self.headers if k.lower() == name.lower()]
        return found[-1].strip() if found else ""


class Rest:
    """A thin curl-equivalent: it reports both the status and the body, because the
    cases that are meant to fail are as interesting as the ones that are meant to pass."""

    def __init__(self, base: str, reporter: Reporter, timeout: float = 30.0) -> None:
        self.base = base.rstrip("/")
        self.r = reporter
        self.timeout = timeout

    def call(
        self,
        method: str,
        path: str,
        body: str | dict | None = None,
        token: str | None = None,
        *,
        note: str = "",
        show_body: bool = True,
    ) -> Response:
        if isinstance(body, dict):
            body = json.dumps(body)
        payload = body.encode() if body is not None else None

        request = urllib.request.Request(self.base + path, data=payload, method=method)
        request.add_header("Accept", "application/json")
        if token:
            request.add_header("Authorization", f"Bearer {token}")
        if payload is not None:
            request.add_header("Content-Type", "application/json")

        started = time.perf_counter()
        try:
            with urllib.request.urlopen(request, timeout=self.timeout) as raw:
                response = Response(raw.status, raw.read().decode(errors="replace"), list(raw.headers.items()))
        except urllib.error.HTTPError as err:  # a 4xx/5xx is an answer, not a failure
            response = Response(err.code, err.read().decode(errors="replace"), list(err.headers.items()))
        except urllib.error.URLError as err:  # the connection itself failed
            response = Response(0, "", [], 0.0)
            self.r.log(f"connection error: {err}")
        response.elapsed = time.perf_counter() - started

        self.r.call(
            f"{method} {path}" + (f"  ({note})" if note else ""),
            request=body if show_body else None,
            meta=[f"Authorization: Bearer {_clip(token)}"] if token else [],
            status=f"HTTP {response.status}" if response.status else "no response",
            style=_status_style(response.status),
            elapsed=response.elapsed,
            body=response.body if show_body else None,
        )
        self.r.exchange(
            f"{method} {self.base}{path}",
            [
                ("Request header", f"Authorization: Bearer {token}" if token else None),
                ("Request body", body if show_body else "<not recorded>"),
                ("Response status", f"{response.status} ({response.elapsed:.3f}s)"),
                ("Response headers", "\n".join(f"{k}: {v}" for k, v in response.headers)),
                ("Response body", response.body or "<empty>"),
            ],
        )
        return response


def _clip(token: str, keep: int = 16) -> str:
    """Show only the head of a token on screen; the log file keeps it whole."""
    return token if len(token) <= keep else token[:keep] + "…"

def _status_style(status: int) -> str:
    if 200 <= status < 300:
        return "green"
    if 300 <= status < 400:
        return "cyan"
    if 400 <= status < 500:
        return "yellow"
    return "red"


def jwt_payload(token: str) -> str:
    """The middle segment of a JWT, which is base64url and readable by anyone holding it."""
    try:
        segment = token.split(".")[1]
        decoded = base64.urlsafe_b64decode(segment + "=" * (-len(segment) % 4))
        return pretty(decoded.decode())
    except Exception:  # noqa: BLE001 - a display nicety; never worth failing the run over
        return ""


def parse_args() -> argparse.Namespace:
    stamp = int(time.time())
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("--base", default=os.getenv("BASE", "http://localhost:8080"), help="API base URL")
    parser.add_argument("--email", default=os.getenv("EMAIL", f"tester+{stamp}@example.com"))
    parser.add_argument("--password", default=os.getenv("PASSWORD", "Str0ng-Passw0rd!"))
    parser.add_argument("--log-dir", default=os.getenv("LOG_DIR", str(ROOT / "scripts" / "logs")))
    parser.add_argument("--no-color", action="store_true", help="plain output, for CI logs")
    return parser.parse_args()


def main() -> int:
    args = parse_args()
    log_file = Path(args.log_dir) / f"rest-{datetime.now():%Y%m%d-%H%M%S}.log"

    r = Reporter(
        "REST API test",
        {"target": args.base, "email": args.email},
        log_file,
        color=False if args.no_color else None,
    )
    api = Rest(args.base, r)
    r.banner()

    # -----------------------------------------------------------------------
    r.step("1. Is the service ready to take work?")

    for _ in range(30):
        try:
            urllib.request.urlopen(args.base + "/readyz", timeout=2).read()
            break
        except Exception:  # noqa: BLE001 - still starting up
            time.sleep(1)

    res = api.call("GET", "/healthz")
    r.expect_value(res.status, 200, "liveness answers without touching MongoDB", what="HTTP")
    r.expect_field(res.json, "status", "ok", "healthz")

    res = api.call("GET", "/readyz")
    r.expect_value(res.status, 200, "readiness pings MongoDB successfully", what="HTTP")
    r.expect_field(res.json, "status", "ok", "readyz")

    if res.status == 0:
        r.console.print(f"[bold red]cannot reach {args.base} — is the server running?")
        return r.summary()

    # -----------------------------------------------------------------------
    r.step("2. A protected route requires a token")

    res = api.call("GET", "/api/v1/users")
    r.expect_value(res.status, 401, "calling /users with no token attached", what="HTTP")
    r.expect_field(res.json, "error.code", "UNAUTHORIZED", "the error code")

    res = api.call("GET", "/api/v1/users", token="not-a-real-jwt")
    r.expect_value(res.status, 401, "attaching a token made up on the spot", what="HTTP")
    r.expect_field(res.json, "error.code", "UNAUTHORIZED", "the error code")

    # -----------------------------------------------------------------------
    r.step(f"3. Sign up ({args.email})")

    signup = {"name": "Tester", "email": args.email, "password": args.password}

    res = api.call("POST", "/api/v1/auth/register", signup)
    r.expect_value(res.status, 201, "signed up successfully", what="HTTP")
    r.expect_field(res.json, "data.email", args.email, "the email stored")

    res = api.call("POST", "/api/v1/auth/register", signup)
    r.expect_value(res.status, 409, "a duplicate email is caught by the unique index, not by a pre-check", what="HTTP")
    r.expect_field(res.json, "error.code", "EMAIL_TAKEN", "the error code")

    res = api.call("POST", "/api/v1/auth/register", {"name": "", "email": "not-an-email", "password": "short"})
    r.expect_value(
        res.status, 422,
        "several bad fields; every one of them is reported at once, in the domain's order (the same as gRPC)",
        what="HTTP",
    )
    r.expect_field(res.json, "error.code", "VALIDATION_ERROR", "the error code")
    r.expect_value(len(jget(res.json, "error.details", []) or []), 3, "how many fields are reported back")
    for index, (want, label) in enumerate((("name", "the first field to fail validation"), ("email", "the second"), ("password", "the third"))):
        r.expect_field(res.json, f"error.details[{index}].field", want, label)

    res = api.call("POST", "/api/v1/auth/register", '{"name": not json}')
    r.expect_value(res.status, 400, "a body that is not JSON", what="HTTP")
    r.expect_field(res.json, "error.code", "MALFORMED_JSON", "the error code")

    # A body over the 1MB cap is refused before it is parsed — and the status says "too large",
    # not "malformed". The body itself is never echoed, so neither the terminal nor the log
    # has to carry a megabyte of padding.
    oversized = '{"name":"' + "x" * 1_100_000 + '"}'
    res = api.call("POST", "/api/v1/auth/register", oversized, note="a 1.1MB body, not shown", show_body=False)
    r.expect_value(res.status, 413, "a body over the 1MB cap", what="HTTP")
    r.expect_field(res.json, "error.code", "PAYLOAD_TOO_LARGE", "the error code")

    # -----------------------------------------------------------------------
    r.step("4. Log in to obtain a token")

    res = api.call("POST", "/api/v1/auth/login", {"email": args.email, "password": args.password})
    r.expect_value(res.status, 200, "logged in successfully", what="HTTP")
    r.expect_field(res.json, "data.token_type", "Bearer", "the token type")

    token = jget(res.json, "data.access_token", "")
    refresh = jget(res.json, "data.refresh_token", "")
    user_id = jget(res.json, "data.user.id", "")

    r.note("inside the access token (anyone holding it can read this — the signature is the part that cannot be forged)")
    r.body(jwt_payload(token))

    res = api.call("POST", "/api/v1/auth/login", {"email": args.email, "password": "wrong-password"})
    r.expect_value(res.status, 401, "wrong password", what="HTTP")
    r.expect_field(res.json, "error.code", "INVALID_CREDENTIALS", "the error code")

    res = api.call("POST", "/api/v1/auth/login", {"email": "no-such-person@example.com", "password": args.password})
    r.expect_value(res.status, 401, "a nonexistent email answers exactly as a wrong password does", what="HTTP")
    r.expect_field(res.json, "error.code", "INVALID_CREDENTIALS", "the error code")

    # -----------------------------------------------------------------------
    r.step("5. List users")

    res = api.call("GET", "/api/v1/users?limit=5", token=token)
    r.expect_value(res.status, 200, "reading the list with a token", what="HTTP")
    r.expect_field(res.json, "meta.limit", 5, "the limit actually used")

    res = api.call("GET", "/api/v1/users?limit=0", token=token)
    r.expect_value(res.status, 422, "limit=0 is not a positive integer", what="HTTP")
    r.expect_field(res.json, "error.details[0].field", "limit", "the offending field")

    res = api.call("GET", "/api/v1/users?limit=101", token=token)
    r.expect_value(res.status, 422, "limit is above the MaxListLimit cap", what="HTTP")
    r.expect_field(res.json, "error.details[0].field", "limit", "the offending field")

    query = urllib.parse.quote(args.email.split("@")[0], safe="")
    res = api.call("GET", f"/api/v1/users?query={query}", token=token)
    r.expect_value(res.status, 200, "searching by name or email", what="HTTP")
    matches = [u for u in (res.json or {}).get("data", []) if u.get("id") == user_id]
    r.expect(len(matches) == 1, "found the user who just signed up", f" → matched {len(matches)} rows, expected 1")

    # -----------------------------------------------------------------------
    r.step("6. Read a single user")

    res = api.call("GET", f"/api/v1/users/{user_id}", token=token)
    r.expect_value(res.status, 200, "reading by an id that exists", what="HTTP")
    r.expect_field(res.json, "data.id", user_id, "the id returned")

    res = api.call("GET", f"/api/v1/users/{MISSING_ID}", token=token)
    r.expect_value(res.status, 404, "a well-formed id with nothing behind it", what="HTTP")
    r.expect_field(res.json, "error.code", "USER_NOT_FOUND", "the error code")

    res = api.call("GET", "/api/v1/users/not-an-object-id", token=token)
    r.expect_value(res.status, 422, "a malformed id (the message does not reveal that it is an ObjectId)", what="HTTP")
    r.expect_field(res.json, "error.details[0].field", "id", "the offending field")

    # -----------------------------------------------------------------------
    r.step("7. Update a subset of fields with PATCH")

    res = api.call("PATCH", f"/api/v1/users/{user_id}", {"name": "Tester (updated)"}, token)
    r.expect_value(res.status, 200, "updating the name", what="HTTP")
    r.expect_field(res.json, "data.name", "Tester (updated)", "the new name")
    r.expect_field(res.json, "data.email", args.email, "a field that was not sent must be left untouched")

    res = api.call("PATCH", f"/api/v1/users/{user_id}", {"email": "not-an-email"}, token)
    r.expect_value(res.status, 422, "a malformed email", what="HTTP")
    r.expect_field(res.json, "error.code", "VALIDATION_ERROR", "the error code")

    # -----------------------------------------------------------------------
    r.step("8. Create a user with a token (unlike /auth/register, which is public)")

    second_email = f"second+{int(time.time())}@example.com"

    res = api.call("POST", "/api/v1/users", {"name": "Second User", "email": second_email, "password": args.password}, token)
    r.expect_value(res.status, 201, "created successfully", what="HTTP")
    second_id = jget(res.json, "data.id", "")
    r.expect_value(res.header("Location"), f"/api/v1/users/{second_id}", "the header points at what was just created", what="Location:")

    res = api.call("POST", "/api/v1/users", {"name": "Duplicate", "email": second_email, "password": args.password}, token)
    r.expect_value(res.status, 409, "duplicate email", what="HTTP")
    r.expect_field(res.json, "error.code", "EMAIL_TAKEN", "the error code")

    # -----------------------------------------------------------------------
    r.step("8b. A caller may only change their own account")

    res = api.call("PATCH", f"/api/v1/users/{second_id}", {"name": "Hijacked"}, token)
    r.expect_value(res.status, 403, "editing someone else's account with a valid token", what="HTTP")
    r.expect_field(res.json, "error.code", "FORBIDDEN", "the error code — not UNAUTHORIZED: the caller is known, the row is not theirs")

    res = api.call("DELETE", f"/api/v1/users/{second_id}", token=token)
    r.expect_value(res.status, 403, "deleting someone else's account", what="HTTP")
    r.expect_field(res.json, "error.code", "FORBIDDEN", "the error code")

    res = api.call("DELETE", f"/api/v1/users/{MISSING_ID}", token=token)
    r.expect_value(res.status, 403, "a row that does not exist answers the same — the 403 must not double as an existence check", what="HTTP")

    res = api.call("GET", f"/api/v1/users/{second_id}", token=token)
    r.expect_value(res.status, 200, "reads stay open to any authenticated caller", what="HTTP")
    r.expect_field(res.json, "data.name", "Second User", "and the bystander is unchanged")

    # -----------------------------------------------------------------------
    r.step("9. Paging by cursor rather than offset")

    res = api.call("GET", "/api/v1/users?limit=1", token=token)
    r.expect_value(res.status, 200, "the first page", what="HTTP")
    r.expect_field(res.json, "meta.has_more", True, "a next page still exists")
    first_id = jget(res.json, "data[0].id", "")
    cursor = urllib.parse.quote(jget(res.json, "meta.next_cursor", "") or "", safe="")

    res = api.call("GET", f"/api/v1/users?limit=1&cursor={cursor}", token=token)
    r.expect_value(res.status, 200, "the next page, via next_cursor from the previous one", what="HTTP")
    r.expect(jget(res.json, "data[0].id") != first_id, "the same row is not returned twice", f" → {first_id} came back again")

    # -----------------------------------------------------------------------
    r.step("10. Rotate the refresh token")

    res = api.call("POST", "/api/v1/auth/refresh", {"refresh_token": refresh})
    r.expect_value(res.status, 200, "rotating the old token into a new one", what="HTTP")
    new_refresh = jget(res.json, "data.refresh_token", "")
    r.expect(bool(new_refresh) and new_refresh != refresh, "got a new refresh token, different from the old one")

    res = api.call("POST", "/api/v1/auth/refresh", {"refresh_token": refresh})
    r.expect_value(res.status, 401, "presenting an already-rotated token again", what="HTTP")
    r.expect_field(res.json, "error.code", "UNAUTHORIZED", "the error code")

    res = api.call("POST", "/api/v1/auth/refresh", {"refresh_token": new_refresh})
    r.expect_value(res.status, 401, "the reuse wiped every session for that user, so even the token just issued no longer works", what="HTTP")

    # An access token is a JWT verified by signature; it is not stored in the database,
    # so the session wipe above does not invalidate one already held until it expires per JWT_ACCESS_TTL.
    # -----------------------------------------------------------------------
    r.step("11. Delete a user — as that user")

    res = api.call("POST", "/api/v1/auth/login", {"email": second_email, "password": args.password})
    r.expect_value(res.status, 200, "the second user logs in, because only they may delete their own account", what="HTTP")
    second_token = jget(res.json, "data.access_token", "")
    second_refresh = jget(res.json, "data.refresh_token", "")

    res = api.call("DELETE", f"/api/v1/users/{second_id}", token=second_token)
    r.expect_value(res.status, 204, "deleted; no response body", what="HTTP")

    res = api.call("DELETE", f"/api/v1/users/{second_id}", token=second_token)
    r.expect_value(res.status, 404, "deleting twice, because that row really is gone", what="HTTP")
    r.expect_field(res.json, "error.code", "USER_NOT_FOUND", "the error code")

    res = api.call("POST", "/api/v1/auth/refresh", {"refresh_token": second_refresh})
    r.expect_value(res.status, 401, "the delete revoked the user's refresh tokens with the row", what="HTTP")

    # -----------------------------------------------------------------------
    r.step("12. A nonexistent route answers with the same error envelope")

    res = api.call("GET", "/api/v1/no-such-route")
    r.expect_value(res.status, 404, "a route that does not exist", what="HTTP")
    r.expect_field(res.json, "error.code", "NOT_FOUND", "the error code")

    res = api.call("PUT", f"/api/v1/users/{user_id}", {}, token)
    r.expect_value(res.status, 405, "a method this route does not support", what="HTTP")
    r.expect_field(res.json, "error.code", "METHOD_NOT_ALLOWED", "the error code")

    return r.summary()


if __name__ == "__main__":
    try:
        sys.exit(main())
    except KeyboardInterrupt:
        sys.exit(130)
