SPK_NAME = xdocker
SPK_VERS = 0.1.0
SPK_REV = 1
SPK_ICON = src/xdocker.png

DEPENDS = cross/xdocker

# Container Manager (Docker) only exists for x86_64 and aarch64 on DSM 7.2+.
UNSUPPORTED_ARCHS = $(ARMv5_ARCHS) $(ARMv7_ARCHS) $(ARMv7L_ARCHS) $(PPC_ARCHS) $(i686_ARCHS)
REQUIRED_MIN_DSM = 7.2
SPK_DEPENDS = "ContainerManager"

MAINTAINER = azeno
DESCRIPTION = Manage Docker containers, images, stacks, ports and a private registry. Search and bulk-delete images, and clean up old images on a schedule, e.g. keep one image per month.
DISPLAY_NAME = xDocker
CHANGELOG = "First release."
HOMEPAGE = https://github.com/azeno/xdocker
LICENSE = MIT
STARTABLE = yes

SERVICE_USER = auto
SERVICE_SETUP = src/service-setup.sh
SERVICE_PORT = 8790
SERVICE_PORT_TITLE = xDocker (HTTP)
ADMIN_PORT = $(SERVICE_PORT)

# Write access to the shared folder that holds stacks (Container Manager's "docker" share).
SYSTEM_GROUP =
WIZARDS_DIR = src/wizard/
CONF_DIR = src/conf/

include ../../mk/spksrc.spk.mk
