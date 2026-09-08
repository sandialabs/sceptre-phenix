package image

const PostbuildAptCleanup = `
# --------------------------------------------------- Cleanup ----------------------------------------------------
apt clean || apt-get clean || echo "unable to clean apt cache"
`

const PostbuildNoRootPasswd = `
# ---------------------------------------------- No Root Password ------------------------------------------------
sed -i 's/nullok_secure/nullok/' /etc/pam.d/common-auth
sed -i 's/#PermitRootLogin prohibit-password/PermitRootLogin yes/' /etc/ssh/sshd_config
sed -i 's/#PermitEmptyPasswords no/PermitEmptyPasswords yes/' /etc/ssh/sshd_config
sed -i 's/PermitRootLogin prohibit-password/PermitRootLogin yes/' /etc/ssh/sshd_config
sed -i 's/PermitEmptyPasswords no/PermitEmptyPasswords yes/' /etc/ssh/sshd_config
passwd -d root
`

const PostbuildPhenixHostname = `
# -------------------------------------------------- Hostname ----------------------------------------------------
echo "phenix" > /etc/hostname
sed -i 's/127.0.1.1 .*/127.0.1.1 phenix/' /etc/hosts
cat > /etc/motd <<EOF

██████╗ ██╗  ██╗███████╗███╗  ██╗██╗██╗  ██╗
██╔══██╗██║  ██║██╔════╝████╗ ██║██║╚██╗██╔╝
██████╔╝███████║█████╗  ██╔██╗██║██║ ╚███╔╝
██╔═══╝ ██╔══██║██╔══╝  ██║╚████║██║ ██╔██╗
██║     ██║  ██║███████╗██║ ╚███║██║██╔╝╚██╗
╚═╝     ╚═╝  ╚═╝╚══════╝╚═╝  ╚══╝╚═╝╚═╝  ╚═╝

EOF
echo "\nBuilt with phenix image on $(date)\n\n" >> /etc/motd
`

const PostbuildPhenixBase = `
# ----------------------------------------------------- Base -----------------------------------------------------
cat > /etc/systemd/system/miniccc.service <<EOF
[Unit]
Description=miniccc
[Service]
ExecStart=/opt/minimega/bin/miniccc -v=false -serial /dev/virtio-ports/cc -logfile /var/log/miniccc.log
[Install]
WantedBy=multi-user.target
EOF
cat > /etc/systemd/system/phenix.service <<EOF
[Unit]
Description=phenix startup service
After=network.target systemd-hostnamed.service
[Service]
Environment=LD_LIBRARY_PATH=/usr/local/lib
ExecStart=/usr/local/bin/phenix-start.sh
RemainAfterExit=true
StandardOutput=journal
Type=oneshot
[Install]
WantedBy=multi-user.target
EOF
mkdir -p /etc/systemd/system/multi-user.target.wants
ln -s /etc/systemd/system/miniccc.service /etc/systemd/system/multi-user.target.wants/miniccc.service
ln -s /etc/systemd/system/phenix.service /etc/systemd/system/multi-user.target.wants/phenix.service
mkdir -p /usr/local/bin
cat > /usr/local/bin/phenix-start.sh <<EOF
#!/bin/bash
for file in /etc/phenix/startup/*; do
	echo \$file
	bash \$file
done
EOF
chmod +x /usr/local/bin/phenix-start.sh
mkdir -p /etc/phenix/startup
`

const PostbuildGUI = `
# ----------------------------------------------------- GUI ------------------------------------------------------
# Autologin root and resolution
apt-get purge -y gdm3 # messes with no-root-password login
mkdir -p /root/.config/xfce4/
echo "TerminalEmulator=xfce4-terminal" > /root/.config/xfce4/helpers.rc
mkdir -p /root/.config/autostart/
cat > /root/.config/autostart/init.desktop <<EOF
[Desktop Entry]
Name=Init
Type=Application
Exec=/root/.init.sh
Terminal=false
EOF
cat >> /root/.init.sh <<EOF
sleep 10 # wait for desktop session to be available
xfconf-query -c xsettings -p /Net/ThemeName -s "Adwaita-dark"
xfconf-query -c xfce4-desktop -p /backdrop/screen0/monitorVirtual-1/workspace0/last-image -s "/usr/share/backgrounds/Optical_Fibers_in_Dark_by_Elena_Stravoravdi.jpg"
EOF
chmod +x /root/.init.sh
# Autologin root
cat > /etc/lightdm/lightdm.conf <<EOF
[Seat:*]
autologin-user=root
autologin-user-timeout=0
display-setup-script=xrandr --output Virtual-1 --mode 1440x900
[daemon]
AutomaticLoginEnable=true
AutomaticLogin=root
EOF
sed -e '/pam_succeed_if.so/s/^#*/#/' -i /etc/pam.d/lightdm-autologin
`

const PostbuildKaliGUI = `
# ----------------------------------------------------- GUI ------------------------------------------------------
# Autologin root and resolution
cat > /etc/lightdm/lightdm.conf <<EOF
[Seat:*]
autologin-user=root
autologin-user-timeout=0
display-setup-script=/root/.init.sh
EOF
sed -i '/quiet_success/s/^/#/' /etc/pam.d/lightdm-autologin
cat > /root/.init.sh <<EOF
#!/bin/sh
xrandr --newmode $(cvt 1600 900 | grep Modeline | sed 's/Modeline //g')
xrandr --addmode Virtual-1 "1600x900_60.00"
xrandr --output Virtual-1 --mode "1600x900_60.00"
EOF
chmod +x /root/.init.sh
`

const PostbuildProtonuke = `
# -------------------------------------------------- Protonuke ---------------------------------------------------
cat > /etc/systemd/system/protonuke.service <<EOF
[Unit]
Description=protonuke
After=network-online.target
Wants=network-online.target
[Service]
EnvironmentFile=/etc/default/protonuke
ExecStart=/opt/minimega/bin/protonuke \$PROTONUKE_ARGS
[Install]
WantedBy=multi-user.target
EOF
mkdir -p /etc/systemd/system/multi-user.target.wants
ln -s /etc/systemd/system/protonuke.service /etc/systemd/system/multi-user.target.wants/protonuke.service
`

const PostbuildEnableDHCP = `
# ----------------------------------------------------- DHCP -----------------------------------------------------
echo "#!/bin/bash\ndhclient" > /etc/init.d/dhcp.sh
chmod +x /etc/init.d/dhcp.sh
update-rc.d dhcp.sh defaults 100
`

var DefaultPackages = []string{ //nolint:gochecknoglobals // global constant
	"curl",
	"ethtool",
	"ncat",
	"net-tools",
	"openssh-server",
	"rsync",
	"ssh",
	"tcpdump",
	"tmux",
	"vim",
	"wget",
}

var DebianComponents = []string{ //nolint:gochecknoglobals // global constant
	"main",
	"contrib",
	"non-free",
	"non-free-firmware",
}

var UbuntuComponents = []string{ //nolint:gochecknoglobals // global constant
	"main",
	"restricted",
	"universe",
	"multiverse",
}

var DebianPackages = []string{ //nolint:gochecknoglobals // global constant
	"dbus",
	"gpg",
	"initramfs-tools",
	"linux-image-amd64",
	"linux-headers-amd64",
	"locales",
}

var DebianMinGUIPackages = []string{ //nolint:gochecknoglobals // global constant
	"wmctrl",
	"xdotool",
	"xfce4",
	"xfce4-terminal",
}

var KaliComponents = []string{ //nolint:gochecknoglobals // global constant
	"main",
	"contrib",
	"non-free",
	"non-free-firmware",
}

var KaliPackages = []string{ //nolint:gochecknoglobals // global constant
	"linux-image-amd64",
	"linux-headers-amd64",
	"default-jdk",
}

var KaliMinGUIPackages = []string{ //nolint:gochecknoglobals // global constant
	"kali-desktop-xfce",
	"wmctrl",
	"xdotool",
}

var UbuntuPackages = []string{ //nolint:gochecknoglobals // global constant
	"linux-image-generic",
	"linux-headers-generic",
}

var UbuntuMinGUIPackages = []string{ //nolint:gochecknoglobals // global constant
	"wmctrl",
	"xdotool",
	"xubuntu-desktop",
}
