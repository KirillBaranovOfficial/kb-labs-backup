"""Deterministic OpenAI/VibeProxy-compatible responses for installed journeys."""
from __future__ import annotations

import json
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer


class Handler(BaseHTTPRequestHandler):
    def do_GET(self) -> None:
        if self.path == "/healthz":
            self.send_response(200)
            self.end_headers()
            return
        self.send_error(404)

    def do_POST(self) -> None:
        length = int(self.headers.get("Content-Length", "0"))
        request = json.loads(self.rfile.read(length) or b"{}")
        messages = request.get("messages")
        valid_messages = (
            isinstance(messages, list)
            and bool(messages)
            and all(
                isinstance(message, dict)
                and message.get("role") in {"user", "assistant"}
                and isinstance(message.get("content"), (str, list))
                for message in messages
            )
        )
        if not valid_messages or not isinstance(request.get("model"), str) or not request["model"]:
            self.send_error(400, "expected an OpenAI-compatible messages request")
            return
        is_vibeproxy = self.path == "/v1/messages"
        if is_vibeproxy and (not isinstance(request.get("max_tokens"), int)
                             or self.headers.get("x-api-key") != "journey-fake-vibeproxy-key"):
            self.send_error(400, "expected a VibeProxy messages request with the fixture key")
            return
        if not is_vibeproxy and self.headers.get("Authorization") != "Bearer journey-fake-openai-key":
            self.send_error(400, "expected the OpenAI-compatible fixture key")
            return
        if not is_vibeproxy and self.path != "/v1/chat/completions":
            self.send_error(404)
            return
        content = "VIBEPROXY_MOCK_RESPONSE" if is_vibeproxy else "OPENAI_MOCK_RESPONSE"
        result = {
            "id": "journey-mock-completion",
            "object": "chat.completion",
            "created": 1,
            "model": request.get("model", "journey-mock-model"),
            "choices": [{"index": 0, "message": {"role": "assistant", "content": content},
                         "finish_reason": "stop"}],
            "usage": {"prompt_tokens": 1, "completion_tokens": 1, "total_tokens": 2},
        }
        if is_vibeproxy:
            result = {
                "id": "journey-mock-message", "type": "message", "role": "assistant",
                "model": request.get("model", "journey-mock-model"), "stop_reason": "end_turn",
                "content": [{"type": "text", "text": content}],
                "usage": {"input_tokens": 1, "output_tokens": 1},
            }
        body = json.dumps(result).encode()
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def log_message(self, _format: str, *_args: object) -> None:
        return


if __name__ == "__main__":
    ThreadingHTTPServer(("0.0.0.0", 8317), Handler).serve_forever()
