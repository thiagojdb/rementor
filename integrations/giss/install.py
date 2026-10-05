#!/usr/bin/env python3
"""Install the runtime-origin adapter into the local GISS Go build helper."""
import argparse
from pathlib import Path
import shutil

parser = argparse.ArgumentParser()
parser.add_argument("builder_root", type=Path)
args = parser.parse_args()
target = args.builder_root / "cmd/front-giss-v2-dev"
builder = target / "builder.go"
text = builder.read_text()
patches = [
    (
        '\t\tencoded, _ := json.Marshal(constants[key])\n',
        '\t\tif key == "URL_BASE" && runtimeOriginID() != "" {\n'
        '\t\t\tout.WriteString(".constant(\'URL_BASE\', " + runtimeBaseExpression(constants[key]) + ")\\n")\n'
        '\t\t\tcontinue\n\t\t}\n'
        '\t\tencoded, _ := json.Marshal(constants[key])\n',
    ),
    (
        'func patchRuntimeSubdomain(data []byte) []byte {\n',
        'func patchRuntimeSubdomain(data []byte) []byte {\n'
        '\tif runtimeOriginID() != "" {\n'
        '\t\treturn bytes.ReplaceAll(data, []byte("window.location.host.split(\'.\')[0]"), []byte("window.gissRuntime.subdomain()"))\n'
        '\t}\n',
    ),
    ('[]byte(markup), 0o644)', '[]byte(injectRuntimeOrigins(markup)), 0o644)'),
]
for before, after in patches:
    if after in text:
        continue
    if text.count(before) != 1:
        raise SystemExit("Unsupported builder layout; no files changed")
    text = text.replace(before, after, 1)
source = Path(__file__).parent
for name in ["runtime_origins.go", "runtime_origins_test.go"]:
    destination = target / name
    if destination.exists() and destination.read_bytes() != (source / name).read_bytes():
        raise SystemExit(f"Existing {name} differs; no files changed")
builder.write_text(text)
for name in ["runtime_origins.go", "runtime_origins_test.go"]:
    shutil.copy2(source / name, target / name)
print("Installed GISS runtime-origin adapter")
