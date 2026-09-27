# Maintainer: dmcode
pkgname=dmcode
pkgver=0.1.1
pkgrel=1
pkgdesc="Terminal coding agent built on google/adk-go"
arch=('x86_64')
url="https://github.com/dedomorozoff/dmcode"
license=('MIT')
# Built from the checkout that was tagged, not from the AUR sources.
depends=()
makedepends=('go')
source=()
provides=('dmcode')
conflicts=()

build() {
  cd "$srcdir"
  make build-linux-amd64 VERSION="$pkgver"
}

package() {
  install -Dm755 "dist/dmcode-linux-amd64" "$pkgdir/usr/bin/dmcode"
}
