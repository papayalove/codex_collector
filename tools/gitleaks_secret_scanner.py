"""Standalone high-confidence secret scanner based on Gitleaks-style TOML rules.

Files to share together:
    gitleaks_secret_scanner.py
    .gitleaks.toml

Examples:
    python gitleaks_secret_scanner.py README.md
    python gitleaks_secret_scanner.py src/ tests/ --json
    cat input.txt | python gitleaks_secret_scanner.py -

The command-line output does not include secret values by default. Use
``--include-secret`` only in a controlled environment.
"""

from __future__ import annotations

import argparse
import json
import math
import re
import sys
from collections.abc import Iterable
from dataclasses import asdict, dataclass
from pathlib import Path
from typing import Any

try:
    import tomllib
except ModuleNotFoundError:  # Python 3.10
    try:
        import tomli as tomllib  # type: ignore[no-redef]
    except ModuleNotFoundError as exc:  # pragma: no cover - depends on runtime
        raise SystemExit("Python 3.10 requires: pip install tomli") from exc


SCRIPT_DIR = Path(__file__).resolve().parent
DEFAULT_CONFIG_FILE = SCRIPT_DIR / ".gitleaks.toml"


@dataclass(frozen=True)
class SecretRule:
    rule_id: str
    description: str
    pattern: re.Pattern[str]
    secret_group: int | None = None
    entropy: float | None = None


@dataclass(frozen=True)
class SecretMatch:
    rule_id: str
    description: str
    start: int
    end: int
    secret: str

    def as_dict(self, *, include_secret: bool = True) -> dict[str, Any]:
        result = asdict(self)
        if not include_secret:
            result.pop("secret", None)
        return result


def shannon_entropy(value: str) -> float:
    if not value:
        return 0.0
    counts: dict[str, int] = {}
    for char in value:
        counts[char] = counts.get(char, 0) + 1
    length = len(value)
    return -sum((count / length) * math.log2(count / length) for count in counts.values())


def load_rules(config_file: str | Path = DEFAULT_CONFIG_FILE) -> list[SecretRule]:
    """Load Gitleaks-style ``[[rules]]`` entries from a TOML file."""
    path = Path(config_file).expanduser().resolve()
    if not path.is_file():
        raise FileNotFoundError(f"Gitleaks config file not found: {path}")

    data = tomllib.loads(path.read_text(encoding="utf-8"))
    raw_rules = data.get("rules", [])
    if not isinstance(raw_rules, list):
        raise TypeError("Gitleaks config must contain [[rules]] entries")

    rules: list[SecretRule] = []
    for index, raw_rule in enumerate(raw_rules, start=1):
        if not isinstance(raw_rule, dict):
            continue
        regex_text = raw_rule.get("regex")
        if not isinstance(regex_text, str) or not regex_text.strip():
            continue
        try:
            pattern = re.compile(regex_text)
        except re.error as exc:
            raise ValueError(f"Invalid regex in rule {raw_rule.get('id', index)!r}: {exc}") from exc

        secret_group = raw_rule.get("secretGroup")
        try:
            secret_group = int(secret_group) if secret_group is not None else None
        except (TypeError, ValueError):
            secret_group = None

        entropy = raw_rule.get("entropy")
        try:
            entropy = float(entropy) if entropy is not None else None
        except (TypeError, ValueError):
            entropy = None

        rules.append(
            SecretRule(
                rule_id=str(raw_rule.get("id") or f"rule_{index}"),
                description=str(raw_rule.get("description") or raw_rule.get("id") or f"rule_{index}"),
                pattern=pattern,
                secret_group=secret_group,
                entropy=entropy,
            )
        )
    return rules


def scan_text(text: str, rules: Iterable[SecretRule]) -> list[SecretMatch]:
    """Return high-confidence rule matches in ``text``.

    This function performs only regular-expression rule matching and optional
    Gitleaks entropy filtering. It does not run PII, NLP, password-context, or
    generic heuristic detection.
    """
    findings: list[SecretMatch] = []
    seen_spans: set[tuple[int, int]] = set()
    for rule in rules:
        for match in rule.pattern.finditer(text):
            secret = match.group(0)
            if rule.secret_group is not None:
                try:
                    secret = match.group(rule.secret_group)
                except IndexError:
                    secret = match.group(0)
            if rule.entropy is not None and shannon_entropy(secret) < rule.entropy:
                continue

            span = (match.start(), match.end())
            if span in seen_spans:
                continue
            seen_spans.add(span)
            findings.append(
                SecretMatch(
                    rule_id=rule.rule_id,
                    description=rule.description,
                    start=match.start(),
                    end=match.end(),
                    secret=match.group(0),
                )
            )
    return findings


def _line_column(text: str, offset: int) -> tuple[int, int]:
    line = text.count("\n", 0, offset) + 1
    previous_newline = text.rfind("\n", 0, offset)
    return line, offset - previous_newline


def _iter_inputs(inputs: list[str]) -> Iterable[tuple[str, str]]:
    for name in inputs:
        if name == "-":
            yield "<stdin>", sys.stdin.read()
            continue
        path = Path(name)
        if path.is_dir():
            for child in sorted(item for item in path.rglob("*") if item.is_file()):
                yield str(child), child.read_text(encoding="utf-8", errors="replace")
            continue
        yield str(path), path.read_text(encoding="utf-8", errors="replace")


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description="Scan text with Gitleaks-style secret rules.")
    parser.add_argument("inputs", nargs="+", help="files/directories to scan, or - for stdin")
    parser.add_argument("--config", type=Path, default=DEFAULT_CONFIG_FILE, help="path to .gitleaks.toml")
    parser.add_argument("--json", action="store_true", dest="as_json", help="emit JSON")
    parser.add_argument("--include-secret", action="store_true", help="include raw secret values in output")
    parser.add_argument("--fail-on-findings", action="store_true", help="exit 1 when a match is found")
    args = parser.parse_args(argv)

    try:
        rules = load_rules(args.config)
        reports: list[dict[str, Any]] = []
        for source, text in _iter_inputs(args.inputs):
            for finding in scan_text(text, rules):
                line, column = _line_column(text, finding.start)
                item = {
                    "source": source,
                    "line": line,
                    "column": column,
                    **finding.as_dict(include_secret=args.include_secret),
                }
                reports.append(item)
    except (OSError, UnicodeError, ValueError) as exc:
        parser.error(str(exc))

    if args.as_json:
        print(json.dumps(reports, ensure_ascii=False, indent=2))
    else:
        for item in reports:
            secret_detail = f" secret={item['secret']}" if args.include_secret else ""
            print(
                f"{item['source']}:{item['line']}:{item['column']} "
                f"{item['rule_id']}: {item['description']}{secret_detail}"
            )
        print(f"findings={len(reports)}")
    return 1 if reports and args.fail_on_findings else 0


if __name__ == "__main__":
    raise SystemExit(main())
