#!/bin/sh

# Error management
set -o errexit
#set -o pipefail
set -o nounset

# Fix for locale issues
LANGUAGE="en_GB.UTF-8"
LC_ALL="en_GB.UTF-8"
LC_CTYPE="en_GB.UTF-8"

cd "$(dirname "$0")"

python3 ./pibackup/backup.py
