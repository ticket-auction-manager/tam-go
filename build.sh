#!/bin/bash
# Builds the programs into ./build.
#
#   ./build.sh client     build the web app, then tam-client
#   ./build.sh server     build tam-server
#   ./build.sh all        both
#   ./build.sh release    the web app once, then both programs for windows,
#                         linux and darwin on amd64 and arm64 into
#                         build/<os>-<arch>/, and one archive per program
#                         and target in build/: tam-server-<version>-<os>-
#                         <arch>.zip and tam-client-<version>-<os>-<arch>.zip
#                         for windows (plus the bare .exe under the same
#                         name), .tar.gz for the others, and for linux a
#                         .deb and an .rpm of each program as well
#
# Cross-compile one target by setting GOOS and GOARCH, for example:
#   GOOS=linux GOARCH=amd64 ./build.sh all
#
# VERSION stamps internal/version.Version (both programs print it, the
# server reports it on GET /api and its admin page) and names the files; it
# defaults to `git describe --tags --always --dirty`, and a leading v is
# dropped (the tag v1.2.3 is version 1.2.3). PKG_RELEASE is the revision of
# the .deb and .rpm, 1 unless a package is built again from the same
# version. SKIP_WEB=1 keeps an existing cmd/tam-client/dist instead of
# building the web app again.
set -euo pipefail
cd "$(dirname "$0")"

target="${1:-}"
version="${VERSION:-$(git describe --tags --always --dirty 2>/dev/null || echo 0.0.1)}"
case "$version" in
  v[0-9]*.[0-9]*) version="${version#v}" ;;
esac
ldflags="-s -w -X ticket-auction-manager/tam-go/internal/version.Version=${version}"
# The Linux packages need a version deb and rpm accept: 1.2.3 gives 1.2.3,
# 1.2.3-rc1 and 1.2.3rc1 give 1.2.3 with the prerelease rc1 (sorted before
# 1.2.3), and anything else, such as the bare commit git describe gives in a
# clone without tags, gives 0.0.0 with the whole string, letters, digits and
# dots only, as the prerelease, so it sorts below every release.
case "$version" in
  [0-9]*.[0-9]*)
    pkg_version="$(printf '%s' "${version%%-*}" | sed -E 's/^([0-9]+(\.[0-9]+)*).*/\1/')"
    pkg_prerelease="${version#"$pkg_version"}"
    pkg_prerelease="${pkg_prerelease#-}"
    ;;
  *)
    pkg_version="0.0.0"
    pkg_prerelease="$(printf '%s' "$version" | tr -c 'A-Za-z0-9.' '.')"
    ;;
esac
pkg_release="${PKG_RELEASE:-1}"
# The Windows version resource wants four numbers: 1.0.0 becomes 1.0.0.0.
winver="$pkg_version"
while [ "$(printf '%s' "$winver" | tr -cd . | wc -c)" -lt 3 ]; do
  winver="$winver.0"
done
# nfpm (https://nfpm.goreleaser.com) writes the .deb and .rpm files, and
# go-winres (https://github.com/tc-hib/go-winres) the Windows icon and
# version resources; these are the versions build.sh installs when none is
# on the PATH.
winres_version=v0.3.3
nfpm_version=v2.47.0
goos="${GOOS:-$(go env GOOS)}"
ext=""
if [ "$goos" = "windows" ]; then
  ext=".exe"
fi
mkdir -p build

# pnpm 12, as CI and the Dockerfile use, when pnpm itself is not installed.
pnpm_cmd="pnpm"
if ! command -v pnpm >/dev/null 2>&1; then
  pnpm_cmd="npx --yes pnpm@12"
fi

build_web() {
  if [ "${SKIP_WEB:-}" = "1" ] && [ -f cmd/tam-client/dist/index.html ]; then
    echo "SKIP_WEB=1: keeping the web app in cmd/tam-client/dist"
    return
  fi
  (cd frontend && $pnpm_cmd install --frozen-lockfile && $pnpm_cmd build)
}

build_client() {
  CGO_ENABLED=0 go build -trimpath -ldflags "$ldflags" -o "build/tam-client${ext}" ./cmd/tam-client/
}

build_server() {
  CGO_ENABLED=0 go build -trimpath -ldflags "$ldflags" -o "build/tam-server${ext}" ./cmd/tam-server/
}

# The release targets. Both Windows builds get the icon and version
# resources (cmd/*/rsrc_windows_amd64.syso and rsrc_windows_arm64.syso).
release_targets="windows/amd64 windows/arm64 linux/amd64 linux/arm64 darwin/amd64 darwin/arm64"

# python_cmd prints a Python 3 interpreter, which writes the archives when
# zip or tar is not around.
python_cmd() {
  local p
  for p in python3 python; do
    if "$p" -c 'import sys; sys.exit(sys.version_info[0] < 3)' >/dev/null 2>&1; then
      echo "$p"
      return 0
    fi
  done
  echo "build.sh: neither zip/tar nor python found to write the archives" >&2
  return 1
}

# tar_owner_flags prints the flags that make tar record every file as owned
# by root (uid and gid 0, no names): GNU tar's, or bsdtar's on macOS.
tar_owner_flags() {
  case "$(tar --version 2>/dev/null)" in
    *"GNU tar"*) echo "--owner=0 --group=0 --numeric-owner" ;;
    *bsdtar*) echo "--uid 0 --gid 0 --numeric-owner" ;;
  esac
}

# on_windows_shell reports whether this is Git Bash, MSYS or Cygwin, where
# tar only sees an executable bit on files it recognises by their content
# (an ELF program yes, a macOS one no), so the archives are written by
# Python there, with the bits set explicitly.
on_windows_shell() {
  case "$(uname -s)" in
    MINGW* | MSYS* | CYGWIN*) return 0 ;;
  esac
  return 1
}

# go_tool NAME MODULE@VERSION prints the command for a Go tool, installing it
# once into Go's bin folder when it is not on the PATH (a fresh machine, the
# release build on GitHub).
go_tool() {
  local name=$1 module=$2 gobin
  if command -v "$name" >/dev/null 2>&1; then
    echo "$name"
    return 0
  fi
  gobin="$(go env GOPATH)/bin"
  if command -v cygpath >/dev/null 2>&1; then
    gobin="$(cygpath -u "$gobin")"
  fi
  if [ ! -x "$gobin/$name" ] && [ ! -x "$gobin/$name.exe" ]; then
    echo "installing $module with go install" >&2
    go install "$module" >&2
  fi
  if [ -x "$gobin/$name.exe" ]; then
    echo "$gobin/$name.exe"
  else
    echo "$gobin/$name"
  fi
}

nfpm_cmd() { go_tool nfpm "github.com/goreleaser/nfpm/v2/cmd/nfpm@$nfpm_version"; }

# stamp_winres writes the Windows resources of both programs with the
# release version (the committed ones say 0.0.1); unstamp_winres puts the
# committed files back afterwards so the checkout stays clean.
stamp_winres() {
  local winres p
  winres="$(go_tool go-winres "github.com/tc-hib/go-winres@$winres_version")"
  for p in tam-server tam-client; do
    "$winres" make --in "cmd/$p/winres/winres.json" --arch amd64,arm64 --out "cmd/$p/rsrc" \
      --product-version "$version" --file-version "$winver"
  done
}
unstamp_winres() {
  local f
  command -v git >/dev/null 2>&1 || return 0
  for f in cmd/tam-server/rsrc_windows_*.syso cmd/tam-client/rsrc_windows_*.syso; do
    if git ls-files --error-unmatch "$f" >/dev/null 2>&1; then
      git checkout -- "$f"
    fi
  done
}

# package_distro ARCH PROGRAM: a .deb and an .rpm of PROGRAM for linux/ARCH
# in build/, from deploy/linux/nfpm. A distribution package puts the program
# in /usr/bin, so its unit and desktop entry are the archive's with that path,
# and the package scripts get the program's name.
package_distro() {
  local arch=$1 program=$2 f fmt nfpm dir
  dir="build/pkg/$program"
  rm -rf "$dir"
  mkdir -p "$dir"
  sed 's#/usr/local/bin/#/usr/bin/#g' "deploy/linux/$program.service" > "$dir/$program.service"
  if [ "$program" = tam-client ]; then
    sed 's#/usr/local/bin/#/usr/bin/#g' deploy/linux/tam-client.desktop > "$dir/tam-client.desktop"
  fi
  for f in preinstall postinstall preremove postremove posttrans; do
    sed "s#@PROGRAM@#$program#g" "deploy/linux/nfpm/$f.sh" > "$dir/$f.sh"
  done
  sed -e "s#@ARCH@#$arch#g" -e "s#@VERSION@#$pkg_version#g" -e "s#@PRERELEASE@#$pkg_prerelease#g" \
    -e "s#@RELEASE@#$pkg_release#g" "deploy/linux/nfpm/$program.yaml" > "$dir/nfpm.yaml"
  nfpm="$(nfpm_cmd)"
  for fmt in deb rpm; do
    "$nfpm" package -f "$dir/nfpm.yaml" -p "$fmt" -t build/ | sed 's#^.*created package: #wrote #'
  done
  rm -rf "$dir"
}

# package_program OS ARCH PROGRAM: one archive in build/ with a single
# top-level folder holding that program, README.md, LICENSE.md and its deploy
# files for that system: on Linux its unit, install.sh and icon (and for the
# client the application-menu entry and tam-client-open, which it runs), on
# macOS its launchd file and the notes as INSTALL.md.
package_program() {
  local os=$1 arch=$2 program=$3 ext="" name stage f
  if [ "$os" = "windows" ]; then
    ext=".exe"
  fi
  name="${program}-${version}-${os}-${arch}"
  stage="build/$name"
  rm -rf "$stage"
  mkdir -p "$stage"
  cp "build/$os-$arch/$program$ext" README.md LICENSE.md "$stage/"
  case "$os" in
    linux)
      cp "deploy/linux/$program.service" deploy/linux/install.sh "$stage/"
      cp "cmd/$program/icon.svg" "$stage/$program.svg"
      if [ "$program" = tam-client ]; then
        cp deploy/linux/tam-client.desktop deploy/linux/tam-client-open "$stage/"
      fi
      ;;
    darwin)
      cp "deploy/macos/com.ticket-auction-manager.$program.plist" "$stage/"
      cp deploy/macos/README.md "$stage/INSTALL.md"
      ;;
  esac
  # The program and the scripts are executable, the rest is not, whatever
  # the file system here says.
  find "$stage" -type f -exec chmod 644 {} +
  chmod 755 "$stage/$program$ext"
  for f in install.sh tam-client-open; do
    if [ -f "$stage/$f" ]; then
      chmod 755 "$stage/$f"
    fi
  done

  if [ "$os" = "windows" ]; then
    rm -f "build/$name.zip"
    if command -v zip >/dev/null 2>&1; then
      (cd build && zip -qr "$name.zip" "$name")
    else
      "$(python_cmd)" -c 'import shutil, sys; shutil.make_archive(sys.argv[1] + "/" + sys.argv[2], "zip", root_dir=sys.argv[1], base_dir=sys.argv[2])' build "$name"
    fi
    echo "wrote build/$name.zip"
    # The program alone as well: on Windows one file is all it takes, so a
    # download that is double-clicked runs without unpacking anything.
    cp "build/$os-$arch/$program$ext" "build/$name$ext"
    echo "wrote build/$name$ext"
  else
    rm -f "build/$name.tar.gz"
    if command -v tar >/dev/null 2>&1 && ! on_windows_shell; then
      # The files belong to root in the archive, not to whoever built it.
      # shellcheck disable=SC2046 # the flags are separate words
      tar $(tar_owner_flags) -czf "build/$name.tar.gz" -C build "$name"
    else
      "$(python_cmd)" - build "$name" <<'PY'
import os, sys, tarfile
build, name = sys.argv[1], sys.argv[2]
def modes(info):
    base = os.path.basename(info.name)
    info.uid = info.gid = 0
    info.uname = info.gname = ""
    info.mode = 0o755 if info.isdir() or base in ("tam-server", "tam-client", "tam-client-open") or base.endswith(".sh") else 0o644
    return info
with tarfile.open(os.path.join(build, name + ".tar.gz"), "w:gz") as tar:
    tar.add(os.path.join(build, name), arcname=name, filter=modes)
PY
    fi
    echo "wrote build/$name.tar.gz"
  fi
  rm -rf "$stage"
}

build_release() {
  local t os arch out ext
  echo "version $version"
  build_web
  rm -rf build/tam-server-* build/tam-client-* build/pkg build/*.deb build/*.rpm
  stamp_winres
  trap unstamp_winres EXIT
  for t in $release_targets; do
    os="${t%/*}"
    arch="${t#*/}"
    ext=""
    if [ "$os" = "windows" ]; then
      ext=".exe"
    fi
    out="build/$os-$arch"
    rm -rf "$out"
    mkdir -p "$out"
    echo "building $os/$arch"
    GOOS="$os" GOARCH="$arch" CGO_ENABLED=0 go build -trimpath -ldflags "$ldflags" -o "$out/tam-server$ext" ./cmd/tam-server/
    GOOS="$os" GOARCH="$arch" CGO_ENABLED=0 go build -trimpath -ldflags "$ldflags" -o "$out/tam-client$ext" ./cmd/tam-client/
    package_program "$os" "$arch" tam-server
    package_program "$os" "$arch" tam-client
    if [ "$os" = linux ]; then
      package_distro "$arch" tam-server
      package_distro "$arch" tam-client
    fi
  done
  # The .rpm files are among tam-server-* and tam-client-*.
  ls -la build/tam-server-* build/tam-client-* build/*.deb
}

case "$target" in
  client) build_web; build_client ;;
  server) build_server ;;
  all) build_web; build_client; build_server ;;
  release) build_release; exit 0 ;;
  *) echo "Usage: $0 client|server|all|release"; exit 1 ;;
esac

if command -v upx >/dev/null 2>&1; then
  upx -q build/tam-*"${ext}" || true
fi
ls -la build/
