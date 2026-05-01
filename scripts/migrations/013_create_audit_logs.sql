CREATE TABLE IF NOT EXISTS audit_logs (
    id          BIGSERIAL PRIMARY KEY,
    user_id     TEXT        NOT NULL DEFAULT '',
    method      TEXT        NOT NULL,
    code        TEXT        NOT NULL,
    peer_addr   TEXT        NOT NULL DEFAULT '',
    duration_ms BIGINT      NOT NULL DEFAULT 0,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_audit_logs_user_id    ON audit_logs (user_id);
CREATE INDEX idx_audit_logs_method     ON audit_logs (method);
CREATE INDEX idx_audit_logs_created_at ON audit_logs (created_at);
