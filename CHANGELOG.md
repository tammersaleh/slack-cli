# Changelog

## [2.0.0](https://github.com/tammersaleh/slack-cli/compare/v1.2.2...v2.0.0) (2026-05-05)


### ⚠ BREAKING CHANGES

* agents that relied on the implicit public-only filter will now see private channels, MPIMs, and DMs in the output. Add `--type public` to restore the prior behavior.

### Features

* list all channel types by default ([72b157b](https://github.com/tammersaleh/slack-cli/commit/72b157b69372b7c126c766dca2bece01a86d355e))

## [1.2.2](https://github.com/tammersaleh/slack-cli/compare/v1.2.1...v1.2.2) (2026-05-04)


### Bug Fixes

* tighten draft block-kit guidance in skill output ([c589242](https://github.com/tammersaleh/slack-cli/commit/c589242fe536b0624061a38a68ccc2903089daf2))

## [1.2.1](https://github.com/tammersaleh/slack-cli/compare/v1.2.0...v1.2.1) (2026-04-25)


### Bug Fixes

* include IM channels in default list output ([3ad257b](https://github.com/tammersaleh/slack-cli/commit/3ad257bbd2ae19b0a1c6f74a94816d3099ec9253))
* stage drafts as composer-originated ([aa648f6](https://github.com/tammersaleh/slack-cli/commit/aa648f6ba7c7b7bde19d448f7c6f7e495f4c99e3))

## [1.2.0](https://github.com/tammersaleh/slack-cli/compare/v1.1.0...v1.2.0) (2026-04-23)


### Features

* reject rich_text_list directly after rich_text_section ([18d20fc](https://github.com/tammersaleh/slack-cli/commit/18d20fcc9b3c241a384a91f44b87403e6a0129a6))

## [1.1.0](https://github.com/tammersaleh/slack-cli/compare/v1.0.2...v1.1.0) (2026-04-22)


### Features

* add recovery hints to common error paths ([7e675e7](https://github.com/tammersaleh/slack-cli/commit/7e675e7a0cd83b6dea3bc939f98017f3bbb5d31e))


### Bug Fixes

* reject non-rich_text draft blocks to prevent content stripping ([1dea852](https://github.com/tammersaleh/slack-cli/commit/1dea8526e7a8fb5c780b06cf07ccc88b3b0a7c42))

## [1.0.2](https://github.com/tammersaleh/slack-cli/compare/v1.0.1...v1.0.2) (2026-04-21)


### Bug Fixes

* skip read+parse of stale file caches ([e118830](https://github.com/tammersaleh/slack-cli/commit/e118830f2767fffd2e0d1db186d2a8f78cfc318f))

## [1.0.1](https://github.com/tammersaleh/slack-cli/compare/v1.0.0...v1.0.1) (2026-04-21)


### Bug Fixes

* cache channel warm failure to avoid retry storm ([dd0ec28](https://github.com/tammersaleh/slack-cli/commit/dd0ec28a8d97710562a3ad9261fc0e85ad254705))

## 1.0.0 (2026-04-19)


### Features

* add Homebrew install path ([a551094](https://github.com/tammersaleh/slack-cli/commit/a551094f6f039ef26f844d6247b6682c799f38ac))
