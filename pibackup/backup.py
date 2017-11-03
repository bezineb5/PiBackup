import logging
import os
import random
import signal
import string
import threading
import time
from functools import wraps
from datetime import datetime, timedelta

import sh
import touchphat
from lycheesync.sync import perform_sync
from watchdog.events import FileSystemEventHandler
from watchdog.observers import Observer

OBSERVE_SD_PATH = "/var/run/usbmount"
BACKUP_PATH = "/share"
LYCHEE_DATA_PATH = "/data/lychee"
LYCHEESYNC_CONF_FILE = os.path.join(os.getcwd(), "lychee/lycheesync.conf")

UNIQUE_ID_FILE = "unique.id"
DEFAULT_UNIQUE_ID = "drive"

BUTTON_POWER = "Back"
BUTTON_RSYNC = "A"
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

        perform_backup(event.src_path)


class SharedDirectoryWatcher(FileSystemEventHandler):
    def on_any_event(self, event):
        """Catch-all event handler.

        :param event:
            Event representing file/directory creation.
        :type event:
            :class:`DirCreatedEvent` or :class:`FileCreatedEvent`
        """

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
    unique_id = DEFAULT_UNIQUE_ID

    try:
        id_path = os.path.join(source_path, UNIQUE_ID_FILE)
        if os.path.exists(id_path):
            with open(id_path, 'r') as f:
                file_unique_id = f.readline()
                keepcharacters = (' ','.','_')
                file_unique_id = "".join(c for c in file_unique_id if c.isalnum() or c in keepcharacters).rstrip()
                if len(file_unique_id) > 0:
                    return file_unique_id

        unique_id = ''.join(random.choice(string.ascii_uppercase + string.digits) for _ in range(6))
        with open(id_path, 'w') as f:
            f.write(unique_id)
    except:
        pass

    return unique_id


@blink(BUTTON_RSYNC)
@no_parallel_run
def perform_backup(source_path):
    if source_path is None:
        return

    unique_id = get_unique_name(source_path)
    destination_path = os.path.join(BACKUP_PATH, unique_id) + os.sep

    log.info("Starting backup for %s to %s", source_path, destination_path)
    # File synchronization
    sh.rsync("-a", "--chmod=Du=rwx,Dgo=rwx,Fu=rw,Fog=rw", source_path + os.sep, destination_path)
    # Flush disk buffers
    sh.sync()
    log.info("Finished backup for %s", source_path)


@blink(BUTTON_LYCHEE_SYNC)
@no_parallel_run
def sync_lychee():
    log.info("Starting Lychee synchronization")

    try:
        perform_sync(False, 'normal',False, False, True, False, BACKUP_PATH, LYCHEE_DATA_PATH, LYCHEESYNC_CONF_FILE)
    except Exception as e:
        log.exception("Unable to perform Lychee synchronization")

    # Flush disk buffers
    sh.sync()
    log.info("Finished Lychee synchronization")


@touchphat.on_release(BUTTON_LYCHEE_SYNC)
def handle_release(event):
    sync_lychee()


@blink(BUTTON_POWER)
def wait_blink(delay):
    time.sleep(delay)


@long_press(BUTTON_POWER, 1.5, default_state=True)
def handle_touch(event):
    wait_blink(2.0)
    with sh.contrib.sudo:
        sh.shutdown("-h", "now")


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


def main():
    log.info("Starting PiBackup")

    global next_lychee_sync
    observer = get_observer_for_cards()
    observer_share = get_observer_for_share()

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

    try:
        while True:
            time.sleep(1)
            if next_lychee_sync and next_lychee_sync < datetime.now():
                next_lychee_sync = None
                sync_lychee()
    except KeyboardInterrupt:
        pass

    exit_gracefully(None, None)


if __name__ == "__main__":
    logging.basicConfig(level=logging.INFO,
                        format='%(asctime)s - %(message)s',
                        datefmt='%Y-%m-%d %H:%M:%S')

    main()
