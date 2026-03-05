import json
import os
import sys
from io import StringIO
from unittest.mock import patch

import pytest
from app import Example
from phenix_apps.common.logger import logger as phenix_logger


@pytest.fixture(autouse=True)
def mock_log_file():
    """Redirects file logging to stderr for all tests to avoid file I/O."""
    with patch("phenix_apps.common.settings.PHENIX_LOG_FILE", "stderr"):
        yield


def test_app_modification():
    # Minimal valid experiment JSON
    input_data = {
        "spec": {
            "experimentName": "test-exp",
            "baseDir": "/tmp/test",
            "scenario": {"apps": [{"name": "example", "hosts": []}]},
        },
        "metadata": {"existing": "value"},
    }
    input_json = json.dumps(input_data)

    # Mock sys.stdin to provide the input JSON
    with patch("sys.stdin", StringIO(input_json)):
        # Initialize app (which reads stdin)
        app = Example("example", "configure")

        # Run the configure stage
        app.execute_stage()

        # Verify the modification happened
        assert app.experiment.metadata.annotations["example-python-processed"] == "true"
        # Verify existing data preserved
        assert app.experiment.metadata.existing == "value"


def test_panic_recovery(capsys):
    # Minimal valid experiment JSON
    input_data = {
        "spec": {
            "experimentName": "test-exp",
            "baseDir": "/tmp/test",
            "scenario": {"apps": [{"name": "example", "hosts": []}]},
        },
        "metadata": {},
    }
    input_json = json.dumps(input_data)

    with patch("sys.stdin", StringIO(input_json)):

        app = Example("example", "running")

        # Reconfigure logger to write JSON to stderr
        phenix_logger.remove()
        phenix_logger.add(sys.stderr, serialize=True)

        with patch.dict(os.environ, {"SIMULATE_PANIC": "1"}):
            with pytest.raises(SystemExit) as excinfo:
                app.execute_stage()

            assert excinfo.value.code == 1

    captured = capsys.readouterr()
    assert '"name": "ERROR"' in captured.err
    assert "simulated panic" in captured.err
    assert '"exception":' in captured.err


def test_log_level_debug(capsys):
    # Minimal valid experiment JSON
    input_data = {
        "spec": {
            "experimentName": "test-exp",
            "baseDir": "/tmp/test",
            "scenario": {"apps": [{"name": "example", "hosts": []}]},
        },
        "metadata": {},
    }
    input_json = json.dumps(input_data)

    with patch("sys.stdin", StringIO(input_json)), patch(
        "phenix_apps.common.settings.PHENIX_LOG_LEVEL", "DEBUG"
    ):

        app = Example("example", "running")

        # Reconfigure logger to write JSON to stderr
        phenix_logger.remove()
        phenix_logger.add(sys.stderr, serialize=True)
        app.execute_stage()

    captured = capsys.readouterr()
    assert '"name": "DEBUG"' in captured.err
    assert "Performing calculation..." in captured.err


def test_log_json_format(capsys):
    # Minimal valid experiment JSON
    input_data = {
        "spec": {
            "experimentName": "test-exp",
            "baseDir": "/tmp/test",
            "scenario": {"apps": [{"name": "example", "hosts": []}]},
        },
        "metadata": {},
    }
    input_json = json.dumps(input_data)

    with patch("sys.stdin", StringIO(input_json)):

        app = Example("example", "running")

        # Reconfigure logger to write JSON to stderr
        phenix_logger.remove()
        phenix_logger.add(sys.stderr, serialize=True)
        app.execute_stage()

    captured = capsys.readouterr()
    assert captured.err.strip(), "Expected stderr output"

    for line in captured.err.strip().split("\n"):
        try:
            entry = json.loads(line)
        except json.JSONDecodeError:
            pytest.fail(f"Log line is not valid JSON: {line}")

        assert "record" in entry
        record = entry["record"]
        assert "level" in record
        assert "message" in record
        assert "time" in record


def test_iteration_field(capsys):
    # Minimal valid experiment JSON
    input_data = {
        "spec": {
            "experimentName": "test-exp",
            "baseDir": "/tmp/test",
            "scenario": {"apps": [{"name": "example", "hosts": []}]},
        },
        "metadata": {},
    }
    input_json = json.dumps(input_data)

    with patch("sys.stdin", StringIO(input_json)), patch(
        "phenix_apps.common.settings.PHENIX_LOG_FILE", "stderr"
    ), patch("phenix_apps.common.settings.PHENIX_LOG_LEVEL", "DEBUG"):

        app = Example("example", "running")

        # Reconfigure logger to write JSON to stderr
        phenix_logger.remove()
        phenix_logger.add(sys.stderr, serialize=True)
        app.execute_stage()

    captured = capsys.readouterr()
    assert '"iteration": 1' in captured.err
