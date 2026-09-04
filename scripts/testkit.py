#!/usr/bin/env python3
"""Shared plumbing for scripts/test_rest.py and scripts/test_grpc.py.

Three things live here, so the two walkthrough scripts stay a plain list of cases:

  * Reporter — the console rendering (rich) and the full, untruncated log file.
  * expectations — the assertions each case is written in, counted into a summary.
  * jget — reads a value out of a decoded JSON body by path, the little that jq was used for.

Nothing in here knows about REST or gRPC; both transports feed it the same shape.
"""

from __future__ import annotations

import json
import re
from datetime import datetime
from pathlib import Path
from typing import Any, Iterable, Sequence

try:
    from rich.console import Console
    from rich.highlighter import JSONHighlighter
    from rich.padding import Padding
    from rich.panel import Panel
    from rich.rule import Rule
    from rich.table import Table
    from rich.text import Text
except ModuleNotFoundError as exc:  # pragma: no cover - a setup problem, not a test failure
    raise SystemExit(
        f"missing Python dependency: {exc.name}\n"
        "install it with:  make py-deps   (or: python3 -m pip install -r scripts/requirements.txt)"
    ) from exc


# --------------------------------------------------------------------------- values

_MISSING = object()
_TOKEN = re.compile(r"[^.\[\]]+|\[(\d+)\]")


def jget(data: Any, path: str, default: Any = None) -> Any:
    """Read a dotted path out of decoded JSON: ``meta.limit``, ``users[0].id``.

    Alternatives are separated by ``|`` and tried in order — the JSON a gRPC gateway
    emits may name a field ``access_token`` or ``accessToken`` depending on the
    encoder, and both spellings mean the same field.
    """
    for candidate in path.split("|"):
        value = _walk(data, candidate.strip())
        if value is not _MISSING:
            return value
    return default


def _walk(data: Any, path: str) -> Any:
    current = data
    for match in _TOKEN.finditer(path):
        index = match.group(1)
        try:
            if index is not None:
                current = current[int(index)]
            else:
                current = current[match.group(0)]
        except (KeyError, IndexError, TypeError):
            return _MISSING
    return current


def as_text(value: Any) -> str:
    """Render a value the way the assertions compare it — the JSON spelling, not Python's.

    ``True`` reads as ``true`` and ``5.0`` as ``5``, so a case can be written against
    what the API actually sent rather than against how Python happened to decode it.
    """
    if value is None:
        return "null"
    if isinstance(value, bool):
        return "true" if value else "false"
    if isinstance(value, float) and value.is_integer():
        return str(int(value))
    if isinstance(value, (int, float, str)):
        return str(value)
    return json.dumps(value, separators=(",", ":"), ensure_ascii=False)


def pretty(raw: str | None) -> str:
    """Indent a JSON body for the console; anything that is not JSON is left as it is."""
    if not raw:
        return ""
    try:
        return json.dumps(json.loads(raw), indent=2, ensure_ascii=False)
    except (ValueError, TypeError):
        return raw


def compact(raw: str | None) -> str:
    """Squeeze a JSON body onto one line, for echoing a request back."""
    if not raw:
        return ""
    try:
        return json.dumps(json.loads(raw), separators=(",", ":"), ensure_ascii=False)
    except (ValueError, TypeError):
        return raw


def _relative(path: Path) -> str:
    """Show a path under the working directory as a short relative one."""
    try:
        return str(path.relative_to(Path.cwd()))
    except ValueError:
        return str(path)


def took(seconds: float) -> str:
    return f"{seconds * 1000:.0f} ms" if seconds < 1 else f"{seconds:.2f} s"


# --------------------------------------------------------------------------- reporting


class Reporter:
    """Prints the run as it happens, and records every exchange in full into a log file."""

    def __init__(
        self,
        title: str,
        facts: dict[str, str],
        log_file: Path,
        *,
        max_body_lines: int = 14,
        color: bool | None = None,
    ) -> None:
        self.console = Console(highlight=False, no_color=color is False, soft_wrap=False)
        self.title = title
        self.facts = facts
        self.log_file = log_file
        self.log_shown = _relative(log_file)
        self.max_body_lines = max_body_lines
        self.passed = 0
        self.failed: list[str] = []
        self._highlight_json = JSONHighlighter()

        self.log_file.parent.mkdir(parents=True, exist_ok=True)
        self._log_handle = self.log_file.open("w", encoding="utf-8")
        self.log(f"{title} — {datetime.now():%F %T}")
        for key, value in facts.items():
            self.log(f"{key}: {value}")

    # ---- log file ----------------------------------------------------------

    def log(self, text: str = "") -> None:
        self._log_handle.write(text + "\n")
        self._log_handle.flush()

    def close(self) -> None:
        self._log_handle.close()

    # ---- console -----------------------------------------------------------

    def banner(self) -> None:
        table = Table.grid(padding=(0, 2))
        table.add_column(style="grey50", justify="right")
        table.add_column(style="white")
        for key, value in self.facts.items():
            table.add_row(key, value)
        table.add_row("log", self.log_shown)
        self.console.print()
        self.console.print(Panel(table, title=f"[bold cyan]{self.title}", border_style="cyan", expand=False))

    def step(self, title: str) -> None:
        self.console.print()
        self.console.print(Rule(Text(title, style="bold cyan"), align="left", style="cyan"))
        self.log()
        self.log(f"══════ {title}")

    def note(self, text: str) -> None:
        self.console.print(Text("  " + text, style="grey42"))

    def call(
        self,
        action: str,
        *,
        request: str | None = None,
        meta: Sequence[str] = (),
        status: str = "",
        style: str = "green",
        elapsed: float = 0.0,
        body: str | None = None,
        error: str | None = None,
    ) -> None:
        """Render one request/response pair — the arrow lines, the bodies, the timing."""
        self.console.print(Text("  → ", style="grey50") + Text(action, style="bold"), no_wrap=True, overflow="ellipsis")
        for line in meta:
            self.console.print(Text("      " + line, style="grey42"))
        if request:
            self._print_body(compact(request), dim=True)
        self.console.print(
            Text("  ← ", style="grey50")
            + Text(status, style=f"bold {style}")
            + Text(f"   {took(elapsed)}", style="grey42")
        )
        if body:
            self._print_body(pretty(body))
        if error:
            for line in error.strip().splitlines()[: self.max_body_lines]:
                self.console.print(Text("      " + line, style="red", no_wrap=True, overflow="ellipsis"))

    def body(self, text: str, *, dim: bool = False) -> None:
        """Print a JSON (or plain text) block on its own, outside a request/response pair."""
        self._print_body(text, dim=dim)

    def _print_body(self, text: str, *, dim: bool = False) -> None:
        """Indent a body under the line it belongs to, clipped to a readable height.

        Long lines are ellipsised rather than wrapped — a JWT or a cursor would otherwise
        spill over several lines and bury the shape of the payload. The log file keeps it whole.
        """
        if not text.strip():
            return
        lines = text.splitlines()
        clipped = lines[: self.max_body_lines]
        as_json = not dim and text.lstrip().startswith(("{", "["))
        # Printed line by line: a single block would be padded out to the widest line,
        # which shows up as trailing whitespace when the output is piped into a file.
        for line in clipped:
            rendered = Text("      " + line, style="grey42" if dim else "grey70")
            if as_json:
                self._highlight_json(rendered)
            self.console.print(rendered, no_wrap=True, overflow="ellipsis")
        if len(lines) > len(clipped):
            self.console.print(Text(f"      … {len(lines) - len(clipped)} more lines (see the log)", style="grey42"))

    def exchange(self, header: str, sections: Iterable[tuple[str, str | None]]) -> None:
        """Record the untruncated exchange — bodies, headers and tokens all intact."""
        self.log()
        self.log(f"--- {header}")
        for label, value in sections:
            if value:
                self.log(f"{label}:\n{value}" if "\n" in value.strip() else f"{label}: {value}")

    # ---- assertions --------------------------------------------------------

    def ok(self, label: str) -> bool:
        self.passed += 1
        self.console.print(Padding(Text("✓ ", style="bold green") + Text(label, style="grey70"), (0, 0, 0, 4), expand=False))
        self.log(f"  PASS: {label}")
        return True

    def ng(self, label: str) -> bool:
        self.failed.append(label)
        self.console.print(Padding(Text("✗ ", style="bold red") + Text(label, style="red"), (0, 0, 0, 4), expand=False))
        self.log(f"  FAIL: {label}")
        return False

    def expect(self, condition: bool, label: str, detail: str = "") -> bool:
        return self.ok(label) if condition else self.ng(f"{label}{detail}")

    def expect_value(self, got: Any, want: Any, label: str, *, what: str = "") -> bool:
        got_text, want_text = as_text(got), as_text(want)
        subject = f"{what} " if what else ""
        if got_text == want_text:
            return self.ok(f"{label} → {subject}{want_text}")
        return self.ng(f'{label} → {subject}expected "{want_text}" but got "{got_text}"')

    def expect_field(self, body: Any, path: str, want: Any, label: str) -> bool:
        return self.expect_value(jget(body, path, "<missing>"), want, label, what=f"{path} =")

    # ---- the end -----------------------------------------------------------

    def summary(self) -> int:
        """Print the tally, mirror it into the log, and give back the exit code."""
        total_failed = len(self.failed)
        table = Table.grid(padding=(0, 2))
        table.add_column(justify="right")
        table.add_column()
        table.add_row(Text("passed", style="grey50"), Text(str(self.passed), style="bold green"))
        table.add_row(Text("failed", style="grey50"), Text(str(total_failed), style="bold red" if total_failed else "grey50"))
        table.add_row(Text("log", style="grey50"), Text(self.log_shown, style="white"))

        self.console.print()
        self.console.print(
            Panel(
                table,
                title="[bold red]failed" if total_failed else "[bold green]every case passed",
                border_style="red" if total_failed else "green",
                expand=False,
            )
        )

        self.log()
        self.log("══════ summary")
        self.log(f"passed {self.passed} / failed {total_failed}")
        for case in self.failed:
            self.log(f"FAIL: {case}")

        if total_failed:
            self.console.print(Text(f"{total_failed} case(s) failed:", style="bold red"))
            for case in self.failed:
                self.console.print(Text("  ✗ " + case, style="red"))
        self.close()
        return 1 if total_failed else 0
