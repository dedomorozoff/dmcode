#!/usr/bin/env bash
# dmcode installer. Downloads the newest release binary for this OS/arch and
# drops it in a directory on PATH. Run it with:
#   curl -fsSL https://raw.githubusercontent.com/dedomorozoff/dmcode/main/install.sh | bash
set -euo pipefail

REPO="dedomorozoff/dmcode"
BIN="dmcode"

need() { command -v "$1" >/dev/null 2>&1 || { echo "dmcode: $1 не найден, поставь его и повтори" >&2; exit 1; }; }
need curl

# sha256_of prints the hex SHA-256 of a file using whichever tool the supported
# platforms ship: GNU coreutils, the Perl shasum on macOS/BSD, or openssl last.
sha256_of() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{print $1}'
  elif command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$1" | awk '{print $1}'
  elif command -v openssl >/dev/null 2>&1; then
    openssl dgst -sha256 "$1" | awk '{print $NF}'
  else
    return 1
  fi
}

# sum_for prints the expected digest for an asset name out of a sha256sums.txt.
# sha256sum marks binary mode with a leading '*' ("<hash> *<name>"); it is
# stripped so the match is by name whether the release wrote "name" or "*name".
sum_for() {
  awk -v n="$1" '{sub(/^\*/, "", $2); if ($2 == n) {print $1; exit}}' "$2"
}

os=$(uname -s | tr '[:upper:]' '[:lower:]')
arch=$(uname -m)
case "$arch" in
  x86_64|amd64) arch="amd64" ;;
  aarch64|arm64) arch="arm64" ;;
  *) echo "dmcode: архитектура $arch не поддерживается" >&2; exit 1 ;;
esac
case "$os" in
  linux|darwin|freebsd|openbsd|netbsd) ;;
  *) echo "dmcode: ОС $os не поддерживается, собери из исходников: go install ." >&2; exit 1 ;;
esac

# Install next to the user's shell binaries when it is on PATH, otherwise ~/.local/bin.
target_dir="$HOME/.local/bin"
for d in "$HOME/bin" "$HOME/.local/bin" /usr/local/bin; do
  case ":$PATH:" in
    *":$d:"*) target_dir="$d"; break ;;
  esac
done
if ! case ":$PATH:" in *":$target_dir:"*) true ;; *) false ;; esac; then
  target_dir="$HOME/.local/bin"
  echo "dmcode: $target_dir не в PATH — добавь в ~/.bashrc: export PATH=\"\$PATH:$HOME/.local/bin\""
fi
mkdir -p "$target_dir"

tag=$(curl -fsSL "https://api.github.com/repos/$REPO/releases/latest" \
  | sed -n 's/.*"tag_name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' | head -1)
[ -n "$tag" ] || { echo "dmcode: не удалось определить последний релиз" >&2; exit 1; }

asset="${BIN}-${os}-${arch}"
url="https://github.com/$REPO/releases/download/$tag/$asset"
echo "dmcode: ставлю $tag ($os/$arch)"

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
curl -fsSL "$url" -o "$tmp/$BIN"

# Check the download against the release's checksums when it publishes them. The
# file lists every asset; a release from before it existed skips the check with
# a note rather than failing an installer that would otherwise work.
sums_url="https://github.com/$REPO/releases/download/$tag/sha256sums.txt"
if curl -fsSL "$sums_url" -o "$tmp/sha256sums.txt" 2>/dev/null; then
  want=$(sum_for "$asset" "$tmp/sha256sums.txt")
  if [ -z "$want" ]; then
    echo "dmcode: $asset отсутствует в sha256sums.txt — пропускаю проверку" >&2
  elif got=$(sha256_of "$tmp/$BIN"); then
    if [ "$got" != "$want" ]; then
      echo "dmcode: контрольная сумма не совпала:" >&2
      echo "  ожидалось $want" >&2
      echo "  получено  $got" >&2
      exit 1
    fi
    echo "dmcode: контрольная сумма OK"
  else
    echo "dmcode: нет sha256-утилиты — пропускаю проверку" >&2
  fi
else
  echo "dmcode: нет sha256sums.txt в релизе $tag — пропускаю проверку" >&2
fi

chmod +x "$tmp/$BIN"

# Picked target is the one just verified, not whatever happens to be in PATH.
if mv "$tmp/$BIN" "$target_dir/$BIN" 2>/dev/null; then
  echo "dmcode: $target_dir/$BIN"
else
  cp "$tmp/$BIN" "$target_dir/$BIN" && chmod +x "$target_dir/$BIN"
  echo "dmcode: $target_dir/$BIN (через sudo не получилось, скопировал)"
fi

echo
echo "Готово. Запуск:"
echo "  $BIN"
echo
echo "Провайдера подберём сами: локальный Ollama/LM Studio, бесплатный"
echo "хост без ключа или мастер /setup при первом старте."
