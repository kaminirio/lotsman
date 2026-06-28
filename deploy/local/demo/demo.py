#!/usr/bin/env python3
"""Local-demo data plane for Lotsman (stdlib only).

This one process backs the three telemetry adapters with controllable, always-
fresh synthetic data so a live `POST /api/v1/investigate` over the demo resource
returns a real, correlated, ranked incident — without needing a Kubernetes
cluster:

  1. Mock ArgoCD REST API on :8081 — serves one Application ("checkout" in
     namespace "demo") whose sync history is timestamped a few minutes before
     *now* on every request. This drives the change-first ranker: the deploy
     becomes the top probable-cause hypothesis.
  2. Loki log push loop — streams ERROR-level logs for {namespace="demo",
     app="checkout"} into Loki so they land in the incident timeline.
  3. VictoriaMetrics import loop — pushes rising memory/error-rate samples for
     the same resource (queryable directly; the incident timeline doesn't
     surface metrics yet — that's the pending detector-scheduler work).

Configuration via env:
  LOKI_URL    (default http://loki:3100)
  VICTORIA_URL(default http://victoriametrics:8428)
  ARGOCD_PORT (default 8081)
  DEMO_NS / DEMO_APP / DEMO_POD
"""
import json
import os
import random
import threading
import time
import urllib.error
import urllib.request
from datetime import datetime, timedelta, timezone
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

LOKI_URL = os.environ.get("LOKI_URL", "http://loki:3100").rstrip("/")
VICTORIA_URL = os.environ.get("VICTORIA_URL", "http://victoriametrics:8428").rstrip("/")
ARGOCD_PORT = int(os.environ.get("ARGOCD_PORT", "8081"))

NS = os.environ.get("DEMO_NS", "demo")
APP = os.environ.get("DEMO_APP", "checkout")
POD = os.environ.get("DEMO_POD", "checkout-7d9f5b8c6-abcde")

# A stable-ish revision for the "bad" deploy so the narrative reads coherently.
BAD_REVISION = "9f3c1ae8b27d04e15c8a6f2b0d4e7a1c3b9f2e6d"
REPO_URL = "https://github.com/securero/checkout"

# How long before "now" the implicated deploy synced. Kept inside the ranker's
# 15m ChangeWindow so it always produces a high-confidence hypothesis.
DEPLOY_AGE = timedelta(minutes=4)


def _now():
    return datetime.now(timezone.utc)


def _rfc3339(dt):
    # ArgoCD emits RFC3339 with a trailing Z; Go's time.Time parses it.
    return dt.replace(microsecond=0).isoformat().replace("+00:00", "Z")


# --------------------------------------------------------------------------- #
# 1. Mock ArgoCD REST API
# --------------------------------------------------------------------------- #
def argo_app_payload():
    """Build the /api/v1/applications envelope with a freshly-timestamped sync."""
    synced = _now() - DEPLOY_AGE
    older = synced - timedelta(hours=6)  # an out-of-window prior sync
    return {
        "items": [
            {
                "metadata": {"name": APP},
                "spec": {
                    "destination": {
                        "namespace": NS,
                        "server": "https://kubernetes.default.svc",
                        "name": "local",
                    }
                },
                "status": {
                    "history": [
                        {
                            "id": 41,
                            "revision": "1c0ffee0baddecaf1c0ffee0baddecaf1c0ffee0",
                            "deployedAt": _rfc3339(older),
                            "source": {"repoURL": REPO_URL},
                        },
                        {
                            "id": 42,
                            "revision": BAD_REVISION,
                            "deployedAt": _rfc3339(synced),
                            "source": {"repoURL": REPO_URL},
                        },
                    ]
                },
            }
        ]
    }


class ArgoHandler(BaseHTTPRequestHandler):
    def do_GET(self):  # noqa: N802 (stdlib API name)
        if self.path.startswith("/api/v1/applications"):
            body = json.dumps(argo_app_payload()).encode()
            self.send_response(200)
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(body)))
            self.end_headers()
            self.wfile.write(body)
        elif self.path in ("/", "/healthz"):
            self.send_response(200)
            self.end_headers()
            self.wfile.write(b"ok")
        else:
            self.send_response(404)
            self.end_headers()

    def log_message(self, *_args):  # silence per-request logging
        pass


def serve_argocd():
    srv = ThreadingHTTPServer(("0.0.0.0", ARGOCD_PORT), ArgoHandler)
    print(f"[demo] mock ArgoCD API listening on :{ARGOCD_PORT}", flush=True)
    srv.serve_forever()


# --------------------------------------------------------------------------- #
# 2. Loki log push loop
# --------------------------------------------------------------------------- #
LOG_LINES = [
    ('error', 'panic: payment gateway timeout after 30s rev=%s' % BAD_REVISION[:8]),
    ('error', 'failed to charge order order_id=8831 status=502'),
    ('error', 'context deadline exceeded calling upstream=payments'),
    ('warn', 'retry 3/3 exhausted for charge order_id=8831'),
    ('error', 'nil pointer dereference in checkout.Finalize (new in %s)' % BAD_REVISION[:8]),
    ('info', 'request completed path=/healthz status=200'),
]


def _post(url, data, headers, timeout=5):
    req = urllib.request.Request(url, data=data, headers=headers, method="POST")
    with urllib.request.urlopen(req, timeout=timeout) as resp:
        return resp.status


def push_logs():
    while True:
        ts = _now()
        # Weight toward errors so the timeline reads as an incident.
        level, msg = random.choices(LOG_LINES, weights=[5, 5, 4, 2, 4, 1])[0]
        stream = {
            "namespace": NS,
            "app": APP,
            "pod": POD,
            "container": APP,
            "level": level,
        }
        payload = {
            "streams": [
                {
                    "stream": stream,
                    "values": [[str(int(ts.timestamp() * 1e9)), msg]],
                }
            ]
        }
        try:
            _post(
                f"{LOKI_URL}/loki/api/v1/push",
                json.dumps(payload).encode(),
                {"Content-Type": "application/json"},
            )
        except (urllib.error.URLError, OSError) as e:
            print(f"[demo] loki push failed (will retry): {e}", flush=True)
        time.sleep(8)


# --------------------------------------------------------------------------- #
# 3. VictoriaMetrics import loop
# --------------------------------------------------------------------------- #
def push_metrics():
    base_mem = 512 * 1024 * 1024  # 512 MiB baseline
    errors = 0
    while True:
        now_ms = int(_now().timestamp() * 1000)
        # Memory climbs after the bad deploy (a leak narrative); error rate up.
        base_mem = min(base_mem + random.randint(2, 10) * 1024 * 1024, 1536 * 1024 * 1024)
        errors += random.randint(0, 4)
        labels = f'namespace="{NS}",app="{APP}",pod="{POD}",container="{APP}"'
        lines = [
            f"container_memory_usage_bytes{{{labels}}} {base_mem} {now_ms}",
            f"app_http_requests_errors_total{{{labels}}} {errors} {now_ms}",
            f"app_http_request_latency_seconds{{{labels},quantile=\"0.99\"}} "
            f"{round(random.uniform(1.5, 8.0), 3)} {now_ms}",
        ]
        body = ("\n".join(lines) + "\n").encode()
        try:
            _post(
                f"{VICTORIA_URL}/api/v1/import/prometheus",
                body,
                {"Content-Type": "text/plain"},
            )
        except (urllib.error.URLError, OSError) as e:
            print(f"[demo] victoriametrics import failed (will retry): {e}", flush=True)
        time.sleep(12)


def main():
    print(
        f"[demo] generating synthetic telemetry for {NS}/{APP} "
        f"(loki={LOKI_URL}, vm={VICTORIA_URL})",
        flush=True,
    )
    threading.Thread(target=push_logs, daemon=True).start()
    threading.Thread(target=push_metrics, daemon=True).start()
    serve_argocd()  # blocks


if __name__ == "__main__":
    main()
