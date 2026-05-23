CREATE TABLE sync_runs (
    id            BIGSERIAL    PRIMARY KEY,
    provider      VARCHAR(10)  NOT NULL,
    mode          VARCHAR(20)  NOT NULL,
    status        VARCHAR(20)  NOT NULL,
    started_at    TIMESTAMPTZ  NOT NULL,
    finished_at   TIMESTAMPTZ  NOT NULL,
    rows_fetched  INTEGER      NOT NULL DEFAULT 0,
    rows_inserted INTEGER      NOT NULL DEFAULT 0,
    rows_skipped  INTEGER      NOT NULL DEFAULT 0,
    error_message TEXT
);

CREATE INDEX idx_sync_runs_provider_finished ON sync_runs (provider, finished_at DESC);
CREATE INDEX idx_sync_runs_status            ON sync_runs (status, finished_at DESC);
