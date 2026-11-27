#!/usr/bin/env bash
# Assembles the tree published as BetterStackHQ/pulumi-betterstack: this pulumi/ directory at its
# root, the generated Terraform provider it bridges under upstream/, and the license. Run it from a
# unified-api checkout after `make build_sdks VERSION=<version>`, so the SDKs carry that version.
# The vendor repository is a publishing target: development stays in unified-api.
set -euo pipefail

dest="${1:?usage: export_vendor_tree.sh <destination>}"
here="$(cd "$(dirname "$0")/.." && pwd)"   # pulumi/
root="$(cd "$here/.." && pwd)"             # unified-api

mkdir -p "$dest"
# Everything but build output and local state.
tar -C "$here" --exclude=./bin --exclude=./.baseline --exclude='./sdk/nodejs/node_modules' \
  --exclude='./sdk/nodejs/bin' --exclude='./sdk/dotnet/bin' --exclude='./sdk/dotnet/obj' \
  --exclude='./sdk/java/build' --exclude='./sdk/java/.gradle' \
  -cf - . | tar -C "$dest" -xf -
rm -rf "$dest/upstream" && mkdir -p "$dest/upstream"
# A preview API's provider code builds only with the preview tag, and its pages sit under preview/: neither
# is released (bin/export_terraform.rb leaves them out of the Terraform provider the same way).
tar -C "$root/generated/terraform" --exclude=./preview --exclude='./internal/provider/preview_*.go' \
  --exclude='./pkg/shim/preview_*.go' -cf - . | tar -C "$dest/upstream" -xf -
cp "$root/generated/terraform/LICENSE" "$dest/LICENSE"
cp "$here/release/README.vendor.md" "$dest/README.md"

# Point the module and tfgen's docs lookup at upstream/ instead of the unified-api layout.
sed -i.bak 's#=> \.\./generated/terraform#=> ./upstream#' "$dest/go.mod"
sed -i.bak 's#UpstreamRepoPath: "\.\./\.\./generated/terraform"#UpstreamRepoPath: "../upstream"#' "$dest/provider/provider.go"
rm -f "$dest/go.mod.bak" "$dest/provider/provider.go.bak"
grep -q '=> ./upstream' "$dest/go.mod"
grep -q 'UpstreamRepoPath: "../upstream"' "$dest/provider/provider.go"
