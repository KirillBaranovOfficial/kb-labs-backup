import unittest

from project_service_path import project_service_path, require_project_service_path_builder


class ProjectServicePathTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        require_project_service_path_builder()

    def test_builds_service_namespace_from_shared_contract(self):
        self.assertEqual(
            project_service_path("p1", "rest", "analytics/events"),
            "/api/v1/projects/p1/rest/analytics/events",
        )

    def test_encodes_project_identity_as_one_path_segment(self):
        self.assertEqual(
            project_service_path("project/one", "mcp"),
            "/api/v1/projects/project%2Fone/mcp",
        )


if __name__ == "__main__":
    unittest.main()
