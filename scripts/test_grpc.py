#!/usr/bin/env python3
"""Exercises every gRPC rpc in the order they are really used, asserting each result.

Calls go through grpcurl, which reads the contract over reflection — so no .proto file
has to be pointed at. It does require APP_ENV=development, though, because reflection
is off in production.

Every request/response is recorded in full (metadata + body + status) into a log file.
The file path is printed both at the start and at the end.

    ./scripts/test_grpc.py
    ./scripts/test_grpc.py --grpc localhost:9090 --log-dir /tmp/api-log
    GRPC=localhost:9090 LOG_DIR=/tmp/api-log ./scripts/test_grpc.py

Exits 0 when every case passes and 1 when any case fails.
Needs the grpcurl binary (make tools-install) and `rich`: see scripts/requirements.txt.
"""

from __future__ import annotations

import argparse
import json
import os
import re
import shutil
import subprocess
import sys
import time
from dataclasses import dataclass
from datetime import datetime
from pathlib import Path
from typing import Any, Sequence

sys.path.insert(0, str(Path(__file__).resolve().parent))
from testkit import Reporter, jget  # noqa: E402

ROOT = Path(__file__).resolve().parent.parent

# An id that is a well-formed ObjectId but has no row behind it — used to tell a 404 apart from a 422.
MISSING_ID = "ffffffffffffffffffffffff"

_CODE = re.compile(r"^\s*Code:\s*(.+)$", re.MULTILINE)
_MESSAGE = re.compile(r"^\s*Message:\s*(.+)$", re.MULTILINE)


@dataclass
class Result:
    code: str
    body: str
    message: str = ""
    errtext: str = ""
    elapsed: float = 0.0

    @property
    def json(self) -> Any:
        try:
            return json.loads(self.body)
        except ValueError:
            return None


class Grpc:
    """grpcurl behind a small façade — the status name, the body and the status details."""

    def __init__(self, target: str, reporter: Reporter, timeout: float = 30.0) -> None:
        self.target = target
        self.r = reporter
        self.timeout = timeout

    def list_services(self) -> list[str]:
        started = time.perf_counter()
        proc = self._run(["grpcurl", "-plaintext", self.target, "list"])
        services = [line.strip() for line in proc.stdout.splitlines() if line.strip()]
        self.r.call(
            "list (over reflection)",
            status="OK" if services else "NO RESPONSE",
            style="green" if services else "red",
            elapsed=time.perf_counter() - started,
            body="\n".join(services),
            error=None if services else proc.stderr,
        )
        self.r.exchange(f"{self.target} list", [("Services", "\n".join(services) or "<none>")])
        return services

    def rpc(self, method: str, data: dict | str | None = None, token: str | None = None) -> Result:
        payload = json.dumps(data) if isinstance(data, dict) else (data or "{}")

        # gRPC has no Authorization header, only metadata, which serves the same purpose.
        # The name must be lowercase, and the value ASCII, as HTTP/2 requires.
        argv = ["grpcurl", "-plaintext", "-emit-defaults", "-d", payload]
        if token:
            argv += ["-H", f"authorization: Bearer {token}"]
        argv += [self.target, method]

        started = time.perf_counter()
        proc = self._run(argv)
        elapsed = time.perf_counter() - started

        if proc.returncode == 0:
            result = Result("OK", proc.stdout, "", proc.stderr, elapsed)
        else:
            code = _CODE.search(proc.stderr)
            message = _MESSAGE.search(proc.stderr)
            # On a failed connection, or when grpcurl itself breaks, there is no Code: line to read.
            result = Result(
                code.group(1).strip() if code else "NO_RESPONSE",
                proc.stdout,
                message.group(1).strip() if message else "",
                proc.stderr,
                elapsed,
            )

        self.r.call(
            method,
            request=payload,
            meta=[f"metadata: authorization: Bearer {_clip(token)}"] if token else [],
            status=result.code,
            style=_code_style(result.code),
            elapsed=elapsed,
            body=result.body if result.code == "OK" else None,
            error=None if result.code == "OK" else (result.message or result.errtext),
        )
        self.r.exchange(
            f"{self.target} {method}",
            [
                ("Request metadata", f"authorization: Bearer {token}" if token else None),
                ("Request body", payload),
                ("Response status", f"{result.code} ({elapsed:.3f}s)"),
                ("Response error", result.errtext.strip() or None),
                ("Response body", result.body or "<empty>"),
            ],
        )
        return result

    def _run(self, argv: Sequence[str]) -> subprocess.CompletedProcess[str]:
        try:
            return subprocess.run(argv, capture_output=True, text=True, timeout=self.timeout)
        except subprocess.TimeoutExpired:
            return subprocess.CompletedProcess(argv, 1, "", f"timed out after {self.timeout}s")


def _clip(token: str, keep: int = 16) -> str:
    """Show only the head of a token on screen; the log file keeps it whole."""
    return token if len(token) <= keep else token[:keep] + "…"

def _code_style(code: str) -> str:
    if code == "OK":
        return "green"
    return "red" if code == "NO_RESPONSE" else "yellow"


def expect_detail(r: Reporter, result: Result, field: str, value: str, label: str) -> bool:
    """A key/value pair inside the status details (ErrorInfo, BadRequest) —
    the gRPC twin of error.code / error.details[] on the REST side."""
    found = re.search(rf'"{re.escape(field)}"\s*:\s*"{re.escape(value)}"', result.errtext)
    return r.expect(bool(found), f"{label} → {field} = {value}", f" — not found in the status details")


def parse_args() -> argparse.Namespace:
    stamp = int(time.time())
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("--grpc", default=os.getenv("GRPC", "localhost:9090"), help="host:port of the gRPC server")
    parser.add_argument("--email", default=os.getenv("EMAIL", f"grpc-tester+{stamp}@example.com"))
    parser.add_argument("--password", default=os.getenv("PASSWORD", "Str0ng-Passw0rd!"))
    parser.add_argument("--log-dir", default=os.getenv("LOG_DIR", str(ROOT / "scripts" / "logs")))
    parser.add_argument("--no-color", action="store_true", help="plain output, for CI logs")
    return parser.parse_args()


def main() -> int:
    args = parse_args()
    if shutil.which("grpcurl") is None:
        raise SystemExit("grpcurl must be installed first (make tools-install)")

    log_file = Path(args.log_dir) / f"grpc-{datetime.now():%Y%m%d-%H%M%S}.log"
    r = Reporter(
        "gRPC test",
        {"target": args.grpc, "email": args.email},
        log_file,
        color=False if args.no_color else None,
    )
    api = Grpc(args.grpc, r)
    r.banner()

    # -----------------------------------------------------------------------
    r.step("1. Is the service ready to take work?")

    for _ in range(30):
        probe = subprocess.run(["grpcurl", "-plaintext", args.grpc, "list"], capture_output=True, text=True)
        if probe.returncode == 0:
            break
        time.sleep(1)

    services = api.list_services()
    if not services:
        r.console.print(
            f"[bold red]cannot reach {args.grpc}, or reflection is off (APP_ENV=development is required)"
        )
        return r.summary()

    for service in ("user.v1.AuthService", "user.v1.UserService", "grpc.health.v1.Health"):
        r.expect(service in services, f"reflection advertises {service}", " — it does not")

    # The health check is in publicPrefixes like AuthService, so it is callable with no token.
    res = api.rpc("grpc.health.v1.Health/Check")
    r.expect_value(res.code, "OK", "health check with no token attached")
    r.expect_field(res.json, "status", "SERVING", "the status")

    # -----------------------------------------------------------------------
    r.step("2. UserService requires a token; AuthService does not")

    res = api.rpc("user.v1.UserService/ListUsers", {"limit": 5})
    r.expect_value(res.code, "Unauthenticated", "calling ListUsers with no metadata attached")
    expect_detail(r, res, "reason", "UNAUTHORIZED", "the shared error code, the same string REST puts in error.code")

    # A metadata value must be ASCII, as HTTP/2 requires — so the fake token has to be ASCII too.
    res = api.rpc("user.v1.UserService/ListUsers", {"limit": 5}, "not-a-real-jwt")
    r.expect_value(res.code, "Unauthenticated", "attaching a token made up on the spot")

    # -----------------------------------------------------------------------
    r.step(f"3. Sign up ({args.email})")

    signup = {"name": "Tester", "email": args.email, "password": args.password}

    res = api.rpc("user.v1.AuthService/Register", signup)
    r.expect_value(res.code, "OK", "signed up successfully, with no token needed")
    r.expect_field(res.json, "user.email", args.email, "the email stored")

    res = api.rpc("user.v1.AuthService/Register", signup)
    r.expect_value(res.code, "AlreadyExists", "a duplicate email is caught by the unique index, not by a pre-check")

    res = api.rpc("user.v1.AuthService/Register", {"name": "Tester", "email": "not-an-email", "password": args.password})
    r.expect_value(res.code, "InvalidArgument", "a malformed email")

    res = api.rpc("user.v1.AuthService/Register", {"name": "", "email": "not-an-email", "password": "short"})
    r.expect_value(
        res.code, "InvalidArgument",
        "several bad fields; every one of them is reported at once, in the domain's order (the same as REST)",
    )
    r.expect_value(res.message, "the data sent is invalid", "the same message REST gives")
    expect_detail(r, res, "reason", "VALIDATION_ERROR", "the shared error code, as ErrorInfo")
    expect_detail(r, res, "field", "name", "the first field to fail validation, as BadRequest")
    expect_detail(r, res, "field", "email", "the second")
    expect_detail(r, res, "field", "password", "the third")

    # -----------------------------------------------------------------------
    r.step("4. Log in to obtain a token")

    res = api.rpc("user.v1.AuthService/Login", {"email": args.email, "password": args.password})
    r.expect_value(res.code, "OK", "logged in successfully")
    r.expect_field(res.json, "session.token_type|session.tokenType", "Bearer", "the token type")

    token = jget(res.json, "session.access_token|session.accessToken", "")
    refresh = jget(res.json, "session.refresh_token|session.refreshToken", "")
    user_id = jget(res.json, "session.user.id", "")

    res = api.rpc("user.v1.AuthService/Login", {"email": args.email, "password": "wrong-password"})
    r.expect_value(res.code, "Unauthenticated", "wrong password")
    expect_detail(r, res, "reason", "INVALID_CREDENTIALS", "told apart from a missing token by the shared code")

    res = api.rpc("user.v1.AuthService/Login", {"email": "no-such-person@example.com", "password": args.password})
    r.expect_value(res.code, "Unauthenticated", "a nonexistent email answers exactly as a wrong password does")

    # -----------------------------------------------------------------------
    r.step("5. List users")

    res = api.rpc("user.v1.UserService/ListUsers", {"limit": 5}, token)
    r.expect_value(res.code, "OK", "reading the list with a token")
    r.expect_field(res.json, "meta.limit", 5, "the limit actually used")

    # limit is optional in the contract — so sending 0 means "deliberately sent 0", not "not sent".
    res = api.rpc("user.v1.UserService/ListUsers", {"limit": 0}, token)
    r.expect_value(res.code, "InvalidArgument", "limit=0 is not a positive integer")

    res = api.rpc("user.v1.UserService/ListUsers", {"limit": 101}, token)
    r.expect_value(res.code, "InvalidArgument", "limit is above the MaxListLimit cap")

    res = api.rpc("user.v1.UserService/ListUsers", {"query": args.email.split("@")[0]}, token)
    r.expect_value(res.code, "OK", "searching by name or email")
    matches = [u for u in ((res.json or {}).get("users") or []) if u.get("id") == user_id]
    r.expect(len(matches) == 1, "found the user who just signed up", f" → matched {len(matches)} rows, expected 1")

    # -----------------------------------------------------------------------
    r.step("6. Read a single user")

    res = api.rpc("user.v1.UserService/GetUser", {"id": user_id}, token)
    r.expect_value(res.code, "OK", "reading by an id that exists")
    r.expect_field(res.json, "user.id", user_id, "the id returned")

    res = api.rpc("user.v1.UserService/GetUser", {"id": MISSING_ID}, token)
    r.expect_value(res.code, "NotFound", "a well-formed id with nothing behind it")

    res = api.rpc("user.v1.UserService/GetUser", {"id": "not-an-object-id"}, token)
    r.expect_value(res.code, "InvalidArgument", "a malformed id (the message does not reveal that it is an ObjectId)")

    # -----------------------------------------------------------------------
    r.step("7. Update a subset of fields (optional in the contract is what makes PATCH survive the trip)")

    res = api.rpc("user.v1.UserService/UpdateUser", {"id": user_id, "name": "Tester (updated)"}, token)
    r.expect_value(res.code, "OK", "updating the name")
    r.expect_field(res.json, "user.name", "Tester (updated)", "the new name")
    r.expect_field(res.json, "user.email", args.email, "a field that was not sent must be left untouched")

    res = api.rpc("user.v1.UserService/UpdateUser", {"id": user_id, "email": "not-an-email"}, token)
    r.expect_value(res.code, "InvalidArgument", "a malformed email")

    # -----------------------------------------------------------------------
    r.step("8. Create a user (equivalent to POST /api/v1/users on the REST side)")

    second_email = f"grpc-second+{int(time.time())}@example.com"

    res = api.rpc("user.v1.UserService/CreateUser", {"name": "Second User", "email": second_email, "password": args.password}, token)
    r.expect_value(res.code, "OK", "created successfully")
    second_id = jget(res.json, "user.id", "")
    r.expect_field(res.json, "user.email", second_email, "the email stored")

    res = api.rpc("user.v1.UserService/CreateUser", {"name": "Duplicate", "email": second_email, "password": args.password}, token)
    r.expect_value(res.code, "AlreadyExists", "duplicate email")

    # -----------------------------------------------------------------------
    r.step("8b. A caller may only change their own account")

    res = api.rpc("user.v1.UserService/UpdateUser", {"id": second_id, "name": "Hijacked"}, token)
    r.expect_value(res.code, "PermissionDenied", "editing someone else's account with a valid token")
    expect_detail(r, res, "reason", "FORBIDDEN", "the shared error code — not UNAUTHORIZED: the caller is known, the row is not theirs")

    res = api.rpc("user.v1.UserService/DeleteUser", {"id": second_id}, token)
    r.expect_value(res.code, "PermissionDenied", "deleting someone else's account")

    res = api.rpc("user.v1.UserService/DeleteUser", {"id": MISSING_ID}, token)
    r.expect_value(res.code, "PermissionDenied", "a row that does not exist answers the same — the refusal must not double as an existence check")

    res = api.rpc("user.v1.UserService/GetUser", {"id": second_id}, token)
    r.expect_value(res.code, "OK", "reads stay open to any authenticated caller")
    r.expect_field(res.json, "user.name", "Second User", "and the bystander is unchanged")

    # -----------------------------------------------------------------------
    r.step("9. Paging by cursor rather than offset")

    res = api.rpc("user.v1.UserService/ListUsers", {"limit": 1}, token)
    r.expect_value(res.code, "OK", "the first page")
    r.expect_field(res.json, "meta.has_more|meta.hasMore", True, "a next page still exists")
    first_id = jget(res.json, "users[0].id", "")
    cursor = jget(res.json, "meta.next_cursor|meta.nextCursor", "")

    res = api.rpc("user.v1.UserService/ListUsers", {"limit": 1, "cursor": cursor}, token)
    r.expect_value(res.code, "OK", "the next page, via next_cursor from the previous one")
    r.expect(jget(res.json, "users[0].id") != first_id, "the same row is not returned twice", f" → {first_id} came back again")

    # -----------------------------------------------------------------------
    r.step("10. Rotate the refresh token")

    res = api.rpc("user.v1.AuthService/Refresh", {"refresh_token": refresh})
    r.expect_value(res.code, "OK", "rotating the old token into a new one")
    new_refresh = jget(res.json, "session.refresh_token|session.refreshToken", "")
    r.expect(bool(new_refresh) and new_refresh != refresh, "got a new refresh token, different from the old one")

    res = api.rpc("user.v1.AuthService/Refresh", {"refresh_token": refresh})
    r.expect_value(res.code, "Unauthenticated", "presenting an already-rotated token again")

    res = api.rpc("user.v1.AuthService/Refresh", {"refresh_token": new_refresh})
    r.expect_value(res.code, "Unauthenticated", "the reuse wiped every session for that user, so even the token just issued no longer works")

    # An access token is a JWT verified by signature; it is not stored in the database,
    # so the session wipe above does not invalidate one already held until it expires per JWT_ACCESS_TTL.
    # -----------------------------------------------------------------------
    r.step("11. Delete a user — as that user")

    res = api.rpc("user.v1.AuthService/Login", {"email": second_email, "password": args.password})
    r.expect_value(res.code, "OK", "the second user logs in, because only they may delete their own account")
    second_token = jget(res.json, "session.access_token|session.accessToken", "")
    second_refresh = jget(res.json, "session.refresh_token|session.refreshToken", "")

    res = api.rpc("user.v1.UserService/DeleteUser", {"id": second_id}, second_token)
    r.expect_value(res.code, "OK", "deleted — the response is deliberately empty, equivalent to a 204 on the REST side")

    res = api.rpc("user.v1.UserService/DeleteUser", {"id": second_id}, second_token)
    r.expect_value(res.code, "NotFound", "deleting twice, because that row really is gone")

    res = api.rpc("user.v1.AuthService/Refresh", {"refresh_token": second_refresh})
    r.expect_value(res.code, "Unauthenticated", "the delete revoked the user's refresh tokens with the row")

    return r.summary()


if __name__ == "__main__":
    try:
        sys.exit(main())
    except KeyboardInterrupt:
        sys.exit(130)
