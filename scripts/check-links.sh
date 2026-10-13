#!/usr/bin/env bash
# Check that relative links and images in README.md, CONTRIBUTING.md and
# docs/*.md resolve to existing files, and that #anchors into markdown files
# match a heading.
cd "$(dirname "$0")/.."
exec python3 - <<'PY'
import re, os, glob, sys
bad = 0
def slug(h):
    s = h.strip().lower()
    s = re.sub(r'[^\w\- ]', '', s)
    return s.replace(' ', '-')
for f in ["README.md", "CONTRIBUTING.md"] + sorted(glob.glob("docs/*.md")):
    text = open(f, encoding="utf-8").read()
    text = re.sub(r"```.*?```", "", text, flags=re.S)
    targets = re.findall(r'\]\(([^)\s]+)\)', text) + re.findall(r'(?:src|srcset|href)="([^"]+)"', text)
    for t in targets:
        if re.match(r'^[a-z]+:', t):
            continue
        path, _, frag = t.partition('#')
        dest = os.path.normpath(os.path.join(os.path.dirname(f), path)) if path else f
        if not os.path.exists(dest):
            print(f"{f}: missing {t}"); bad += 1; continue
        if frag and dest.endswith(".md"):
            heads = [slug(m) for m in re.findall(r'^#+\s+(.*)$', open(dest, encoding="utf-8").read(), flags=re.M)]
            if frag not in heads:
                print(f"{f}: no anchor {t}"); bad += 1
print("broken links:", bad)
sys.exit(1 if bad else 0)
PY
