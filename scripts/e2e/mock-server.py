#!/usr/bin/env python3
import json
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from urllib.parse import parse_qs, urlparse

STATE = {
    "status": "WAITING",
    "red": 0,
    "blue": 0,
    "result": "UNKNOWN",
}


def schedule():
    return {
        "data": {
            "event": {
                "title": "E2E Event",
                "zones": {
                    "nodes": [
                        {
                            "name": "E2E Zone",
                            "groupMatches": {
                                "nodes": [
                                    {
                                        "id": "e2e-match-1",
                                        "orderNumber": 1,
                                        "status": STATE["status"],
                                        "matchType": "BO3",
                                        "slug": "e2e-match-1",
                                        "planGameCount": 3,
                                        "result": STATE["result"],
                                        "winnerPlaceholdName": "",
                                        "loserPlaceholdName": "",
                                        "redSideWinGameCount": STATE["red"],
                                        "blueSideWinGameCount": STATE["blue"],
                                        "redSide": {
                                            "player": {
                                                "teamId": "red-team",
                                                "team": {
                                                    "id": "red-team",
                                                    "name": "Red",
                                                    "collegeName": "Red School",
                                                    "collegeLogo": "",
                                                },
                                            }
                                        },
                                        "blueSide": {
                                            "player": {
                                                "teamId": "blue-team",
                                                "team": {
                                                    "id": "blue-team",
                                                    "name": "Blue",
                                                    "collegeName": "Blue School",
                                                    "collegeLogo": "",
                                                },
                                            }
                                        },
                                    }
                                ]
                            },
                            "knockoutMatches": {"nodes": []},
                        }
                    ]
                },
            }
        }
    }


def live_info():
    return {
        "eventData": [
            {
                "zoneName": "E2E Zone",
                "chatRoomId": "e2e-chat-room",
                "zoneLiveString": [
                    {"res": "1080p", "src": "http://e2e-media:8080/main.m3u8"}
                ],
                "fpvData": [
                    {
                        "role": "\u7ea2\u65b9\u82f1\u96c4\u7b2c\u4e00\u89c6\u89d2",
                        "sources": [
                            {"res": "1080p", "src": "http://e2e-media:8080/main.m3u8"}
                        ],
                    }
                ],
            }
        ]
    }


class Handler(BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"

    def do_GET(self):
        parsed = urlparse(self.path)
        if parsed.path == "/healthz":
            self.send(200, {"ok": True, "state": STATE})
            return
        if parsed.path == "/set":
            params = parse_qs(parsed.query)
            if "status" in params:
                STATE["status"] = params["status"][0]
            if "red" in params:
                STATE["red"] = int(params["red"][0])
            if "blue" in params:
                STATE["blue"] = int(params["blue"][0])
            if "result" in params:
                STATE["result"] = params["result"][0]
            self.send(200, {"ok": True, "state": STATE})
            return
        if parsed.path == "/schedule.json":
            self.send(200, schedule())
            return
        if parsed.path == "/live_game_info.json":
            self.send(200, live_info())
            return
        self.send(404, {"error": "not found"})

    def log_message(self, fmt, *args):
        print(fmt % args, flush=True)

    def send(self, code, payload):
        body = json.dumps(payload, ensure_ascii=False).encode("utf-8")
        self.send_response(code)
        self.send_header("content-type", "application/json; charset=utf-8")
        self.send_header("content-length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)


ThreadingHTTPServer(("0.0.0.0", 8080), Handler).serve_forever()
