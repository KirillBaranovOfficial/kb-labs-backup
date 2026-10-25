#!/usr/bin/env python3
import json
import argparse
import contextlib
import io
import sys
import unittest


class CollectingResult(unittest.TextTestResult):
    def __init__(self, *args, **kwargs):
        super().__init__(*args, **kwargs)
        self.failures_json = []
        self.errors_json = []

    def addFailure(self, test, err):
        super().addFailure(test, err)
        self.failures_json.append({"testId": test.id(), "traceback": self._exc_info_to_string(err, test)})

    def addError(self, test, err):
        super().addError(test, err)
        self.errors_json.append({"testId": test.id(), "traceback": self._exc_info_to_string(err, test)})


class CollectingRunner(unittest.TextTestRunner):
    resultclass = CollectingResult


if __name__ == "__main__":
    parser = argparse.ArgumentParser()
    parser.add_argument("--start-directory", default="e2e/journey")
    parser.add_argument("--pattern", default="test_*.py")
    args = parser.parse_args()
    suite = unittest.defaultTestLoader.discover(args.start_directory, pattern=args.pattern)
    with contextlib.redirect_stdout(io.StringIO()):
        result = CollectingRunner(stream=sys.stderr, verbosity=1).run(suite)
    print(json.dumps({
        "testsRun": result.testsRun,
        "failures": result.failures_json,
        "errors": result.errors_json,
        "skipped": len(result.skipped),
    }))
    sys.exit(not result.wasSuccessful())
