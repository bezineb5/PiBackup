import logging
import os
import random
import signal
import string
import threading
import time
from datetime import datetime, timedelta
from functools import wraps

import sh
import touchphat
import usb1
import werkzeug
from lycheesync.sync import perform_sync
from watchdog.events import FileSystemEventHandler
from watchdog.observers import Observer

import ptp_copy

OBSERVE_SD_PATH = "/var/run/usbmount"
BACKUP_PATH = "/share"
LYCHEE_DATA_PATH = "/data/lychee"
LYCHEESYNC_CONF_FILE = os.path.join(os.getcwd(), "lychee/lycheesync.conf")
LOG_DIRECTORY = os.path.join(BACKUP_PATH, "logs")

UNIQUE_ID_FILE = "unique.id"
DEFAULT_UNIQUE_ID = "drive"

BUTTON_POWER = "Back"
BUTTON_RSYNC = "A"
BUTTON_GPHOTO2_SYNC = "B"
BUTTON_LYCHEE_SYNC = "D"

log = logging.getLogger(__name__)
exiting = False
next_lychee_sync = None


class SDCardWatcher(FileSystemEventHandler):
    def on_created(self, event):
        """Called when a file or directory is created.

        :param event:
            Event representing file/directory creation.
        :type event:
            :class:`DirCreatedEvent` or :class:`FileCreatedEvent`
        """

        if event is None or (not event.is_directory and not os.path.islink(event.src_path)):
            return

        mass_storage_backup(event.src_path)


class SharedDirectoryWatcher(FileSystemEventHandler):

    def __init__(self):
        self._exclude_path = LOG_DIRECTORY + os.sep
        self._exclude = LOG_DIRECTORY

    def on_any_event(self, event):
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

        global next_lychee_sync
        next_lychee_sync = datetime.now() + timedelta(seconds=5)


def no_parallel_run(func):
    lock = threading.Lock()

    @wraps(func)
    def func_wrapper(*args, **kwargs):
        if lock.acquire(blocking=False):
            try:
                return func(*args, **kwargs)
            finally:
                lock.release()

    return func_wrapper


def blink(led_id):
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
    def long_press_decorator(func):
        start_time = None

        @touchphat.on_touch(button_id)
        def handle_touch(event):
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
    unique_id = None

    try:
        # First try to use the UID stored on the drive
        id_path = os.path.join(source_path, UNIQUE_ID_FILE)
        if os.path.exists(id_path):
            with open(id_path, 'r') as f:
                file_unique_id = f.readline()
                file_unique_id = werkzeug.utils.secure_filename(file_unique_id)
                if file_unique_id:
                    return file_unique_id

        # Otherwise, generate one and try to store it on the device
        random_string = ''.join(random.choice(string.ascii_uppercase + string.digits) for _ in range(6))
        unique_id = werkzeug.utils.secure_filename(random_string)
        with open(id_path, 'w') as f:
            f.write(unique_id)
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
    if source_path is None:
        return

    unique_id = get_unique_name(source_path)
    destination_path = os.path.join(BACKUP_PATH, unique_id) + os.sep

    # Create the folder
    os.makedirs(destination_path, exist_ok=True)

    log.info("Starting backup for %s to %s", source_path, destination_path)
    # File synchronization
    sh.rsync("-a", "--chmod=Du=rwx,Dgo=rwx,Fu=rw,Fog=rw", source_path + os.sep, destination_path)
    # Flush disk buffers
    sh.sync()
    log.info("Finished backup for %s", source_path)

    # Schedule a lychee sync for now
    global next_lychee_sync
    next_lychee_sync = datetime.now()


@blink(BUTTON_LYCHEE_SYNC)
@no_parallel_run
def sync_lychee(complete_sync=False):
    log.info("Starting Lychee synchronization")

    if complete_sync:
        # Delete broken links
        log.info("Removing broken symlinks for Lychee")
        sh.find(LYCHEE_DATA_PATH, "-xtype", "l", "-delete")
        exclusive_mode = 'replace'
    else:
        exclusive_mode = 'normal'

    try:
        perform_sync(False, exclusive_mode, True, False, True, False, BACKUP_PATH, LYCHEE_DATA_PATH, LYCHEESYNC_CONF_FILE)
    except Exception as e:
        log.exception("Unable to perform Lychee synchronization")

    # Flush disk buffers
    sh.sync()
    log.info("Finished Lychee synchronization")


@touchphat.on_release(BUTTON_LYCHEE_SYNC)
def handle_release(event):
    sync_lychee(complete_sync=True)


@blink(BUTTON_POWER)
def wait_blink(delay):
    time.sleep(delay)


@long_press(BUTTON_POWER, 1.5, default_state=True)
def handle_touch(event):
    wait_blink(2.0)
    with sh.contrib.sudo:
        sh.shutdown("--poweroff", "now")


def get_observer_for_cards():
    path = OBSERVE_SD_PATH
    event_handler = SDCardWatcher()

    observer = Observer()
    observer.schedule(event_handler, path, recursive=False)
    observer.start()
    return observer


def get_observer_for_share():
    path = BACKUP_PATH
    event_handler = SharedDirectoryWatcher()

    observer = Observer()
    observer.schedule(event_handler, path, recursive=True)
    observer.start()
    return observer


@blink(BUTTON_GPHOTO2_SYNC)
@no_parallel_run
def gphoto_backup(device):
    if device is None:
        return

    ptp_copy.rsync_all_cameras(BACKUP_PATH)

    # Flush disk buffers
    sh.sync()

    # Schedule a lychee sync for now
    global next_lychee_sync
    next_lychee_sync = datetime.now()
 

def hotplug_callback(context, device, event):
    log.info("Device %s: %s" % (
        {
            usb1.HOTPLUG_EVENT_DEVICE_ARRIVED: 'arrived',
            usb1.HOTPLUG_EVENT_DEVICE_LEFT: 'left',
        }[event],
        device,
    ))
    # Note: cannot call synchronous API in this function.

    if event == usb1.HOTPLUG_EVENT_DEVICE_ARRIVED:
        thread = threading.Thread(target = gphoto_backup, args = (device, ))
        thread.start()


def monitor_usb_devices():
    with usb1.USBContext() as context:
        if not context.hasCapability(usb1.CAP_HAS_HOTPLUG):
            log.error('Hotplug support is missing. Please update your libusb version.')
            return
        log.info('Registering hotplug callback...')
        opaque = context.hotplugRegisterCallback(hotplug_callback)
        log.info('Callback registered. Monitoring events, ^C to exit')
        try:
            while not exiting:
                context.handleEvents()
        except (KeyboardInterrupt, SystemExit):
            log.info('Exiting')


def main():
    log.info("Starting PiBackup")

    global next_lychee_sync
    observer = get_observer_for_cards()
    observer_share = get_observer_for_share()
    monitor_usb_devices()

    touchphat.led_on(BUTTON_POWER)

    def exit_gracefully(signum, frame):
        global exiting

        if exiting:
            return
        exiting = True
        log.info("Stopping PiBackup")
        observer.stop()
        observer_share.stop()
        observer.join(2.0)
        observer_share.join(2.0)
        touchphat.all_off()
        log.info("Stopped PiBackup")
        exit(-1)

    signal.signal(signal.SIGINT, exit_gracefully)
    signal.signal(signal.SIGTERM, exit_gracefully)

    while True:
        try:
            time.sleep(1)
            if next_lychee_sync and next_lychee_sync < datetime.now():
                next_lychee_sync = None
                sync_lychee()
        except (KeyboardInterrupt, SystemExit):
            break
        except:
            log.exception("Unable to synchronize Lychee")

    exit_gracefully(None, None)


def _init_logging(directory):
    if not os.path.exists(directory):
        os.makedirs(directory)

    log_file = os.path.join(directory, "pibackup.log")
    handler = logging.handlers.TimedRotatingFileHandler(log_file,
                                                        when="d",
                                                        interval=1,
                                                        backupCount=60)

    logging.basicConfig(level=logging.INFO,
                        format='%(asctime)s - %(message)s',
                        datefmt='%Y-%m-%d %H:%M:%S',
                        handlers=[handler])

if __name__ == "__main__":
    try:
        _init_logging(LOG_DIRECTORY)
        main()
    except:
        log.exception("Unable to start PiBackup")
