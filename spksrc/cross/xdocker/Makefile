PKG_NAME = xdocker
PKG_VERS = 0.1.0
PKG_EXT = tar.gz
PKG_DIST_NAME = $(PKG_NAME)-$(PKG_VERS)-src.$(PKG_EXT)
PKG_DIST_SITE = https://github.com/azeno/xdocker/releases/download/v$(PKG_VERS)
PKG_DIR = $(PKG_NAME)-$(PKG_VERS)

DEPENDS =

HOMEPAGE = https://github.com/azeno/xdocker
COMMENT  = Docker, stack, port and private registry management with image retention policies.
LICENSE  = MIT

# Pure Go (modernc.org/sqlite), no cgo.
GO_SRC_DIR = $(WORK_DIR)/$(PKG_DIR)/backend
GO_BIN_DIR = $(GO_SRC_DIR)/xdocker
CGO_ENABLED = 0
GO_LDFLAGS = -s -w -X main.version=$(PKG_VERS)
GO_BUILD_ARGS = -trimpath -o $(GO_BIN_DIR) ./cmd/xdocker

COMPILE_TARGET = xdocker_compile
INSTALL_TARGET = xdocker_install

include ../../mk/spksrc.cross-go.mk

.PHONY: xdocker_compile
xdocker_compile:
	@$(RUN) $(GO) build $(GO_BUILD_ARGS) -ldflags "$(GO_LDFLAGS)"

.PHONY: xdocker_install
xdocker_install:
	install -m 755 -d $(STAGING_INSTALL_PREFIX)/bin
	install -m 755 $(GO_BIN_DIR) $(STAGING_INSTALL_PREFIX)/bin/
