from __future__ import annotations

import unittest

from runner import project_b_fixture_from_state
from service_steps import FlowGraph, Journey


class ProjectFixtureStateTests(unittest.TestCase):
    def test_missing_project_b_fixture_state_is_optional_at_runner_boundary(self):
        graph = FlowGraph(Journey("local", "missing-project-b-fixture"), {})
        self.assertIsNone(project_b_fixture_from_state(graph))

    def test_project_b_fixture_values_are_read_from_established_state(self):
        value = {"projectBId": "prj_project_b", "projectBRoot": "/tmp/project-b"}
        graph = FlowGraph(Journey("local", "registered-project-b-fixture"), {}, states={
            "fixtures-installed": {"passed": True, "evidence": "registered fixture project", "value": value},
        })
        self.assertEqual(project_b_fixture_from_state(graph), value)


if __name__ == "__main__":
    unittest.main()
