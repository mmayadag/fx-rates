# Providers

This repository syncs ECB only, but the `providers` table may also contain externally managed metadata for `CURAPI`.

| Key | Name |
| --- | --- |
| `ECB` | European Central Bank |
| `CURAPI` | currencyapi.com |

Notes:

- `ECB` is the only runtime-synced provider in this repo.
- `CURAPI` is preserved in `providers` for rows inserted by another API.
- `CURAPI` is not registered in the runtime adapter registry and is never queued by this service.
- Expected external metadata shape:
  - `key='CURAPI'`
  - `name='currencyapi.com'` or `CurrencyAPI`
  - `data_url='https://currencyapi.com/docs'`
  - `pivot_currency` is external-system-defined
  - other optional metadata fields may be null unless owned by the external writer
