#!/usr/bin/env bash
# Builds the publishable SDK packages from the committed SDK trees into <dist>, and checks each one
# holds what users import. Needs node + npm, python3 with `build`, dotnet for .NET and gradle with a
# Java 11 toolchain for Java (each skipped with a notice when missing, unless REQUIRE_ALL=1).
# Publishing is the release workflow's.
set -euo pipefail

here="$(cd "$(dirname "$0")/.." && pwd)"
dist="$(mkdir -p "${1:?usage: package_sdks.sh <dist>}" && cd "$1" && pwd)"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

# Node: compile into bin/ and publish bin/ itself. The SDK's .gitignore excludes bin/, so packing
# sdk/nodejs would ship TypeScript sources without the compiled code; bin/ gets package.json,
# README and LICENSE beside index.js, which is what `main` resolves to by default.
cp -R "$here/sdk/nodejs" "$work/nodejs"
(
  cd "$work/nodejs"
  npm install --no-audit --no-fund --package-lock=false
  npm run build
  cp package.json README.md bin/
  cp "$here/../generated/terraform/LICENSE" bin/LICENSE 2>/dev/null || cp "$here/LICENSE" bin/LICENSE
  npm pack ./bin --pack-destination "$dist" >/dev/null
)
tarball="$(ls "$dist"/*.tgz)"
contents="$(tar -tzf "$tarball")"
for f in package/index.js package/index.d.ts package/package.json package/provider.js; do
  grep -qx "$f" <<<"$contents" || { echo "npm package $tarball lacks $f" >&2; exit 1; }
done
if grep -q '\.ts$' <<<"$(grep -v '\.d\.ts$' <<<"$contents")"; then
  echo "npm package $tarball ships TypeScript sources" >&2; exit 1
fi

# Python: a wheel and an sdist, with the version the SDK was generated with.
cp -R "$here/sdk/python" "$work/python"
cp "$here/sdk/python/pulumi_betterstack/README.md" "$work/python/README.md"
python3 -m build --outdir "$dist" "$work/python" >/dev/null
ls "$dist"/pulumi_betterstack-*.whl >/dev/null

# .NET: the csproj packs on build (GeneratePackageOnBuild).
if command -v dotnet >/dev/null 2>&1; then
  cp -R "$here/sdk/dotnet" "$work/dotnet"
  dotnet build "$work/dotnet" -c Release -p:Version="$(cat "$here/sdk/dotnet/version.txt")" >/dev/null
  cp "$work/dotnet"/bin/Release/*.nupkg "$dist/"
  # SDK generation downloads logoUrl into the icon; a failed download leaves the error page there.
  python3 -c 'import sys,zipfile; z=zipfile.ZipFile(sys.argv[1]); sys.exit(0 if z.read("logo.png")[:8]==b"\x89PNG\r\n\x1a\n" else "NuGet package icon logo.png is not a PNG")' "$dist"/*.nupkg
elif [ "${REQUIRE_ALL:-}" = 1 ]; then
  echo "dotnet is missing and REQUIRE_ALL=1" >&2; exit 1
else
  echo "notice: dotnet missing, .NET package not built"
fi
# Java: the Maven publication (jar, sources, javadoc, POM) in a local repository under <dist>/maven,
# at the schema's version. The jar carries plugin.json, which tells Pulumi where to download the
# plugin; it must name this version and the vendor repository.
version="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["version"])' "$here/provider/cmd/pulumi-resource-betterstack/schema.json")"
if command -v gradle >/dev/null 2>&1 && command -v java >/dev/null 2>&1; then
  cp -R "$here/sdk/java" "$work/java"
  toolchain=()
  [ -n "${JAVA_HOME_11_X64:-}" ] && toolchain=(-Porg.gradle.java.installations.paths="$JAVA_HOME_11_X64")
  PACKAGE_VERSION="$version" gradle -p "$work/java" --no-daemon -q "${toolchain[@]}" \
    -Dmaven.repo.local="$dist/maven" publishToMavenLocal
  artifact="$dist/maven/com/betterstack/pulumi/$version"
  for f in "pulumi-$version.jar" "pulumi-$version-sources.jar" "pulumi-$version-javadoc.jar" "pulumi-$version.pom"; do
    [ -f "$artifact/$f" ] || { echo "Java publication lacks $f" >&2; exit 1; }
  done
  python3 - "$artifact/pulumi-$version.jar" "$version" <<'PY'
import json, sys, zipfile
jar, version = zipfile.ZipFile(sys.argv[1]), sys.argv[2]
names = [n for n in jar.namelist() if n.endswith("/plugin.json")]
assert names, "the jar has no plugin.json"
info = json.loads(jar.read(names[0]))
assert info.get("version") == version, f"plugin.json version {info.get('version')}, want {version}"
assert info.get("server") == "github://api.github.com/BetterStackHQ/pulumi-betterstack", f"plugin.json server {info.get('server')}"
PY
elif [ "${REQUIRE_ALL:-}" = 1 ]; then
  echo "gradle or a JDK is missing and REQUIRE_ALL=1" >&2; exit 1
else
  echo "notice: gradle or a JDK missing, Java package not built"
fi
# Every package must carry the schema's version and ask the engine for the plugin at that version:
# tfgen takes it from the provider binary's Version, so a run without the Makefile's -ldflags would
# ship the default (provider/provider.go) instead.
python3 - "$dist" "$here" "$version" <<'PY'
import glob, json, re, sys, tarfile, zipfile
dist, here, want = sys.argv[1], sys.argv[2], sys.argv[3]
found = {}
with tarfile.open(glob.glob(f"{dist}/*.tgz")[0]) as t:
    pkg = json.load(t.extractfile("package/package.json"))
    found["npm package.json version"] = pkg["version"]
    found["npm package.json pulumi.version"] = pkg["pulumi"]["version"]
whl = zipfile.ZipFile(glob.glob(f"{dist}/pulumi_betterstack-*.whl")[0])
found["wheel version"] = re.search(r"pulumi_betterstack-([^-]+)-", whl.filename).group(1)
found["wheel pulumi-plugin.json"] = json.loads(whl.read("pulumi_betterstack/pulumi-plugin.json"))["version"]
for nupkg in glob.glob(f"{dist}/*.nupkg"):
    z = zipfile.ZipFile(nupkg)
    found["nupkg version"] = re.search(r"Pulumi\.BetterStack\.(.+)\.nupkg$", nupkg).group(1)
    for n in z.namelist():
        if n.endswith("pulumi-plugin.json"): found[f"nupkg {n}"] = json.loads(z.read(n))["version"]
        if n.endswith("version.txt"): found[f"nupkg {n}"] = z.read(n).decode().strip()
found["sdk/dotnet/version.txt"] = open(f"{here}/sdk/dotnet/version.txt").read().strip()
found["sdk/go pulumi-plugin.json"] = json.load(open(f"{here}/sdk/go/betterstack/pulumi-plugin.json"))["version"]
for v in re.findall(r'semver\.MustParse\("([^"]+)"\)', open(f"{here}/sdk/go/betterstack/internal/pulumiUtilities.go").read()):
    found["sdk/go internal/pulumiUtilities.go"] = v
bad = {k: v for k, v in found.items() if v != want}
if bad:
    sys.exit(f"packages don't carry version {want}: {bad}")
PY
ls -1 "$dist"
