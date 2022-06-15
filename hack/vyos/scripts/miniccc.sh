#!/bin/sh -eux

wget -O /usr/local/bin/miniccc http://${PACKER_HTTP_ADDR}/miniccc
chmod +x /usr/local/bin/miniccc

cat > /etc/systemd/system/miniccc.service <<EOF
[Unit]
Description=miniccc Agent
[Service]
ExecStart=/usr/local/bin/miniccc -serial /dev/virtio-ports/cc
[Install]
WantedBy=multi-user.target
EOF

mkdir -p /etc/systemd/system/multi-user.target.wants
ln -s /etc/systemd/system/miniccc.service /etc/systemd/system/multi-user.target.wants/miniccc.service
