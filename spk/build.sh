#!/bin/sh
# Builds xdocker-<version>-x86_64.spk for manual install on DSM 7.2+.
#   ./spk/build.sh [version]        (run from anywhere; needs go and npm)
set -eu

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
VERSION="${1:-0.1.0}"
SPK_VERSION="${VERSION}-0001"
ARCH="${ARCH:-amd64}"
MAINTAINER="${MAINTAINER:-azeno}"
HOMEPAGE="${HOMEPAGE:-https://github.com/azeno/xdocker}"
OUT="${ROOT}/dist"
WORK="$(mktemp -d)"
trap 'rm -rf "${WORK}"' EXIT

echo "==> web UI"
if ! grep -q '/assets/' "${ROOT}/backend/internal/web/dist/index.html" 2>/dev/null || [ "${REBUILD_UI:-0}" = 1 ]; then
    (cd "${ROOT}/frontend" && npm ci --no-audit --no-fund >/dev/null && npm run build >/dev/null)
fi

echo "==> xdocker binary (linux/${ARCH})"
mkdir -p "${WORK}/package/bin"
(cd "${ROOT}/backend" && CGO_ENABLED=0 GOOS=linux GOARCH="${ARCH}" \
    go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o "${WORK}/package/bin/xdocker" ./cmd/xdocker)

echo "==> package.tgz"
mkdir -p "${WORK}/package/ui/images" "${WORK}/package/app"
cp "${ROOT}/spk/ui/config" "${WORK}/package/ui/config"
cp "${ROOT}/spk/app/xdocker.sc" "${WORK}/package/app/"
(cd "${ROOT}/backend" && go run ./tools/genicon "${WORK}/package/ui/images" "xdocker_%d.png" 16 24 32 48 64 72 256)
if tar --version 2>/dev/null | grep -q GNU; then OWN="--owner=0 --group=0"; else OWN="--uid 0 --gid 0"; fi
(cd "${WORK}/package" && COPYFILE_DISABLE=1 tar ${OWN} -czf "${WORK}/package.tgz" .)

echo "==> spk"
mkdir -p "${WORK}/spk"
cp "${WORK}/package.tgz" "${WORK}/spk/"
cp -R "${ROOT}/spk/scripts" "${ROOT}/spk/conf" "${ROOT}/spk/WIZARD_UIFILES" "${WORK}/spk/"
cp "${WORK}/package/ui/images/xdocker_64.png" "${WORK}/spk/PACKAGE_ICON.PNG"
cp "${WORK}/package/ui/images/xdocker_256.png" "${WORK}/spk/PACKAGE_ICON_256.PNG"
CHECKSUM="$( (md5sum "${WORK}/package.tgz" 2>/dev/null || md5 -r "${WORK}/package.tgz") | cut -d' ' -f1)"
sed -e "s|@VERSION@|${SPK_VERSION}|" -e "s|@MAINTAINER@|${MAINTAINER}|" -e "s|@HOMEPAGE@|${HOMEPAGE}|" \
    "${ROOT}/spk/INFO.in" > "${WORK}/spk/INFO"
echo "checksum=\"${CHECKSUM}\"" >> "${WORK}/spk/INFO"
[ "${ARCH}" = "arm64" ] && sed -i.bak 's/^arch=.*/arch="armv8"/' "${WORK}/spk/INFO" && rm -f "${WORK}/spk/INFO.bak"
chmod 755 "${WORK}/spk/scripts/"*

mkdir -p "${OUT}"
NAME="xdocker-${SPK_VERSION}-$( [ "${ARCH}" = amd64 ] && echo x86_64 || echo armv8 ).spk"
# An SPK is a plain (uncompressed) tar; entries are owned by root.
if tar --version 2>/dev/null | grep -q GNU; then OWN="--owner=0 --group=0"; else OWN="--uid 0 --gid 0"; fi
(cd "${WORK}/spk" && COPYFILE_DISABLE=1 tar ${OWN} -cf "${OUT}/${NAME}" INFO package.tgz scripts conf WIZARD_UIFILES PACKAGE_ICON.PNG PACKAGE_ICON_256.PNG)
echo "==> ${OUT}/${NAME}"
