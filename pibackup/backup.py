import argparse
import logging
import os
import pathlib
import random
import signal
import string
import threading
import time
from logging import handlers
from datetime import datetime, timedelta
from functools import wraps
from typing import Optional

import sh
import touchphat
import usb1
import werkzeug
from watchdog.events import FileSystemEventHandler
from watchdog.observers import Observer

import gps_tracklog
import ptp_copy

OBSERVE_SD_PATH = "/var/run/usbmount"
BACKUP_PATH = "/share"
GPS_BACKUP_PATH = pathlib.Path(BACKUP_PATH) / "gps"
LYCHEE_DATA_PATH = "/data/lychee"
LYCHEESYNC_CONF_FILE = os.path.join(os.getcwd(), "lychee/lycheesync.conf")
LOG_DIRECTORY = os.path.join(BACKUP_PATH, "logs")
LOG_FILENAME = "pibackup.log"

UNIQUE_ID_FILE = "unique.id"
DEFAULT_UNIQUE_ID = "drive"

BUTTON_POWER = "Back"
BUTTON_RSYNC = "A"
BUTTON_GPHOTO2_SYNC = "B"
BUTTON_GPS_SYNC = "C"
BUTTON_LYCHEE_SYNC = "D"

log = logging.getLogger(__name__)
exiting = threading.Event()
# The next Lychee synchronization is scheduled at this time. It must be atomically modified
next_lychee_sync = None
next_lychee_sync_lock = threading.Lock()
enable_lychee_sync = True


class SDCardWatcher(FileSystemEventHandler):
    """
    Watchdog event handler for SD card insertion/removal.
    """

    def on_created(self, event):
        """Called when a file or directory is created.

        :param event:
            Event representing file/directory creation.
        :type event:
            :class:`DirCreatedEvent` or :class:`FileCreatedEvent`
        """

        if event is None or (
            not event.is_directory and not os.path.islink(event.src_path)
        ):
            return

        mass_storage_backup(event.src_path)


class SharedDirectoryWatcher(FileSystemEventHandler):
    """
    Watchdog event handler for shared directory changes.
    """

    def __init__(self):
        self._exclude_path = LOG_DIRECTORY + os.sep
        self._exclude = LOG_DIRECTORY

    def on_created(self, event):
        """Catch-all event handler.

        :param event:
            Event representing file/directory creation.
        :type event:
            :class:`DirCreatedEvent` or :class:`FileCreatedEvent`
        """

        # Ignore logging
        if event:
            path = event.src_path
            if path:
                if path.startswith(self._exclude_path) or path == self._exclude:
                    return

        schedule_sync(20)


def schedule_sync(in_seconds=0):
    """
    Schedule a synchronization with Lychee.
    """
    with next_lychee_sync_lock:
        global next_lychee_sync
        next_lychee_sync = datetime.now() + timedelta(seconds=in_seconds)
        log.info("Scheduling a Lychee synchronization at %s", next_lychee_sync)


def _pop_schedule() -> bool:
    """
    Pop the next schedule and return it.
    """
    with next_lychee_sync_lock:
        global next_lychee_sync
        if next_lychee_sync and next_lychee_sync < datetime.now():
            next_lychee_sync = None
            return True
    return False


def no_parallel_run(func):
    """
    Decorator to prevent a function from running in parallel.
    """
    lock = threading.Lock()

    @wraps(func)
    def func_wrapper(*args, **kwargs):
        if lock.acquire(blocking=False):
            try:
                return func(*args, **kwargs)
            finally:
                lock.release()
        else:
            log.info("Skipping %s, already running", func.__name__)
            return None

    return func_wrapper


def blink(led_id):
    """
    Decorator to blink a button's LED while the function is running.
    """

    def blink_decorator(func):
        @wraps(func)
        def func_wrapper(*args, **kwargs):
            def worker():
                return func(*args, **kwargs)

            led_state = True
            t = threading.Thread(target=worker, daemon=True)
            t.start()

            # Make the button blink
            while t.is_alive():
                touchphat.set_led(led_id, led_state)
                led_state = not led_state
                t.join(0.5)

            # After blinking, turn the LED off
            touchphat.led_off(led_id)

        return func_wrapper

    return blink_decorator


def long_press(button_id, delay, default_state=False):
    """
    Decorator to handle long press events on a physical button.
    """

    def long_press_decorator(func):
        start_time = None

        @touchphat.on_touch(button_id)
        def handle_touch(_event):
            nonlocal start_time
            start_time = time.time()

        @touchphat.on_release(button_id)
        def handle_release(event):
            touchphat.set_led(button_id, default_state)

            if start_time is None:
                return
            elapsed_time = time.time() - start_time
            if elapsed_time >= delay:
                return func(event)

        @wraps(func)
        def func_wrapper(*args, **kwargs):
            return func(*args, **kwargs)

        return func_wrapper

    return long_press_decorator


def get_unique_name(source_path):
    """
    Get a unique name for the backup directory.
    It can be either a unique ID stored on the drive or a random ID.
    It will be used as the name of the backup directory.
    """
    unique_id = None

    try:
        # First try to use the UID stored on the drive
        id_path = os.path.join(source_path, UNIQUE_ID_FILE)
        if os.path.exists(id_path):
            with open(id_path, "r", encoding="utf-8") as file:
                file_unique_id = file.readline()
                file_unique_id = werkzeug.utils.secure_filename(file_unique_id)
                if file_unique_id:
                    return file_unique_id

        # Otherwise, generate one and try to store it on the device
        random_string = "".join(
            random.choice(string.ascii_uppercase + string.digits) for _ in range(6)
        )
        unique_id = werkzeug.utils.secure_filename(random_string)
        with open(id_path, "w", encoding="utf-8") as file:
            file.write(unique_id)
    except:
        log.warning("Unable to generate a unique ID, using default", exc_info=True)
        unique_id = None

    # Everything failed
    if not unique_id:
        unique_id = DEFAULT_UNIQUE_ID

    return unique_id


@blink(BUTTON_RSYNC)
@no_parallel_run
def mass_storage_backup(source_path):
    """
    Backup a mass storage device.
    """
    if source_path is None:
        return

    unique_id = get_unique_name(source_path)
    destination_path = os.path.join(BACKUP_PATH, unique_id) + os.sep

    # Create the folder
    os.makedirs(destination_path, exist_ok=True)

    log.info("Starting backup for %s to %s", source_path, destination_path)
    # File synchronization
    sh.rsync(
        "-a",
        "--chmod=Du=rwx,Dgo=rwx,Fu=rw,Fog=rw",
        source_path + os.sep,
        destination_path,
    )
    # Flush disk buffers
    sh.sync()
    # Change date
    sh.touch(destination_path)
    log.info("Finished backup for %s", source_path)

    # Schedule a lychee sync for now
    schedule_sync()


@blink(BUTTON_LYCHEE_SYNC)
@no_parallel_run
def sync_lychee(complete_sync=False):
    """
    Synchronize Lychee with the backup.
    """
    from lycheesync.sync import perform_sync

    if not enable_lychee_sync:
        log.info("Lychee synchronization is disabled")
        return
    log.info("Starting Lychee synchronization")

    if complete_sync:
        # Delete broken links
        log.info("Removing broken symlinks for Lychee")
        sh.find(LYCHEE_DATA_PATH, "-xtype", "l", "-delete")
        exclusive_mode = "replace"
    else:
        exclusive_mode = "normal"

    try:
        perform_sync(
            False,
            exclusive_mode,
            True,
            False,
            True,
            False,
            BACKUP_PATH,
            LYCHEE_DATA_PATH,
            LYCHEESYNC_CONF_FILE,
        )
    except Exception as e:
        log.exception("Unable to perform Lychee synchronization")

    # Flush disk buffers
    sh.sync()
    log.info("Finished Lychee synchronization")


@touchphat.on_release(BUTTON_LYCHEE_SYNC)
def handle_lychee_sync_release(_event):
    """
    Handle the release of the Lychee sync button.
    """
    sync_lychee(complete_sync=True)


@touchphat.on_release(BUTTON_GPS_SYNC)
def handle_gps_sync_release(_event):
    """
    Handle the release of the GPS track sync button.
    """
    gps_backup()


@blink(BUTTON_POWER)
def wait_blink_power(delay):
    """
    Blink the power button for a given delay.
    """
    time.sleep(delay)


@long_press(BUTTON_POWER, 1.5, default_state=True)
def handle_touch_power(_event):
    """
    Handle the touch of the power button.
    """
    # Start stopping all the listeners
    exit_gracefully()
    wait_blink_power(2.0)

    # Shutdown the system
    with sh.contrib.sudo:
        sh.shutdown("--poweroff", "now")


def _get_observer_for_cards():
    path = OBSERVE_SD_PATH
    event_handler = SDCardWatcher()

    observer = Observer()
    observer.schedule(event_handler, path, recursive=False)
    observer.start()
    return observer


def _get_observer_for_share():
    path = BACKUP_PATH
    event_handler = SharedDirectoryWatcher()

    observer = Observer()
    observer.schedule(event_handler, path, recursive=True)
    observer.start()
    return observer


@blink(BUTTON_GPHOTO2_SYNC)
@no_parallel_run
def gphoto_backup(device):
    """
    Backup a gphoto2 device.
    """
    if device is None:
        return

    number_of_copies = ptp_copy.rsync_all_cameras(pathlib.Path(BACKUP_PATH))

    # No file copied
    if number_of_copies <= 0:
        return

    # Flush disk buffers
    sh.sync()

    # Schedule a lychee sync for now
    schedule_sync()


@blink(BUTTON_GPS_SYNC)
@no_parallel_run
def gps_backup():
    """
    Backup GPS tracks.
    """
    gps_tracklog.download_gps_tracks(GPS_BACKUP_PATH)


DEVICE_EVENTS_LABELS = {
    usb1.HOTPLUG_EVENT_DEVICE_ARRIVED: "arrived",
    usb1.HOTPLUG_EVENT_DEVICE_LEFT: "left",
}


def hotplug_callback(_context: usb1.USBContext, device: usb1.USBDevice, event):
    """
    Callback for hotplug events.
    """
    log.info("Device %s: %s", DEVICE_EVENTS_LABELS[event], device)
    # Note: cannot call synchronous API in this function.

    if event == usb1.HOTPLUG_EVENT_DEVICE_ARRIVED:
        if gps_tracklog.is_device_supported(device):
            log.info("Starting GPS backup")
            thread = threading.Thread(target=gps_backup)
            thread.start()
        else:
            thread = threading.Thread(target=gphoto_backup, args=(device,))
            thread.start()


def _monitor_usb_devices():
    thread = threading.Thread(target=_monitor_usb_devices_thread)
    thread.start()


def _monitor_usb_devices_thread():
    with usb1.USBContext() as context:
        if not context.hasCapability(usb1.CAP_HAS_HOTPLUG):
            log.error("Hotplug support is missing. Please update your libusb version.")
            return
        log.info("Registering hotplug callback...")
        context.hotplugRegisterCallback(hotplug_callback)
        log.info("Callback registered. Monitoring events, ^C to exit")
        try:
            while not exiting.is_set():
                context.handleEvents()
        except (KeyboardInterrupt, SystemExit):
            log.info("Exiting")


def exit_gracefully(_signum=None, _frame=None):
    """
    Exit gracefully.
    """
    exiting.set()


def main():
    """
    Main function.
    """
    args = _parse_arguments()
    log_level = logging.DEBUG if args.debug else logging.INFO
    log_dest = None if args.stdout else LOG_DIRECTORY
    _init_logging(log_dest, log_level)

    log.info("Starting PiBackup")
    global enable_lychee_sync
    enable_lychee_sync = not args.disable_lychee_sync

    observer = _get_observer_for_cards()
    observer_share = _get_observer_for_share()
    _monitor_usb_devices()

    touchphat.led_on(BUTTON_POWER)

    signal.signal(signal.SIGINT, exit_gracefully)
    signal.signal(signal.SIGTERM, exit_gracefully)

    while not exiting.is_set():
        try:
            if _pop_schedule():
                sync_lychee()
        except (KeyboardInterrupt, SystemExit):
            break
        except:
            log.exception("Unable to synchronize Lychee")
        finally:
            time.sleep(1)

    exit_gracefully()

    log.info("Stopping PiBackup")
    observer.stop()
    observer_share.stop()
    observer.join(2.0)
    observer_share.join(2.0)
    touchphat.all_off()
    log.info("Stopped PiBackup")


def _parse_arguments():
    parser = argparse.ArgumentParser(description="PiBackup")
    parser.add_argument(
        "-n",
        "--no-lychee-sync",
        dest="disable_lychee_sync",
        action="store_true",
        default=False,
        help="Do not synchronize Lychee",
    )
    parser.add_argument(
        "-d", "--debug", action="store_true", default=False, help="Show debug output"
    )
    parser.add_argument(
        "-s",
        "--stdout",
        action="store_true",
        default=False,
        help="Send log to stdout instead of a file",
    )

    return parser.parse_args()


def _init_logging(directory: Optional[str], level: int = logging.INFO):
    if not directory:
        logging.basicConfig(
            level=level,
            format="%(asctime)s - %(message)s",
            datefmt="%Y-%m-%d %H:%M:%S",
        )
        return

    if not os.path.exists(directory):
        os.makedirs(directory)

    log_file = os.path.join(directory, LOG_FILENAME)
    handler = handlers.TimedRotatingFileHandler(
        log_file, when="d", interval=1, backupCount=60
    )

    logging.basicConfig(
        level=level,
        format="%(asctime)s - %(message)s",
        datefmt="%Y-%m-%d %H:%M:%S",
        handlers=[handler],
    )


if __name__ == "__main__":
    main()
