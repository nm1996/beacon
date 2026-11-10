#!/usr/bin/env bash
# Builds release archives for every supported platform into dist/.
#
# The version comes from $VERSION, which the release workflow sets to the
# pushed tag, or from git describe for local test builds. It is linked into
# the binary with -ldflags -X main.version.
set -euo pipefail

cd "$(dirname "$0")/.."

version="${VERSION:-$(git describe --tags --always --dirty)}"
platforms=(linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64 windows/arm64)

rm -rf dist
mkdir -p dist

for platform in "${platforms[@]}"; do
	os="${platform%/*}"
	arch="${platform#*/}"
	name="beacon_${version}_${os}_${arch}"
	binary="beacon"
	[[ "$os" == "windows" ]] && binary="beacon.exe"

	echo "building ${name}"

	CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" go build \
		-trimpath \
		-ldflags "-s -w -X main.version=${version}" \
		-o "dist/${name}/${binary}" \
		./cmd/beacon

	cp README.md LICENSE CHANGELOG.md configs/beacon.example.toml "dist/${name}/"

	if [[ "$os" == "windows" ]]; then
		(cd dist && zip -qr "${name}.zip" "${name}")
	else
		tar -C dist -czf "dist/${name}.tar.gz" "${name}"
	fi

	rm -rf "dist/${name}"
done

(cd dist && sha256sum ./*.tar.gz ./*.zip | sed 's| \./| |' > checksums.txt)

echo "archives for ${version} are in dist/"
