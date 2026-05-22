-- KIFF Postgres schema.
--
-- One table per store. Append-only for events, decisions, and audit.
-- Approvals are upserted by id so review state can transition pending -> granted/denied.
--
-- The schema is intentionally minimal: just enough columns and indexes to
-- back the storetest conformance suite. Production deployments may add
-- partitioning, retention, or replicas later — those choices are operational
-- and live outside the framework.

CREATE TABLE IF NOT EXISTS kiff_events (
    id            TEXT        PRIMARY KEY,
    type          TEXT        NOT NULL,
    entity_id     TEXT        NOT NULL,
    entity_type   TEXT        NOT NULL,
    source        TEXT        NOT NULL,
    actor_id      TEXT        NOT NULL,
    occurred_at   TIMESTAMPTZ NOT NULL,
    metadata      JSONB       NOT NULL DEFAULT '{}'::JSONB,
    payload       JSONB       NOT NULL DEFAULT '{}'::JSONB,
    inserted_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS kiff_events_entity_idx
    ON kiff_events (entity_id, inserted_at);

CREATE TABLE IF NOT EXISTS kiff_decisions (
    id                 TEXT        PRIMARY KEY,
    entity_id          TEXT        NOT NULL,
    entity_type        TEXT        NOT NULL,
    kind               TEXT        NOT NULL,
    proposed_action    TEXT,
    evidence           JSONB       NOT NULL DEFAULT '[]'::JSONB,
    reasoning_summary  TEXT,
    confidence         DOUBLE PRECISION,
    actor_id           TEXT        NOT NULL,
    created_at         TIMESTAMPTZ NOT NULL,
    inserted_at        TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS kiff_decisions_entity_idx
    ON kiff_decisions (entity_id, inserted_at);

CREATE TABLE IF NOT EXISTS kiff_approvals (
    id            TEXT        PRIMARY KEY,
    entity_id     TEXT        NOT NULL,
    entity_type   TEXT        NOT NULL,
    action_name   TEXT        NOT NULL,
    requested_by  TEXT        NOT NULL,
    reviewed_by   TEXT,
    status        TEXT        NOT NULL,
    reason        TEXT,
    created_at    TIMESTAMPTZ NOT NULL,
    reviewed_at   TIMESTAMPTZ,
    inserted_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS kiff_approvals_entity_idx
    ON kiff_approvals (entity_id, inserted_at);
CREATE INDEX IF NOT EXISTS kiff_approvals_status_idx
    ON kiff_approvals (status);

CREATE TABLE IF NOT EXISTS kiff_audit (
    id              TEXT        PRIMARY KEY,
    kind            TEXT        NOT NULL,
    entity_id       TEXT        NOT NULL,
    entity_type     TEXT        NOT NULL,
    actor_id        TEXT,
    message         TEXT,
    data            JSONB       NOT NULL DEFAULT '{}'::JSONB,
    trace_id        TEXT,
    correlation_id  TEXT,
    causation_id    TEXT,
    created_at      TIMESTAMPTZ NOT NULL,
    inserted_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS kiff_audit_entity_idx
    ON kiff_audit (entity_id, created_at);
CREATE INDEX IF NOT EXISTS kiff_audit_kind_idx
    ON kiff_audit (kind);
CREATE INDEX IF NOT EXISTS kiff_audit_actor_idx
    ON kiff_audit (actor_id);
CREATE INDEX IF NOT EXISTS kiff_audit_trace_idx
    ON kiff_audit (trace_id);
CREATE INDEX IF NOT EXISTS kiff_audit_correlation_idx
    ON kiff_audit (correlation_id);
