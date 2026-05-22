CREATE TABLE currencies (
    iso_code   VARCHAR(3) PRIMARY KEY,
    start_date DATE       NOT NULL,
    end_date   DATE       NOT NULL
);

CREATE TABLE currency_coverages (
    provider_key VARCHAR(10) NOT NULL REFERENCES providers(key) ON DELETE CASCADE,
    iso_code     VARCHAR(3)  NOT NULL,
    start_date   DATE,
    end_date     DATE,

    PRIMARY KEY (provider_key, iso_code)
);
