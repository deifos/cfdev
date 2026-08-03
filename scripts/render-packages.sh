#!/bin/sh
set -eu

if [ "$#" -ne 3 ]; then
  echo "usage: render-packages.sh <version> <checksums.txt> <output-directory>" >&2
  exit 3
fi

cfdev_version=${1#v}
cfdev_checksums=$2
cfdev_output=$3

cfdev_checksum() {
  cfdev_name=$1
  cfdev_value=$(awk -v name="$cfdev_name" '{ candidate=$2; sub(/\r$/, "", candidate) } candidate == name || candidate == "*" name { print $1; exit }' "$cfdev_checksums")
  if [ -z "$cfdev_value" ]; then
    echo "missing checksum for $cfdev_name" >&2
    exit 1
  fi
  echo "$cfdev_value"
}

cfdev_windows_amd64=$(cfdev_checksum cfdev-windows-amd64.exe)
cfdev_windows_arm64=$(cfdev_checksum cfdev-windows-arm64.exe)
cfdev_darwin_amd64=$(cfdev_checksum cfdev-darwin-amd64)
cfdev_darwin_arm64=$(cfdev_checksum cfdev-darwin-arm64)
cfdev_linux_amd64=$(cfdev_checksum cfdev-linux-amd64)
cfdev_linux_arm64=$(cfdev_checksum cfdev-linux-arm64)

mkdir -p "$cfdev_output/winget"

cfdev_render() {
  cfdev_source=$1
  cfdev_destination=$2
  sed \
    -e "s/@VERSION@/$cfdev_version/g" \
    -e "s/@WINDOWS_AMD64_SHA@/$cfdev_windows_amd64/g" \
    -e "s/@WINDOWS_ARM64_SHA@/$cfdev_windows_arm64/g" \
    -e "s/@DARWIN_AMD64_SHA@/$cfdev_darwin_amd64/g" \
    -e "s/@DARWIN_ARM64_SHA@/$cfdev_darwin_arm64/g" \
    -e "s/@LINUX_AMD64_SHA@/$cfdev_linux_amd64/g" \
    -e "s/@LINUX_ARM64_SHA@/$cfdev_linux_arm64/g" \
    "$cfdev_source" > "$cfdev_destination"
}

cfdev_render packaging/homebrew/cfdev.rb.tmpl "$cfdev_output/cfdev.rb"
cfdev_render packaging/scoop/cfdev.json.tmpl "$cfdev_output/cfdev.json"
cfdev_render packaging/winget/Deifos.cfdev.yaml.tmpl "$cfdev_output/winget/Deifos.cfdev.yaml"
cfdev_render packaging/winget/Deifos.cfdev.installer.yaml.tmpl "$cfdev_output/winget/Deifos.cfdev.installer.yaml"
cfdev_render packaging/winget/Deifos.cfdev.locale.en-US.yaml.tmpl "$cfdev_output/winget/Deifos.cfdev.locale.en-US.yaml"
