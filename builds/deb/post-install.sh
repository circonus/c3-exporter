#!/bin/bash

BIN_DIR=/opt/circonus/c3/sbin
SERVICE_DIR=/opt/circonus/c3/service

function install_init {
    cp -f $SERVICE_DIR/circonus-c3-exporter.init /etc/init.d/circonus-c3-exporter
    chmod +x /etc/init.d/circonus-c3-exporter
}

function install_systemd {
    cp -f $SERVICE_DIR/circonus-c3-exporter.service $1
    systemctl enable circonus-c3-exporter || true
    systemctl daemon-reload || true
}

function install_update_rcd {
    update-rc.d circonus-c3-exporter defaults
}

function install_chkconfig {
    chkconfig --add circonus-c3-exporter
}

# Remove legacy symlink, if it exists
if [[ -L /etc/init.d/circonus-c3-exporter ]]; then
    rm -f /etc/init.d/circonus-c3-exporter
fi
# Remove legacy symlink, if it exists
if [[ -L /etc/systemd/system/circonus-c3-exporter.service ]]; then
    rm -f /etc/systemd/system/circonus-c3-exporter.service
fi

# Add defaults file, if it doesn't exist
if [[ ! -f /opt/circonus/c3/etc/c3-exporter.env ]]; then
    touch /opt/circonus/c3/etc/c3-exporter.env
fi

# If 'c3-exporter.yaml' is not present use package's sample (fresh install)
if [[ ! -f /opt/circonus/c3/etc/c3-exporter.yaml ]] && [[ -f /opt/circonus/c3/etc/example-c3-exporter.yaml ]]; then
   cp /opt/circonus/c3/etc/example-c3-exporter.yaml /opt/circonus/c3/etc/c3-exporter.yaml
fi

if [[ "$(readlink /proc/1/exe)" == */systemd ]]; then
	install_systemd /lib/systemd/system/circonus-c3-exporter.service
	deb-systemd-invoke restart circonus-c3-exporter.service || echo "WARNING: systemd not running."
else
	# Assuming SysVinit
	install_init
	# Run update-rc.d or fallback to chkconfig if not available
	if which update-rc.d &>/dev/null; then
		install_update_rcd
	else
		install_chkconfig
	fi
	invoke-rc.d circonus-c3-exporter restart
fi
