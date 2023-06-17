#!/bin/bash

WIFI_NETWORK_NAME=PiBackup
WIFI_NETWORK_PASSWORD=W1f1B0ckup
SAMBA_USER=pi
SAMBA_PASSWORD=PiUser

# Error management
set -euxo pipefail

# Warn about Raspberry Pi configuration
echo "Don't forget to use 'sudo raspi-config' to:
 * Enable i2c
 * Expand filesystem"

# Base update
echo "Updating system"
sudo apt-get update
sudo apt-get -y upgrade

# Wifi hotspot
# Based upon: https://www.raspberrypi.org/documentation/configuration/wireless/access-point.md
echo "Setting up Wifi hotspot"
curl https://raw.githubusercontent.com/t0b3/rpi-wifi/master/configure | bash -s -- -a MyAP myappass -c WifiSSID wifipass
sudo apt-get -y install dnsmasq hostapd

sudo systemctl stop dnsmasq
sudo systemctl stop hostapd

## Configure a static IP
echo "Configuring static IP"

sudo echo "denyinterfaces wlan0" >> /etc/dhcpcd.conf

echo "allow-hotplug wlan0
iface wlan0 inet static
    address 192.168.0.1
    netmask 255.255.255.0
    network 192.168.0.0" | sudo tee /etc/network/interfaces.d/hotspot.conf > /dev/null

sudo service dhcpcd restart
sudo ifdown wlan0
sudo ifup wlan0

## DHCP server
echo "Configuring DHCP server"

sudo mv /etc/dnsmasq.conf /etc/dnsmasq.conf.orig  
echo "interface=wlan0      # Use the require wireless interface - usually wlan0
  dhcp-range=192.168.0.2,192.168.0.20,255.255.255.0,24h" | sudo tee /etc/dnsmasq.conf > /dev/null

## Access point host (hostapd)
echo "Configuring hostapd"

echo "interface=wlan0" | sudo tee /etc/hostapd/hostapd.conf > /dev/null
echo "driver=nl80211" | sudo tee -a /etc/hostapd/hostapd.conf > /dev/null
echo "ssid=$WIFI_NETWORK_NAME" | sudo tee -a /etc/hostapd/hostapd.conf > /dev/null
echo "hw_mode=g" | sudo tee -a /etc/hostapd/hostapd.conf > /dev/null
echo "channel=7" | sudo tee -a /etc/hostapd/hostapd.conf > /dev/null
echo "wmm_enabled=0" | sudo tee -a /etc/hostapd/hostapd.conf > /dev/null
echo "macaddr_acl=0" | sudo tee -a /etc/hostapd/hostapd.conf > /dev/null
echo "auth_algs=1" | sudo tee -a /etc/hostapd/hostapd.conf > /dev/null
echo "ignore_broadcast_ssid=0" | sudo tee -a /etc/hostapd/hostapd.conf > /dev/null
echo "wpa=2" | sudo tee -a /etc/hostapd/hostapd.conf > /dev/null
echo "wpa_passphrase=$WIFI_NETWORK_PASSWORD" | sudo tee -a /etc/hostapd/hostapd.conf > /dev/null
echo "wpa_key_mgmt=WPA-PSK" | sudo tee -a /etc/hostapd/hostapd.conf > /dev/null
echo "wpa_pairwise=TKIP" | sudo tee -a /etc/hostapd/hostapd.conf > /dev/null
echo "rsn_pairwise=CCMP" | sudo tee -a /etc/hostapd/hostapd.conf > /dev/null

echo "DAEMON_CONF=\"/etc/hostapd/hostapd.conf\"" | sudo tee -a /etc/default/hostapd > /dev/null

sudo service hostapd start  
sudo service dnsmasq start  

# Samba (network sharing)
echo "Configuring network share"

sudo apt-get -y install samba samba-common-bin

## Create shared directory
sudo mkdir -m 1777 /share
sudo chmod g+s /share

## Configure network share
echo "[share]
Comment = Photo shared folder
Path = /share
Browseable = yes
Writeable = Yes
only guest = no
create mask = 0777
directory mask = 0777
Public = yes
Guest ok = yes" | sudo tee -a /etc/samba/smb.conf > /dev/null

## Create user account
(echo "$SAMBA_PASSWORD"; echo "$SAMBA_PASSWORD") | sudo smbpasswd -s -a $SAMBA_USER

sudo samba restart

# Install docker
echo "Installing docker"
curl -sSL https://get.docker.com | sh
sudo usermod -aG docker pi

# echo "Installing docker compose"
# sudo apt-get -y install python3 python3-pip

# sudo pip3 install docker-compose
# docker-compose --version

# GPhoto2
sudo apt-get -y install gphoto2 libgphoto2-dev libusb-1.0-0

# Pillow
sudo apt-get -y install libopenjp2-7

# USBMount with support for exFAT, NTFS and MTP
sudo apt-get -y install fuse ntfs-3g jmtpfs exfat-fuse exfat-utils

# Note that USBMount is not longer available on Debian Stretch repo, so we have to install it manually
sudo apt-get install -y debhelper build-essential git lockfile-progs
git clone https://github.com/rbrito/usbmount.git
cd ./usbmount
sudo dpkg-buildpackage -us -uc -b
cd -
sudo dpkg -i usbmount_0.0.24_all.deb
sudo apt-get install -y -f

sudo sed -i "/FS_MOUNTOPTIONS=\"\"/c\FS_MOUNTOPTIONS=\"-fstype=vfat,flush,gid=plugdev,dmask=0007,fmask=0117\"" /etc/usbmount/usbmount.conf
# FILESYSTEMS="vfat ext2 ext3 ext4 hfsplus exfat ntfs fuseblk"

#echo ".include /usr/lib/systemd/system/systemd-udevd.service
#[Service]
#MountFlags=shared" | sudo tee /etc/systemd/system/systemd-udevd.service > /dev/null

#sudo service systemd-udevd restart

# Install application
sudo apt-get -y install libjpeg-dev zlib1g-dev python3 python3-dev python3-smbus python3-pip git rsync
git clone https://github.com/bezineb5/PiBackup.git
mv ./PiBackup pibackup
cd ./pibackup

sudo pip3 install -r requirements.txt
sudo groupadd plugdev
sudo usermod -aG plugdev pi

docker compose -f ./lychee/docker-compose.yml pull
docker compose -f ./lychee/docker-compose.yml up -d

crontab -l | { cat; echo "@reboot `pwd`/start.sh"; } | crontab
