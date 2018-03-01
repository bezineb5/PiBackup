import logging
import os
import pathlib

import gphoto2 as gp
import werkzeug

log = logging.getLogger(__name__)

gp.check_result(gp.use_python_logging())

def rsync_all_cameras(target_dir):
    target_path = pathlib.Path(target_dir)
    cameras = gp.check_result(gp.gp_camera_autodetect())

    n = 0
    for name, addr in cameras:
        log.info("Camera[{index}] = {name}, {address}".format(index=n, name=name, address=addr))
        n += 1

        target_camera_path = target_path.joinpath(_get_unique_id(name))
        camera = _get_camera(addr)
        rsync_camera(camera, target_camera_path)
        camera.exit()


def _get_camera(address):
    camera = gp.Camera()
    # search ports for camera port name
    port_info_list = gp.PortInfoList()
    port_info_list.load()
    idx = port_info_list.lookup_path(address)
    camera.set_port_info(port_info_list[idx])
    camera.init()

    return camera


def _get_unique_id(camera_name):
    return werkzeug.utils.secure_filename(camera_name)


def _enumerate_camera_dir(camera, path):
    # List files in that directory
    for name, _ in camera.folder_list_files(str(path)):
        yield path.joinpath(name)

    # Read subdirectories
    for name, _ in camera.folder_list_folders(str(path)):
        folder_path = path.joinpath(name)
        for file_path in _enumerate_camera_dir(camera, folder_path):
            yield file_path

    
def rsync_camera(camera, target_path):
    log.info("Starting backup for %s to %s", camera, target_path)

    start_path = pathlib.Path('/')

    for file_path in _enumerate_camera_dir(camera, start_path):
        relative_path = pathlib.Path('.' + str(file_path))
        destination_path = target_path.joinpath(relative_path)

        if not destination_path.exists():
            # Create the folder
            os.makedirs(str(destination_path.parent), exist_ok=True)

            log.info("Copy %s to %s", file_path, destination_path)
            folder, name = os.path.split(str(file_path))
            camera_file = camera.file_get(folder, name, gp.GP_FILE_TYPE_NORMAL)
            camera_file.file_save(str(destination_path))


    log.info("Finished backup for %s", camera)


def get_camera_file_info(camera, path):
    folder, name = os.path.split(path)
    return gp.check_result(
        gp.gp_camera_file_get_info(camera, folder, name))