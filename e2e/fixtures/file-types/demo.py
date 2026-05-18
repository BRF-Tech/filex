# filex companion utility — counts files per extension.
from collections import Counter
from pathlib import Path

def by_extension(root: Path) -> Counter[str]:
    c: Counter[str] = Counter()
    for p in root.rglob('*'):
        if p.is_file():
            c[p.suffix.lower() or '<noext>'] += 1
    return c

if __name__ == '__main__':
    import sys
    for ext, n in by_extension(Path(sys.argv[1])).most_common():
        print(f'{n:>6}  {ext}')
