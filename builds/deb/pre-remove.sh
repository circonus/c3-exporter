#!/bin/bash

if [[ "$(readlink /proc/1/exe)" == */systemd ]]; then
	deb-systemd-invoke stop circonus-c3-exporter.service
else
	# Assuming sysv
	invoke-rc.d circonus-c3-exporter stop
fi
