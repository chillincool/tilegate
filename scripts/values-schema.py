#!/usr/bin/env python3
"""Generates charts/tilegate/values.schema.json.

The chart's own settings are strict (unknown keys are errors, so a typo such
as `auht:` fails at install time). Kubernetes pass-through objects such as
`resources` or `affinity` are left open. `config` is the apps file, so it
reuses schema/apps.schema.json.

    scripts/values-schema.py            write the file
    scripts/values-schema.py --check    fail if the file is out of date
"""
import json
import pathlib
import sys

ROOT = pathlib.Path(__file__).resolve().parent.parent
OUT = ROOT / "charts/tilegate/values.schema.json"


def obj(props, strict=True, **extra):
    o = {"type": "object", "properties": props, **extra}
    if strict:
        o["additionalProperties"] = False
    return o


STR = {"type": "string"}
BOOL = {"type": "boolean"}
OPEN = {"type": "object"}                       # Kubernetes pass-through
LIST = {"type": "array"}
PORT = {"type": "integer", "minimum": 1, "maximum": 65535}


def s(desc, **kw):
    return {"type": "string", "description": desc, **kw}


apps = json.loads((ROOT / "schema/apps.schema.json").read_text())
apps.pop("$schema", None)
apps.pop("$id", None)
apps["description"] = "The apps file (see schema/apps.schema.json)"

schema = obj({
    "image": obj({
        "repository": STR,
        "tag": s("Defaults to the chart's appVersion"),
        "pullPolicy": {"enum": ["Always", "IfNotPresent", "Never"]},
    }),
    "replicaCount": {"type": "integer", "minimum": 0},
    "auth": obj({
        "mode": {"enum": ["oidc", "proxy-jwt", "proxy-header", "dev"]},
        "rolesClaim": s("Comma-separated claim path(s) holding roles"),
        "oidc": obj({
            "issuer": STR, "discoveryURL": STR, "clientID": STR, "redirectURL": STR,
            "postLogoutRedirectURL": STR, "scopes": STR, "sessionTTL": STR,
        }),
        "proxyJWT": obj({"header": STR, "issuer": STR, "jwksURL": STR, "audience": STR}),
        "proxyHeader": obj({"userHeader": STR, "emailHeader": STR, "groupsHeader": STR}),
        "trustedProxies": STR,
        "logoutURL": STR,
        "devGroups": STR,
        "existingSecret": STR,
        "clientSecret": STR,
        "sessionSecret": STR,
    }),
    "config": apps,
    "database": obj({
        "existingSecret": STR,
        "secretKey": STR,
        "url": STR,
        "postgres": obj({
            "host": STR, "port": PORT, "database": STR, "user": STR,
            "passwordSecret": obj({"name": STR, "key": STR}),
            "sslMode": STR,
        }),
        "cnpg": obj({
            "enabled": BOOL,
            "instances": {"type": "integer", "minimum": 1},
            "imageName": STR,
            "storage": obj({"size": STR, "storageClass": STR}),
            "database": STR,
            "owner": STR,
            "existingSecret": STR,
            "monitoring": obj({"enablePodMonitor": BOOL}),
            "resources": OPEN,
            "backup": OPEN,
            "scheduledBackup": obj({"enabled": BOOL, "schedule": STR}),
            "extraSpec": OPEN,
        }),
    }),
    "persistence": obj({
        "enabled": BOOL, "existingClaim": STR, "size": STR, "storageClass": STR,
        "accessMode": {"enum": ["ReadWriteOnce", "ReadWriteOncePod", "ReadWriteMany"]},
    }),
    "existingConfigMap": STR,
    "iconsConfigMap": STR,
    "logLevel": {"enum": ["debug", "info", "warn", "error"]},
    "logFormat": {"enum": ["text", "json"]},
    "extraEnv": LIST,
    "service": obj({
        "type": {"enum": ["ClusterIP", "NodePort", "LoadBalancer"]},
        "port": PORT,
    }),
    "route": obj({"enabled": BOOL, "annotations": OPEN, "hostnames": LIST, "parentRefs": LIST}),
    "ingress": obj({"enabled": BOOL, "className": STR, "annotations": OPEN, "hosts": LIST, "tls": LIST}),
    "resources": OPEN,
    "podAnnotations": OPEN,
    "podLabels": OPEN,
    "podSecurityContext": OPEN,
    "securityContext": OPEN,
    "nodeSelector": OPEN,
    "tolerations": LIST,
    "affinity": OPEN,
}, **{"$schema": "https://json-schema.org/draft-07/schema#", "title": "tilegate Helm chart values"})
# Helm passes global values to every chart; allow them.
schema["properties"]["global"] = OPEN

text = json.dumps(schema, indent=2) + "\n"
if "--check" in sys.argv:
    if OUT.read_text() != text:
        sys.exit(f"{OUT.relative_to(ROOT)} is out of date; run scripts/values-schema.py")
    print("values.schema.json is up to date")
else:
    OUT.write_text(text)
    print(f"wrote {OUT.relative_to(ROOT)}")
