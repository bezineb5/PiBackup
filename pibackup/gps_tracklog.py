import logging
from datetime import datetime
from pathlib import Path

import gt2gpx
import usb1
from werkzeug.utils import secure_filename

log = logging.getLogger(__name__)


def is_device_supported(device: usb1.USBDevice) -> bool:
    """Check if the GPS device is recognized as a supported device."""
    # iGotU GPS devices
    if (
        device.getVendorID() == gt2gpx.connections.VENDOR_ID
        and device.getProductID() == gt2gpx.connections.PRODUCT_ID
    ):
        return True
    return False


def download_gps_tracks(target_dir: Path) -> None:
    """
    Download GPS tracks from all connected GPS devices
    """
    log.info("Downloading GPS tracks to %s", target_dir)
    target_dir.mkdir(parents=True, exist_ok=True)

    _igotu_download_track(_generate_gpx_filename(target_dir, "iGotU"))

    log.info("Finished downloading GPS tracks")


def _generate_gpx_filename(target_dir: Path, device_name: str) -> Path:
    """
    Generate a unique filename for the GPS track
    """
    safe_device_name = secure_filename(device_name)
    current_time = datetime.now().strftime("%Y%m%d_%H%M%S")
    filename = f"{safe_device_name}_{current_time}.gpx"
    return target_dir / filename


def _igotu_download_track(destination_file: Path) -> None:
    """
    Download a GPS track from a iGotU GPS device to a file.
    """

    # Connection
    log.info("Downloading iGotU GPS track...")
    gt2gpx.connections.SLOW_TIMEOUT = 10000
    connection = gt2gpx.connections.get_connection(
        gt2gpx.connections.CONNECTION_TYPE_USB
    )
    gt2gpx.download_track(connection, destination_file)
    log.info("Downloaded iGotU GPS track to: %s", destination_file)
