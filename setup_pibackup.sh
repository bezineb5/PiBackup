#!/bin/bash

WIFI_NETWORK_NAME=PiBackup
WIFI_NETWORK_PASSWORD=W1f1B0ckup
SAMBA_USER=pi
SAMBA_PASSWORD=PiUser

# Error management
set -o errexit
set -o pipefail
set -o nounset

# Must be on Debian Jessie
if [ `lsb_release --release --short` != "8.0" ]; then
    echo "It currently (2017-11-01) only works with Raspbian Jessie, because of Docker";
    exit -1;
fi

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

sudo /etc/init.d/samba restart

# Install docker
echo "Installing docker"
curl -sSL https://get.docker.com | sh
sudo usermod -aG docker pi

echo "Installing docker compose"
sudo apt-get -y install python

wget https://bootstrap.pypa.io/get-pip.py
sudo python get-pip.py
sudo python3 get-pip.py
rm get-pip.py

sudo pip install docker-compose
docker-compose --version

# GPhoto2
sudo apt-get -y install gphoto2 gphotofs

# USBMount with support for NTFS and MTP
sudo apt-get -y install usbmount fuse ntfs-3g jmtpfs

sudo sed -i "/FS_MOUNTOPTIONS=\"\"/c\FS_MOUNTOPTIONS=\"-fstype=vfat,flush,gid=plugdev,dmask=0007,fmask=0117\"" /etc/usbmount/usbmount.conf
# FILESYSTEMS="vfat ext2 ext3 ext4 hfsplus jmtpfs ntfs fuseblk"

echo ".include /usr/lib/systemd/system/systemd-udevd.service
[Service]
MountFlags=shared" | sudo tee /etc/systemd/system/systemd-udevd.service > /dev/null

sudo service systemd-udevd restart

# Install application
sudo apt-get -y install libjpeg-dev zlib1g-dev python3 python3-dev python3-smbus git rsync

sudo pip3 install -r requirements.txt
sudo groupadd plugdev
sudo usermod -aG plugdev pi

cd lychee
docker-compose pull
docker-compose -d up
cd ..

crontab -l | { cat; echo "@reboot `pwd`/start.sh"; } | crontab
