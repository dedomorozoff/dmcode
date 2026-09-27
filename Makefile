# dmcode build. CI (.github/workflows) and the release workflow call these
# targets, so the names here are a contract — don't rename them.
#
# VERSION is passed by the release workflow from the git tag (v0.1.1 -> 0.1.1)
# and stamped into the binary via -ldflags; a local build leaves it "dev".

BINARY   := dmcode
DIST     := dist
VERSION  ?= dev
GIT_COMMIT := $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
LDFLAGS  := -s -w -X main.version=$(VERSION)
GOFLAGS  := -trimpath

# GOOS/GOARCH per target, so build-<os>-<arch> works for the whole matrix.
build-%:
	@mkdir -p $(DIST)
	@os=$$(echo $* | cut -d- -f1); \
	arch=$$(echo $* | cut -d- -f2); \
	case "$$os" in windows) ext=".exe";; *) ext="";; esac; \
	echo "building $(BINARY)-$* $(VERSION)"; \
	GOOS=$$os GOARCH=$$arch CGO_ENABLED=0 go build $(GOFLAGS) -ldflags "$(LDFLAGS)" \
		-o $(DIST)/$(BINARY)-$*$$ext .

.PHONY: vet test fmt build clean install uninstall win-zip deb rpm pkg termux

build: ## Native build into dist/ (version stamped).
	@mkdir -p $(DIST)
	go build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $(DIST)/$(BINARY)$(shell go env GOEXE) .

vet:
	go vet ./...

test:
	go test ./...

fmt:
	go fmt ./...

win-zip: build-windows-amd64
	@command -v zip >/dev/null 2>&1 || { \
		echo "win-zip needs 'zip'; raw binary is in $(DIST)/"; exit 1; }
	cd $(DIST) && zip -q $(BINARY)-windows-amd64-$(VERSION).zip $(BINARY)-windows-amd64.exe

deb: build-linux-amd64
	@rm -rf $(DIST)/deb && mkdir -p $(DIST)/deb/DEBIAN $(DIST)/deb/usr/bin
	cp $(DIST)/$(BINARY)-linux-amd64 $(DIST)/deb/usr/bin/$(BINARY)
	@printf 'Package: %s\nVersion: %s\nArchitecture: amd64\nMaintainer: dmcode\n' \
		"$(BINARY)" "$(VERSION)" > $(DIST)/deb/DEBIAN/control
	@printf 'Package: %s\nVersion: %s\nArchitecture: all\nMaintainer: dmcode\nDescription: Terminal coding agent built on google/adk-go\n' \
		"$(BINARY)" "$(VERSION)" > $(DIST)/deb/DEBIAN/info
	dpkg-deb --build --root-owner-group $(DIST)/deb $(DIST)/$(BINARY)_$(VERSION)_amd64.deb

rpm: build-linux-amd64
	mkdir -p dist/rpm/{BUILD,BUILDROOT,RPMS,SOURCES,SPECS,SRPMS}
	rpmbuild --define "_topdir $(CURDIR)/dist/rpm" \
		--define "srcdir $(CURDIR)" \
		--define "_arch x86_64" \
		--define "version $(VERSION)" \
		--define "major $(shell echo $(VERSION) | cut -d. -f1)" \
		--define "minor $(shell echo $(VERSION) | cut -d. -f2)" \
		--define "patch $(shell echo $(VERSION) | cut -d. -f3)" \
		-bb packaging/dmcode.spec

pkg: build-linux-amd64
	@# Keep PKGBUILD's pkgver in step with the tag the release workflow passes.
	sed -i.bak "s/^pkgver=.*/pkgver=$(VERSION)/" PKGBUILD && rm -f PKGBUILD.bak
	@mkdir -p $(DIST)
	PKGDEST=$(CURDIR)/$(DIST) makepkg -sf --noconfirm --nodeps

# Termux packages are plain ELF binaries; a tarball is the distribution format.
termux: build-linux-arm64
	tar czf $(DIST)/$(BINARY)-termux-$(VERSION).tar.gz -C $(DIST) $(BINARY)-linux-arm64

clean:
	rm -rf $(DIST)

# Where `make install` puts the binary. GOBIN (or GOPATH/bin) is the default
# rather than /usr/local/bin because it is writable without root and exists on
# Windows too. Pass PREFIX for a system-wide install: `sudo make install
# PREFIX=/usr/local` lands the binary in /usr/local/bin.
GOBIN_DIR := $(shell go env GOBIN)
ifeq ($(strip $(GOBIN_DIR)),)
GOBIN_DIR := $(shell go env GOPATH)/bin
endif
PREFIX ?=
BINDIR ?= $(if $(strip $(PREFIX)),$(PREFIX)/bin,$(GOBIN_DIR))

install: build ## Install the binary into $(BINDIR).
	@mkdir -p "$(BINDIR)"
	install -m 0755 $(DIST)/$(BINARY)$(shell go env GOEXE) "$(BINDIR)/$(BINARY)$(shell go env GOEXE)"
	@echo "installed $(BINDIR)/$(BINARY)$(shell go env GOEXE)"
	@case ":$$PATH:" in \
		*":$(BINDIR):"*) ;; \
		*) echo "note: $(BINDIR) is not on PATH; add it to use '$(BINARY)' by name" ;; \
	esac

uninstall: ## Remove the installed binary.
	rm -f "$(BINDIR)/$(BINARY)$(shell go env GOEXE)"
	@echo "removed $(BINDIR)/$(BINARY)$(shell go env GOEXE)"

