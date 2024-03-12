import logging
import os
import pathlib

import gphoto2 as gp
import werkzeug

log = logging.getLogger(__name__)


def rsync_all_cameras(target_path: pathlib.Path) -> int:
    """
    Backup all cameras to the target directory
    """
    cameras = gp.check_result(gp.gp_camera_autodetect())

    number_of_copies = 0
    for index, (name, addr) in enumerate(cameras):
        log.info("Camera[%d] = %s, %s", index, name, addr)

        log.info("Starting backup for %s to %s", name, target_path)
        target_camera_path = target_path.joinpath(_get_unique_id(name))
        camera = _get_camera(addr)
        copies_for_camera = rsync_camera(camera, target_camera_path)
        camera.exit()
        log.info("Finished backup for %s, %s files copied", name, copies_for_camera)
        number_of_copies += copies_for_camera

    return number_of_copies


def _get_camera(address) -> gp.Camera:
    camera = gp.Camera()
    # search ports for camera port name
    port_info_list = gp.PortInfoList()
    port_info_list.load()
    idx = port_info_list.lookup_path(address)
    camera.set_port_info(port_info_list[idx])
    camera.init()

    return camera


def _get_unique_id(camera_name: str) -> str:
    return werkzeug.utils.secure_filename(camera_name)


def _enumerate_camera_dir(camera, path: pathlib.Path):
    # List files in that directory
    for name, _ in camera.folder_list_files(str(path)):
        yield path.joinpath(name)

    # Read subdirectories
    for name, _ in camera.folder_list_folders(str(path)):
        folder_path = path.joinpath(name)
        for file_path in _enumerate_camera_dir(camera, folder_path):
            yield file_path


def rsync_camera(camera, target_path: pathlib.Path) -> int:
    """
    Backup the specified camera to the target directory
    """
    number_of_copies = 0
    start_path = pathlib.Path("/")

    for file_path in _enumerate_camera_dir(camera, start_path):
        relative_path = pathlib.Path("." + str(file_path))
        destination_path = target_path.joinpath(relative_path)

        if not destination_path.exists():
            # Create the folder
            os.makedirs(str(destination_path.parent), exist_ok=True)

            # Copy the file
            log.info("Copy %s to %s", file_path, destination_path)
            folder, name = os.path.split(str(file_path))
            camera_file = camera.file_get(folder, name, gp.GP_FILE_TYPE_NORMAL)
            camera_file.save(str(destination_path))
            number_of_copies += 1

    return number_of_copies
