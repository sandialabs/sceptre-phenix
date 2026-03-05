import os
import sys
from phenix_apps.apps import AppBase
from phenix_apps.common.logger import logger


class Example(AppBase):
    """
    Example application demonstrating Phenix logging capabilities.
    """

    def configure(self) -> None:
        logger.info("Configuring example app")

        # Demonstrate modifying the experiment configuration
        if "metadata" not in self.experiment:
            self.experiment.metadata = {}

        if "annotations" not in self.experiment.metadata:
            self.experiment.metadata.annotations = {}

        self.experiment.metadata.annotations["example-python-processed"] = "true"

        # Demonstrate structured logging
        # Context fields bound via .bind() are merged into the JSON log output
        logger.bind(
            config_id=12345,
            mode="demo",
            features=["logging", "json"],
        ).info("Loaded configuration")

    def pre_start(self) -> None:
        logger.info("Executing pre-start checks")

    def post_start(self) -> None:
        logger.info("Executing post-start tasks")

    def running(self) -> None:
        if os.getenv("SIMULATE_PANIC") == "1":
            try:
                raise RuntimeError("simulated panic")
            except Exception:
                logger.exception("Simulated panic occurred")
                sys.exit(1)

        logger.info("Starting main execution loop")

        # Demonstrate exception handling
        # Always use logger.exception() inside except blocks to ensure
        # the traceback is captured as a structured field ('traceback')
        # rather than raw text on stderr.
        try:
            self._simulate_work()
        except Exception:
            logger.exception("An error occurred during simulated work")

    def cleanup(self) -> None:
        logger.info("Cleaning up resources")

    def _simulate_work(self):
        logger.bind(iteration=1).debug("Performing calculation...")
        # Simulate a failure to demonstrate traceback logging
        raise RuntimeError("Something went wrong!")


def main():
    # Entry point that leverages AppBase.main logic
    Example.main("example")


if __name__ == "__main__":
    main()
