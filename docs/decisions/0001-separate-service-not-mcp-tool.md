# 1. mavlink-bridge is a standalone service, not an MCP tool

Date: 2026-09-13

## Status

Accepted (this restates and formalizes a decision already made in
`DRONEFLEET_NEXT_PHASE_PLAN.md` §5 — recorded here per that plan's own §12
convention that every architectural choice gets an ADR).

## Context

MAVLink is a continuous UDP stream from PX4 SITL. Flight state (armed,
landed, battery, faults) needs to be known and reported to dronefleet
whether or not an agent happens to be asking a question at that moment —
in particular, the fail-closed heartbeat watchdog (§4/§11a) must keep
running and can transition state to `UNKNOWN` with nobody calling any tool
at all.

`dronefleet-mcp`'s six tools are stateless request/response wrappers around
dronefleet's REST API (verified: every tool handler is a thin JSON
passthrough — see `dronefleet-mcp/cmd/server/main.go`). There's no place in
that model for a persistent listener or a background watchdog.

## Decision

`mavlink-bridge` is its own long-running Go service:
`px4-sitl-gazebo-svc:14550 → mavlink-bridge → dronefleet API`. It writes to
dronefleet over the same HTTP API any other client uses. No changes to
`dronefleet-mcp` are required, and none were made — its tools return
whatever JSON the dronefleet API gives them, so the richer `Drone` response
this repo's telemetry/fault writes produce reaches Agent 1 automatically
through the `get_drone`/`get_fleet_status` tool calls it already makes.

## Consequences

- One more deployable service, Helm chart, and CI pipeline (matching the
  existing per-repo pattern: `dronefleet`, `dronefleet-agent`, now
  `mavlink-bridge`).
- Agent 1's diagnostic loop needed zero code changes to benefit from this —
  confirmed by reading `dronefleet-agent/internal/agent/agent.go`: it calls
  `get_drone`/`get_fleet_status` already, and those tools just relay
  dronefleet's JSON.
