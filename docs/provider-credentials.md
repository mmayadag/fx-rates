# Provider Credentials

ECB-only mode does not require any provider credentials.

The runtime no longer reads provider-specific API keys, usernames, passwords, or `DISABLED_PROVIDERS`. If those variables still exist in an old deployment environment, they are ignored by the application after this simplification.

`currencyapi.com` credentials are also not used by this repo. If `CURAPI` rows exist in `providers` or `rates`, they are expected to be written by another API.
