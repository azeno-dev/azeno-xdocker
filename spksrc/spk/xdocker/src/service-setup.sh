# Sourced by spksrc's generic service script (DSM 7, runs as sc-xdocker).

XDOCKER="${SYNOPKG_PKGDEST}/bin/xdocker"
SERVICE_COMMAND="${XDOCKER}"
SVC_BACKGROUND=y
SVC_WRITE_PID=y

export XDOCKER_LISTEN=":${SERVICE_PORT}"
export XDOCKER_DATA_DIR="${SYNOPKG_PKGVAR}/data"
export XDOCKER_DOCKER_HOST="unix:///var/run/docker.sock"
export XDOCKER_DOCKER_BIN="/usr/local/bin/docker"
export XDOCKER_HOST_PROC="/proc"
export XDOCKER_HOST_ADDR="localhost"
export PATH="/usr/local/bin:${PATH}"
if [ -f "${SYNOPKG_PKGVAR}/config.env" ]; then
    . "${SYNOPKG_PKGVAR}/config.env"
fi
export XDOCKER_STACKS_DIR="${XDOCKER_STACKS_DIR:-/volume1/docker}"

service_postinst ()
{
    mkdir -p "${SYNOPKG_PKGVAR}/data"
    if [ -n "${wizard_stacks_dir}" ]; then
        echo "XDOCKER_STACKS_DIR=\"${wizard_stacks_dir}\"" > "${SYNOPKG_PKGVAR}/config.env"
    fi
}

service_prestart ()
{
    if [ ! -w /var/run/docker.sock ]; then
        echo "WARNING: ${EFF_USER} cannot access /var/run/docker.sock. See the package README (Docker access)." >> "${LOG_FILE}"
    fi
}
