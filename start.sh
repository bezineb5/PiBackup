#!/bin/sh

# Fix: the /var/run/usbmount directory must exist
# This command will return an error, but will create the directory
sudo /usr/share/usbmount/usbmount

# Error management
set -o errexit
#set -o pipefail
set -o nounset

# Fix for locale issues
LANGUAGE="en_GB.UTF-8"
LC_ALL="en_GB.UTF-8"
LC_CTYPE="en_GB.UTF-8"

# Start the application
cd "$(dirname "$0")"
python3 ./pibackup/backup.py --no-lychee-sync
