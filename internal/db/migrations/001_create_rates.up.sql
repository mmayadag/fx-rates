CREATE TABLE rates (
    date     DATE             NOT NULL,
    base     VARCHAR(3)       NOT NULL,
    quote    VARCHAR(3)       NOT NULL,
    rate     DOUBLE PRECISION NOT NULL,
    provider VARCHAR(10)      NOT NULL,

    CONSTRAINT rates_pkey PRIMARY KEY (provider, date, base, quote)
);

CREATE INDEX idx_rates_date ON rates (date);
CREATE INDEX idx_rates_provider_quote ON rates (provider, quote);
CREATE INDEX idx_rates_provider_base ON rates (provider, base);
