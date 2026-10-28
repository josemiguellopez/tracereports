"""La grabación offline lleva la evidencia sin redactar: solo el usuario la lee (0700/0600)."""
import os
import stat

import pytest

from tracereports.offline import Recorder

pytestmark = pytest.mark.skipif(os.name == "nt", reason="sin permisos POSIX")


def test_recording_is_private(tmp_path):
    directory = tmp_path / "grabacion"
    r = Recorder(str(directory))
    r.send_now("POST", "/api/v1/runs", b'{"name":"x"}', "application/json", 0)
    r.send_now("POST", "/api/v1/tests/-1/screenshot", b"\x89PNG secreto", "image/png", 0)
    r.close()
    seen = 0
    for root, dirs, files in os.walk(directory):
        for d in dirs:
            assert stat.S_IMODE(os.stat(os.path.join(root, d)).st_mode) == 0o700, d
        for f in files:
            assert stat.S_IMODE(os.stat(os.path.join(root, f)).st_mode) == 0o600, f
            seen += 1
    assert stat.S_IMODE(os.stat(directory).st_mode) == 0o700
    assert seen >= 3
