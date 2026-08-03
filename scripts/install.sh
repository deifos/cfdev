#!/bin/sh
set -eu

cfdev_os=$(uname -s)
case "$cfdev_os" in
  Darwin) cfdev_release_os=darwin ;;
  Linux) cfdev_release_os=linux ;;
  *) echo "cfdev does not publish a build for $cfdev_os." >&2; exit 4 ;;
esac

cfdev_arch=$(uname -m)
case "$cfdev_arch" in
  arm64|aarch64) cfdev_release_arch=arm64 ;;
  x86_64|amd64) cfdev_release_arch=amd64 ;;
  *) echo "cfdev does not publish a build for $cfdev_arch." >&2; exit 4 ;;
esac

cfdev_version=${CFDEV_VERSION:-latest}
cfdev_asset="cfdev-${cfdev_release_os}-${cfdev_release_arch}"
if [ "$cfdev_version" = "latest" ]; then
  cfdev_release_base="https://github.com/deifos/cfdev/releases/latest/download"
else
  cfdev_version=${cfdev_version#v}
  cfdev_release_base="https://github.com/deifos/cfdev/releases/download/v${cfdev_version}"
fi

cfdev_temporary=$(mktemp -d)
trap 'rm -rf "$cfdev_temporary"' EXIT HUP INT TERM

cfdev_download() {
  cfdev_url=$1
  cfdev_destination=$2
  if command -v curl >/dev/null 2>&1; then
    curl -fsSL "$cfdev_url" -o "$cfdev_destination"
  elif command -v wget >/dev/null 2>&1; then
    wget -q "$cfdev_url" -O "$cfdev_destination"
  else
    echo "Install curl or wget, then retry." >&2
    exit 4
  fi
}

echo "Downloading cfdev for ${cfdev_release_os}/${cfdev_release_arch}..."
cfdev_download "$cfdev_release_base/$cfdev_asset" "$cfdev_temporary/$cfdev_asset"
cfdev_download "$cfdev_release_base/checksums.txt" "$cfdev_temporary/checksums.txt"

cfdev_expected=$(awk -v name="$cfdev_asset" '{ candidate=$2; sub(/\r$/, "", candidate) } candidate == name || candidate == "*" name { print $1; exit }' "$cfdev_temporary/checksums.txt")
if [ -z "$cfdev_expected" ]; then
  echo "The release does not include a checksum for $cfdev_asset." >&2
  exit 4
fi
if command -v shasum >/dev/null 2>&1; then
  cfdev_actual=$(shasum -a 256 "$cfdev_temporary/$cfdev_asset" | awk '{print $1}')
elif command -v sha256sum >/dev/null 2>&1; then
  cfdev_actual=$(sha256sum "$cfdev_temporary/$cfdev_asset" | awk '{print $1}')
else
  echo "No SHA-256 utility is available to verify the download." >&2
  exit 4
fi
if [ "$cfdev_actual" != "$cfdev_expected" ]; then
  echo "The downloaded cfdev binary failed checksum verification." >&2
  exit 4
fi

cfdev_install_dir=${CFDEV_INSTALL_DIR:-"$HOME/.local/bin"}
mkdir -p "$cfdev_install_dir"
install -m 0755 "$cfdev_temporary/$cfdev_asset" "$cfdev_install_dir/cfdev"

case ":$PATH:" in
  *":$cfdev_install_dir:"*) ;;
  *)
    if [ "$cfdev_os" = "Darwin" ]; then
      cfdev_profile="$HOME/.zprofile"
    else
      cfdev_profile="$HOME/.profile"
    fi
    printf '\n# cfdev\nexport PATH="%s:$PATH"\n' "$cfdev_install_dir" >> "$cfdev_profile"
    echo "Added $cfdev_install_dir to PATH in $cfdev_profile."
    ;;
esac

"$cfdev_install_dir/cfdev" --version
echo "cfdev is installed. Open a new terminal and run: cfdev setup"
