"""Shared service graph and transport port assertions for installer journeys."""
from __future__ import annotations

import json
from urllib.parse import urlsplit


def service_ports(plan: dict) -> dict[str, int]:
    services = plan.get("serviceGraph", {}).get("services", [])
    return {item["id"]: item["port"] for item in services
            if isinstance(item, dict) and item.get("id") and isinstance(item.get("port"), int)
            and item["port"] > 0}


def service_transport_ports(plan: dict) -> dict[str, int]:
    for patch in plan.get("configPatches", []):
        if not isinstance(patch, dict) or patch.get("path") != "/platform/adapterOptions":
            continue
        config = json.loads(patch.get("json", "{}"))
        services = config.get("serviceTransport", {}).get("services", {})
        return {service_id: urlsplit(settings["url"]).port for service_id, settings in services.items()
                if isinstance(settings, dict) and isinstance(settings.get("url"), str)
                and urlsplit(settings["url"]).port is not None}
    raise RuntimeError("resolved plan omitted service transport URLs")


def assert_root_offset(plan_or_receipt: dict, base_ports: dict[str, int],
                       base_transport: dict[str, int], offset: int, label: str) -> None:
    """Assert the complete service graph and its transport URLs share one root offset."""
    ports = service_ports(plan_or_receipt)
    transport_ports = service_transport_ports(plan_or_receipt)
    if not ports or any(port != base_ports.get(service_id, 0) + offset
                        for service_id, port in ports.items()):
        raise RuntimeError(f"{label} service ports do not share the {offset}-port root offset")
    if set(ports) != set(transport_ports) or any(
            port != base_transport.get(service_id, 0) + offset
            for service_id, port in transport_ports.items()):
        raise RuntimeError(f"{label} service transport URLs do not share the {offset}-port root offset")


def assert_service_ports_preserved(before_plan: dict, after_plan: dict, label: str) -> None:
    """Assert lifecycle operations preserve a root's service and transport ports."""
    before_services, after_services = service_ports(before_plan), service_ports(after_plan)
    if before_services != after_services:
        raise RuntimeError(f"{label} changed service graph ports: before={before_services}, after={after_services}")
    before_transport = service_transport_ports(before_plan)
    after_transport = service_transport_ports(after_plan)
    if before_transport != after_transport:
        raise RuntimeError(f"{label} changed service transport ports: before={before_transport}, after={after_transport}")
