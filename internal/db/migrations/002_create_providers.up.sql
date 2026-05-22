CREATE TABLE providers (
    key            VARCHAR(10) PRIMARY KEY,
    name           TEXT        NOT NULL,
    country_code   VARCHAR(2),
    rate_type      TEXT,
    pivot_currency VARCHAR(3),
    data_url       TEXT,
    terms_url      TEXT,
    publish_time   INTEGER,
    publish_days   VARCHAR(10),
    coverage_start DATE
);
