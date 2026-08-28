#!/usr/bin/env python3
"""Tiny mock of a UniFi OS gateway's Network API for smoke-testing rampart."""
import json
from http.server import BaseHTTPRequestHandler, HTTPServer

RULES = {
    "aaa111": {"_id": "aaa111", "name": "Allow SSH from mgmt", "ruleset": "LAN_IN",
               "rule_index": 2000, "action": "accept", "enabled": True, "protocol": "tcp",
               "src_address": "10.0.99.0/24", "src_port": "", "dst_address": "", "dst_port": "22",
               "logging": False, "extra_field": "must-survive-roundtrip"},
    "bbb222": {"_id": "bbb222", "name": "Block IoT to LAN", "ruleset": "LAN_IN",
               "rule_index": 2001, "action": "drop", "enabled": True, "protocol": "all",
               "src_address": "10.0.30.0/24", "src_port": "", "dst_address": "10.0.10.0/24",
               "dst_port": "", "logging": True},
}
DEVICES = [{"_id": "d1", "name": "Gateway", "model": "UDM-Pro", "type": "udm", "mac": "aa:bb:cc:dd:ee:01",
            "ip": "192.168.1.1", "version": "4.1.13", "state": 1, "uptime": 273600}]
STAS = [{"_id": "s1", "name": "", "hostname": "laptop", "oui": "Apple", "mac": "11:22:33:44:55:66",
         "ip": "10.0.10.50", "network": "LAN", "is_wired": False, "uptime": 3700}]
GROUPS = [{"_id": "g1", "name": "Admin IPs", "group_type": "address-group", "group_members": ["10.0.99.5", "10.0.99.6"]}]
PORTFWDS = {
    "pf1": {"_id": "pf1", "name": "Web server", "enabled": True, "pfwd_interface": "wan",
            "proto": "tcp", "src": "any", "dst_port": "443", "fwd": "10.0.10.20", "fwd_port": "8443",
            "log": False, "extra_field": "must-survive-roundtrip"},
}
POLICIES = [{"_id": "p1", "name": "Block IoT to Internal", "action": "BLOCK", "enabled": True,
             "index": 10000, "predefined": False, "protocol": "all",
             "source": {"zone_id": "z-iot"}, "destination": {"zone_id": "z-internal"}}]
ZONES = [{"_id": "z-iot", "name": "IoT"}, {"_id": "z-internal", "name": "Internal"}]
NEXT_ID = [1]

def env(data):
    return {"meta": {"rc": "ok"}, "data": data}

class H(BaseHTTPRequestHandler):
    def _send(self, obj, status=200):
        body = json.dumps(obj).encode()
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("X-Csrf-Token", "csrf-tok-123")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def _body(self):
        n = int(self.headers.get("Content-Length", 0))
        return json.loads(self.rfile.read(n)) if n else None

    def log_message(self, *a):
        pass

    def do_GET(self):
        p = self.path
        if p == "/":
            return self._send({"up": True})
        if p == "/proxy/network/api/s/default/rest/firewallrule":
            return self._send(env(list(RULES.values())))
        if p.startswith("/proxy/network/api/s/default/rest/firewallrule/"):
            rid = p.rsplit("/", 1)[1]
            return self._send(env([RULES[rid]] if rid in RULES else []))
        if p == "/proxy/network/api/s/default/stat/device":
            return self._send(env(DEVICES))
        if p == "/proxy/network/api/s/default/stat/sta":
            return self._send(env(STAS))
        if p == "/proxy/network/api/s/default/rest/firewallgroup":
            return self._send(env(GROUPS))
        if p == "/proxy/network/api/s/default/rest/portforward":
            return self._send(env(list(PORTFWDS.values())))
        if p.startswith("/proxy/network/api/s/default/rest/portforward/"):
            rid = p.rsplit("/", 1)[1]
            return self._send(env([PORTFWDS[rid]] if rid in PORTFWDS else []))
        if p == "/proxy/network/v2/api/site/default/firewall-policies":
            return self._send(POLICIES)
        if p == "/proxy/network/v2/api/site/default/firewall/zones":
            return self._send(ZONES)
        self._send({"message": "not found"}, 404)

    def do_POST(self):
        p = self.path
        if p == "/api/auth/login":
            b = self._body()
            if b.get("username") == "admin" and b.get("password") == "pw":
                return self._send({"unique_id": "u1"})
            return self._send({"meta": {"rc": "error", "msg": "api.err.LoginRequired"}}, 401)
        if p == "/proxy/network/api/s/default/rest/firewallrule":
            if self.headers.get("X-Csrf-Token") != "csrf-tok-123":
                return self._send({"meta": {"rc": "error", "msg": "missing csrf"}}, 401)
            rule = self._body()
            rid = "new%03d" % NEXT_ID[0]
            NEXT_ID[0] += 1
            rule["_id"] = rid
            RULES[rid] = rule
            return self._send(env([rule]))
        if p == "/proxy/network/api/s/default/rest/portforward":
            if self.headers.get("X-Csrf-Token") != "csrf-tok-123":
                return self._send({"meta": {"rc": "error", "msg": "missing csrf"}}, 401)
            rule = self._body()
            rid = "newpf%03d" % NEXT_ID[0]
            NEXT_ID[0] += 1
            rule["_id"] = rid
            PORTFWDS[rid] = rule
            return self._send(env([rule]))
        self._send({"message": "not found"}, 404)

    def do_PUT(self):
        p = self.path
        if p.startswith("/proxy/network/api/s/default/rest/firewallrule/"):
            rid = p.rsplit("/", 1)[1]
            if rid not in RULES:
                return self._send(env([]))
            rule = self._body()
            assert rule.get("extra_field", None) == RULES[rid].get("extra_field"), "unknown fields must round-trip"
            RULES[rid] = rule
            return self._send(env([rule]))
        if p.startswith("/proxy/network/api/s/default/rest/portforward/"):
            rid = p.rsplit("/", 1)[1]
            if rid not in PORTFWDS:
                return self._send(env([]))
            rule = self._body()
            assert rule.get("extra_field", None) == PORTFWDS[rid].get("extra_field"), "unknown fields must round-trip"
            PORTFWDS[rid] = rule
            return self._send(env([rule]))
        if p.startswith("/proxy/network/v2/api/site/default/firewall-policies/"):
            pid = p.rsplit("/", 1)[1]
            body = self._body()
            for i, pol in enumerate(POLICIES):
                if pol["_id"] == pid:
                    POLICIES[i] = body
                    return self._send(body)
        self._send({"message": "not found"}, 404)

    def do_DELETE(self):
        p = self.path
        if p.startswith("/proxy/network/api/s/default/rest/firewallrule/"):
            rid = p.rsplit("/", 1)[1]
            RULES.pop(rid, None)
            return self._send(env([]))
        if p.startswith("/proxy/network/api/s/default/rest/portforward/"):
            rid = p.rsplit("/", 1)[1]
            PORTFWDS.pop(rid, None)
            return self._send(env([]))
        self._send({"message": "not found"}, 404)

HTTPServer(("127.0.0.1", 8765), H).serve_forever()
