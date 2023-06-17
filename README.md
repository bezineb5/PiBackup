# PiBackup

Build a photo backup disk with a Raspberry Pi!

Features:
* Automatically copy the content of a memory card to the internal storage
* Incremental copy (thanks to rsync)
* Wifi hotspot
* Integrated image gallery
* Shared disk to retrieve your photos from the network

## Usage

### Backup
Plug in the memory card reader into a USB port, and the copy will start.

### Hotspot
Connect to the wifi hotspot from your PC or mobile device (name and password defined at the creation of the device).

You'll can then connect to the shared drive to download your files.
You can also connect to the image gallery, powered by [Lychee](https://github.com/electerious/Lychee)

### Configuration of the directory name
You can specify a name to use for the backup directory by creating a file named `unique.id` at the root of the memory card. The content of the file will be used as the name of the directory.

### Button features
The buttons are used to control the device:
* Long press (1.5 seconds) on the Back button: shutdown the device
* Button A: manually start a backup of the SD card. This should be unnecessary as the backup is automatic, but it can be useful if you want to force a backup.
* Button B: start PTP sync.
* Button D: start the image gallery synchronization.

## Installation

Follow this: [https://github.com/rbrito/usbmount/issues/25#issuecomment-550534754](https://github.com/rbrito/usbmount/issues/25#issuecomment-550534754)

Configure `FILESYSTEMS="vfat ext2 ext3 ext4 hfsplus exfat ntfs fuseblk"` in `/etc/usbmount/usbmount.conf`