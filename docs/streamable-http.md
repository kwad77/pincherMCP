# Streamable-HTTP MCP transport

> Operational reference for the streamable-HTTP MCP transport (#651). Pincher serves MCP over stdio by default; this transport is the alternative for routing-shaped consumers (zelos, bifrost) deployed in k8s where per-backend stdio sub-process spawning is undesirable.

## Why

Stdio is fine when one MCP client launches one pincher binary per workspace. It stops being fine when a router (zelos, bifrost) wants to multiplex traffic across many backends:

- Per-backend stdio means per-backend sub-process supervision
- Standard HTTP load-balancing doesn't apply to stdio
- HTTP observability stacks (request logs, traces, metrics) don't see stdio JSON-RPC

Streamable-HTTP closes that gap. The router speaks HTTP to pincher; pincher serves the same MCP tools through the same in-process server it serves over stdio.

## Enabling

```bash
# Stdio + streamable-HTTP at /mcp on the existing HTTP server
pincher --http :8081 --mcp-http-path /mcp

# HTTP-only (no stdio): streamable-HTTP and REST gateway co-served
pincher --no-stdio --http :8081 --mcp-http-path /mcp

# Behind a reverse proxy stripping /pincher
pincher --http :8081 --mcp-http-path /mcp --basepath /pincher
# → router POSTs to https://example.com/pincher/mcp
```

Environment fallback: `PINCHER_MCP_HTTP_PATH=/mcp`.

`--mcp-http-path` requires `--http` — the streamable transport mounts on the existing HTTP server, sharing its bind address, auth (`--http-key`), rate limiting (`--http-rate`), and basepath stripping.

## Capability advertisement

When mounted, every tool response includes `streamable_http` in `_meta.capabilities` (#649). Routers query the capability before deciding to switch from stdio to HTTP — so a misconfigured deployment is detectable without trial-and-error.

```json
{
  "_meta": {
    "capabilities": ["schema_v25", "operator_tools_on_mcp", "streamable_http", ...]
  }
}
```

## Auth

When `--http-key <token>` is set, the streamable-HTTP transport requires `Authorization: Bearer <token>` exactly like the REST gateway. The bearer check runs *before* the MCP handler — an unauthenticated request never reaches the SDK.

`/v1/health` and `/v1/openapi.json` remain public probes for liveness checks (#588). The streamable-HTTP path does not have a public-probe carve-out — every request must authenticate.

## Both transports simultaneously

Stdio and streamable-HTTP share the same in-process `*mcp.Server` with the same registered tool set. Tool registration, capability advertisement, and per-tool complexity tier (`#650`) apply identically across both transports. There is no router-visible difference between calling `health` over stdio vs HTTP — same input schema, same output shape, same `_meta` envelope.

## Capability composition

The streamable-HTTP transport composes with the rest of v0.53's router-integration contract:

- **`_meta.capabilities`** (#649) — declares `streamable_http` so routers can discover the transport
- **`_meta.complexity_tier`** (#650) — every response carries its tier regardless of transport
- **Release channels** (#642) — pincher binaries served on the `dev` channel can be tested alongside the stable channel by pointing routers at different `--mcp-http-path` mounts

## See also

- [#651](https://github.com/kwad77/pincher/issues/651) — streamable-HTTP MCP transport
- [#649](https://github.com/kwad77/pincher/issues/649) — `_meta.capabilities` advertisement
- [`docs/release-channels.md`](release-channels.md) — channel discipline for transport rollout
