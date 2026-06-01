#!/usr/bin/env python3
import json
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from urllib.parse import parse_qs, urlparse
import time

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
                                        "matchType": "BO1",
                                        "slug": "e2e-match-1",
                                        "planGameCount": 1,
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
                "fpvData": [],
            }
        ]
    }


class Handler(BaseHTTPRequestHandler):
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

    def do_POST(self):
        parsed = urlparse(self.path)
        if parsed.path == "/inference":
            # whisper.cpp compatible enough for stt-job e2e. We intentionally
            # do not parse multipart content here; this endpoint verifies that
            # the job can POST media and consume verbose_json.
            self.send(200, {
                "text": "红方开局推进。蓝方完成防守反击。",
                "duration": 8.0,
                "segments": [
                    {"id": 0, "start": 0.0, "end": 3.5, "text": "红方开局推进。"},
                    {"id": 1, "start": 3.5, "end": 8.0, "text": "蓝方完成防守反击。"},
                ],
            })
            return
        if parsed.path == "/v1/workflows/run":
            length = int(self.headers.get("content-length") or 0)
            if length:
                self.rfile.read(length)
            now = int(time.time())
            self.send(200, {
                "workflow_run_id": "e2e-workflow-run",
                "task_id": "e2e-task",
                "data": {
                    "id": "e2e-run",
                    "status": "succeeded",
                    "outputs": {
                        "report_markdown": "## E2E 战报\n\n红方 1:0 蓝方，本地闭环测试完成。",
                        "report_json": {
                            "summary": "local e2e report",
                            "winner": "red",
                            "rounds": 1,
                        },
                    },
                    "total_tokens": 16,
                    "total_steps": 1,
                    "created_at": now,
                    "finished_at": now,
                },
            })
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
