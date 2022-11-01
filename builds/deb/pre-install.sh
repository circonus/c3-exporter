#!/bin/bash

if ! grep "^c3exporter:" /etc/group &>/dev/null; then
    groupadd -r c3exporter
fi

if ! id c3exporter &>/dev/null; then
    useradd -r -M c3exporter -s /bin/false -d /opt/circonus/c3 -g c3exporter
fi
