"""filex companion utility — counts files per extension.

Walks a directory tree and emits a markdown table sorted by count. The
counterpart `filex` CLI ships this with the binary, but the script is
useful as a one-off for ad-hoc audit work.
"""
from __future__ import annotations
from collections import Counter
from pathlib import Path
import argparse
import sys


def by_extension(root: Path) -> Counter[str]:
    c: Counter[str] = Counter()
    for p in root.rglob('*'):
        if p.is_file():
            c[p.suffix.lower() or '<noext>'] += 1
    return c


def render_markdown(c: Counter[str]) -> str:
    out = ['| Extension | Count |', '|-----------|------:|']
    for ext, n in c.most_common():
        out.append(f'| {ext:<9} | {n:>5} |')
    return '\n'.join(out)


def main(argv: list[str] | None = None) -> int:
    p = argparse.ArgumentParser()
    p.add_argument('root', type=Path)
    p.add_argument('--limit', type=int, default=0)
    args = p.parse_args(argv)
    counts = by_extension(args.root)
    if args.limit:
        counts = Counter(dict(counts.most_common(args.limit)))
    print(render_markdown(counts))
    return 0


if __name__ == '__main__':
    sys.exit(main())
