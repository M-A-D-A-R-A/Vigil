#!/bin/sh
set -eu

repo="${VIGIL_REPO:-M-A-D-A-R-A/Vigil}"
install_dir="${VIGIL_INSTALL_DIR:-/usr/local/bin}"

need() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "vigil install: missing required command: $1" >&2
    exit 1
  fi
}

need curl
need tar
need uname

case "$(uname -s)" in
  Darwin) os="darwin" ;;
  Linux) os="linux" ;;
  *)
    echo "vigil install: unsupported OS: $(uname -s)" >&2
    exit 1
    ;;
esac

case "$(uname -m)" in
  x86_64 | amd64) arch="amd64" ;;
  arm64 | aarch64) arch="arm64" ;;
  *)
    echo "vigil install: unsupported architecture: $(uname -m)" >&2
    exit 1
    ;;
esac

version="${VIGIL_VERSION:-}"
if [ -z "$version" ]; then
  version="$(curl -fsSL "https://api.github.com/repos/${repo}/releases/latest" | sed -n 's/.*"tag_name":[[:space:]]*"\([^"]*\)".*/\1/p' | head -n 1)"
fi

if [ -z "$version" ]; then
  echo "vigil install: could not determine latest release; set VIGIL_VERSION=vX.Y.Z" >&2
  exit 1
fi

asset="vigil_${version}_${os}_${arch}"
base_url="https://github.com/${repo}/releases/download/${version}"
tmpdir="$(mktemp -d)"
trap 'rm -rf "$tmpdir"' EXIT INT TERM

curl -fL "${base_url}/${asset}.tar.gz" -o "${tmpdir}/${asset}.tar.gz"
curl -fL "${base_url}/checksums.txt" -o "${tmpdir}/checksums.txt"

if command -v sha256sum >/dev/null 2>&1; then
  actual="$(sha256sum "${tmpdir}/${asset}.tar.gz" | awk '{print $1}')"
elif command -v shasum >/dev/null 2>&1; then
  actual="$(shasum -a 256 "${tmpdir}/${asset}.tar.gz" | awk '{print $1}')"
else
  echo "vigil install: sha256sum or shasum is required to verify checksums" >&2
  exit 1
fi

expected="$(grep " ${asset}.tar.gz\$" "${tmpdir}/checksums.txt" | awk '{print $1}')"
if [ -z "$expected" ]; then
  echo "vigil install: checksum not found for ${asset}.tar.gz" >&2
  exit 1
fi
if [ "$actual" != "$expected" ]; then
  echo "vigil install: checksum mismatch for ${asset}.tar.gz" >&2
  exit 1
fi

tar -xzf "${tmpdir}/${asset}.tar.gz" -C "$tmpdir"

install_vigil() {
  mkdir -p "$install_dir"
  if command -v install >/dev/null 2>&1; then
    install -m 0755 "${tmpdir}/vigil" "${install_dir}/vigil"
  else
    cp "${tmpdir}/vigil" "${install_dir}/vigil"
    chmod 0755 "${install_dir}/vigil"
  fi
}

if [ -d "$install_dir" ] && [ -w "$install_dir" ]; then
  install_vigil
elif [ ! -e "$install_dir" ] && mkdir -p "$install_dir" 2>/dev/null; then
  install_vigil
else
  need sudo
  sudo mkdir -p "$install_dir"
  if command -v install >/dev/null 2>&1; then
    sudo install -m 0755 "${tmpdir}/vigil" "${install_dir}/vigil"
  else
    sudo cp "${tmpdir}/vigil" "${install_dir}/vigil"
    sudo chmod 0755 "${install_dir}/vigil"
  fi
fi

echo "vigil ${version} installed to ${install_dir}/vigil"
echo "Run: vigil serve"
