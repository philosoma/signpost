"""Asserts what the detection test rendered."""
import json
import sys

import yaml

out, fixture = sys.argv[1], sys.argv[2]
config = json.load(open(f"{out}/config.json"))
compose = yaml.safe_load(open(f"{out}/docker-compose.yml"))
route = yaml.safe_load(open(f"{out}/traefik-signpost.yml"))
# What the running app shows, after reading the fixture's route files.
status = json.load(open(f"{out}/status.json"))

links = {l["name"]: l for s in status["sections"] for l in s["links"]}
titles = [s["title"] for s in status["sections"]]
discover = config["discover"]
volumes = compose["services"]["signpost"]["volumes"]
pins = compose["services"]["signpost"]["extra_hosts"]
router = route["http"]["routers"]["signpost"]

assert route["http"]["services"]["signpost"]["loadBalancer"]["servers"][0]["url"].startswith("http://oauth2-proxy-signpost")
assert config["auth"]["required_groups"] == ["PLATFORM-ADMIN", "IAM-ADMIN"]
assert "netbird" not in config["sources"], "no token, so no NetBird source"
assert not any(p.startswith("signpost.") for p in pins), "must not pin itself"
assert not any("<" in p for p in pins), "text in comments must be ignored"
assert discover["domain"] == "demo.example.net"
assert discover["core_address"] == "192.0.2.1"
assert {"mgmt", "oauth2", "signpost"} <= set(discover["hide"]), discover["hide"]
assert any(v.endswith(":/etc/signpost/routes:ro") or "/fixtures/" in v for v in volumes), volumes
assert "error" not in status["discovery"], status["discovery"]
assert not any(l["name"] in {"Mgmt", "Oauth2", "Signpost"} for l in links.values())
# Links the app found are not baked into the config, so a new route needs no redeploy.
assert all(l["url"].startswith("http://") for s in config["sections"] for l in s["links"]), config["sections"]

if fixture == "full":
    assert titles == ["Administer", "Observe", "Help", "Play", "Other"], titles
    assert set(links) == {
        "NetBird dashboard", "Portal", "Account", "Panoscope", "Guide", "Air Defenders", "Sentry",
        "Grafana (obs-one)", "Grafana (obs-two)", "New App", "Federation",
    }, sorted(links)
    admin = [l["name"] for s in status["sections"] if s["title"] == "Administer" for l in s["links"]]
    assert "Federation" in admin, admin
    assert config["sources"]["federation"]["url"] == "http://federation:8090", config["sources"]
    assert links["New App"]["url"] == "https://new-app.demo.example.net/"
    assert links["Account"]["url"] == "https://auth.demo.example.net/settings"
    assert links["Account"]["probe_url"] == "https://auth.demo.example.net/"
    assert links["Grafana (obs-one)"]["probe"] is False
    assert links["Air Defenders"]["url"] == "https://airdefenders.demo.example.net/"
    play = next(s["links"] for s in status["sections"] if s["title"] == "Play")
    assert [l["name"] for l in play] == ["Air Defenders", "Sentry"], play
    assert links["Grafana (obs-two)"]["url"] == "http://198.51.100.22:3000/"
    assert config["sources"]["portal"]["url"] == "https://portal.demo.example.net"
    assert [p["url"] for p in config["sources"]["prometheus"]] == [
        "http://192.0.2.21:9090", "http://198.51.100.22:9090",
    ]
    assert "example.net:192.0.2.1" in pins
    assert "goat-ratelimit@file" in router["middlewares"]
else:
    assert titles == ["Administer"], titles
    assert set(links) == {"NetBird dashboard"}, sorted(links)
    assert "portal" not in config["sources"], "no route, so no portal source"
    assert "federation" not in config["sources"], "no route or key store, so no federation source"
    assert "prometheus" not in config["sources"], "no obs hosts"
    assert router["middlewares"] == ["mesh-only-allowlist@file"], "no rate limit defined"

print(f"{fixture}: ok")
