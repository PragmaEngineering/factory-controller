#!/bin/sh
set -eu

source_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd -P)
destination=${1:-"$HOME/.local/bin"}
mkdir -p "$destination"
destination=$(CDPATH= cd -- "$destination" && pwd -P)

case ":$PATH:" in
  *":$destination:"*) ;;
  *)
    printf '%s is not on PATH. Add it to PATH or pass a bin directory that already is.\n' "$destination" >&2
    exit 1
    ;;
esac

temporary_binary=$(mktemp "$destination/.factory.XXXXXX")
trap 'rm -f "$temporary_binary"' 0
(cd "$source_dir" && go build -o "$temporary_binary" ./cmd/factory)
chmod 755 "$temporary_binary"
mv -f "$temporary_binary" "$destination/factory"
printf 'Installed %s\n' "$destination/factory"
