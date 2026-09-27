#!/usr/bin/env bash
# dmcode installer. Downloads the newest release binary for this OS/arch and
# drops it in a directory on PATH. Run it with:
#   curl -fsSL https://raw.githubusercontent.com/dedomorozoff/dmcode/main/install.sh | bash
set -euo pipefail

REPO="dedomorozoff/dmcode"
BIN="dmcode"

need() { command -v "$1" >/dev/null 2>&1 || { echo "dmcode: $1 не найден, поставь его и повтори" >&2; exit 1; }; }
need curl

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

url="https://github.com/$REPO/releases/download/$tag/${BIN}-${os}-${arch}"
echo "dmcode: ставлю $tag ($os/$arch)"

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
curl -fsSL "$url" -o "$tmp/$BIN"
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
