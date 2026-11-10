# Changelog

## [5.12.0](https://github.com/tammersaleh/slack-cli/compare/v5.11.2...v5.12.0) (2026-11-10)


### Features

* report Slack's result total in the search and file list trailer ([ce7802f](https://github.com/tammersaleh/slack-cli/commit/ce7802f3202605830295b3bb910d2b9c45ba6a10))


### Bug Fixes

* refuse a search or file page above 100 instead of repeating page 1 ([3cc7476](https://github.com/tammersaleh/slack-cli/commit/3cc7476a59ad7eb7246b1126659565dc08486eed))
* trace the search query as sent, after user names are resolved ([0edf354](https://github.com/tammersaleh/slack-cli/commit/0edf35499bce9acdd9437cbef5d5b12caaac7dce))

## [5.11.2](https://github.com/tammersaleh/slack-cli/compare/v5.11.1...v5.11.2) (2026-11-10)


### Bug Fixes

* answer a repeat channel-name miss without walking the workspace again ([49b6994](https://github.com/tammersaleh/slack-cli/commit/49b6994b942b44cd21e852cbe27002c4542fb856))
* trace and retry the resolver's user and channel lookups ([c9e849e](https://github.com/tammersaleh/slack-cli/commit/c9e849e9031a91726ad291ac6180891480bf5014))

## [5.11.1](https://github.com/tammersaleh/slack-cli/compare/v5.11.0...v5.11.1) (2026-11-10)


### Bug Fixes

* end bulk commands with a _meta trailer when they stop early ([92ca0cc](https://github.com/tammersaleh/slack-cli/commit/92ca0cc6cfa7cdbff10c265b059a7c29ccd6a276))
* end user info with a _meta trailer when it stops early ([ed5fea9](https://github.com/tammersaleh/slack-cli/commit/ed5fea9f8ad35b039b80a30d1506248299da466a))
* exit 2 on missing_scope ([2d9dc0b](https://github.com/tammersaleh/slack-cli/commit/2d9dc0b114a12c22428b1049a9d1c5027cf53125))
* recognize rate limits on internal API calls ([624f81b](https://github.com/tammersaleh/slack-cli/commit/624f81b85a8217a366d74e4e63454e2d42038d94))
* report a body-level rate limit as rate_limited and retry it ([94418c1](https://github.com/tammersaleh/slack-cli/commit/94418c109df477827655dc15c05ead907fae73a1))
* retry rate-limited internal reads and section moves ([f191a90](https://github.com/tammersaleh/slack-cli/commit/f191a908c841cdaee1e3bf75faa70d150e098a51))
* retry users.info and stop user info on a failed email lookup ([e284655](https://github.com/tammersaleh/slack-cli/commit/e284655869b5f24013e7d30744ddbb6617656cd0))
* stop bulk commands on --timeout and on failed email lookups ([17c2e83](https://github.com/tammersaleh/slack-cli/commit/17c2e83f872241f17938a1627da4c954a78edfdc))

## [5.11.0](https://github.com/tammersaleh/slack-cli/compare/v5.10.1...v5.11.0) (2026-11-06)


### Features

* **draft:** accept divider blocks in draft payloads ([11bdd76](https://github.com/tammersaleh/slack-cli/commit/11bdd7697b0af65f6542519365263da8c3660335))
* **draft:** attribute Markdown errors after a divider to their own line ([131596f](https://github.com/tammersaleh/slack-cli/commit/131596fb6f4ca46ef45d6a4b334f05ecd1698ecc))
* **draft:** convert Markdown thematic breaks to divider blocks ([6ff5e93](https://github.com/tammersaleh/slack-cli/commit/6ff5e939f1d291f455ac55c40c3b75b31d8b5f01))

## [5.10.1](https://github.com/tammersaleh/slack-cli/compare/v5.10.0...v5.10.1) (2026-11-06)


### Bug Fixes

* **draft:** treat a header block as an absorption boundary ([470bb21](https://github.com/tammersaleh/slack-cli/commit/470bb2180e8c1a6be1aa6dc6aab3e1d0172581c5))

## [5.10.0](https://github.com/tammersaleh/slack-cli/compare/v5.9.2...v5.10.0) (2026-11-06)


### Features

* **draft:** accept header blocks in draft payloads ([a39762f](https://github.com/tammersaleh/slack-cli/commit/a39762f61bb2d9bd9f684e3c2ef1526fb849f475))
* **draft:** convert Markdown headings to header blocks ([79aadbd](https://github.com/tammersaleh/slack-cli/commit/79aadbd2a4ed990d59db88dacd50e1767f7cb199))
* **draft:** name header blocks in error hints and line-attribute heading errors ([40ed9c9](https://github.com/tammersaleh/slack-cli/commit/40ed9c9f6157e56813575eafdb9398ba183ef6f4))

## [5.9.2](https://github.com/tammersaleh/slack-cli/compare/v5.9.1...v5.9.2) (2026-11-05)


### Bug Fixes

* **homebrew:** rename cask to tammersaleh-slack-cli ([65a88df](https://github.com/tammersaleh/slack-cli/commit/65a88df79e41a3ae53bfd41c317ab4da62608536))

## [5.9.1](https://github.com/tammersaleh/slack-cli/compare/v5.9.0...v5.9.1) (2026-11-04)


### Bug Fixes

* **file:** report bytes written by file download ([0351ac0](https://github.com/tammersaleh/slack-cli/commit/0351ac0aa3d3d2dfcfa15c45fc27dde4685457ef))

## [5.9.0](https://github.com/tammersaleh/slack-cli/compare/v5.8.0...v5.9.0) (2026-11-04)


### Features

* **list:** add list record add and update ([3f26645](https://github.com/tammersaleh/slack-cli/commit/3f26645e5b08e3473530ef9f048c6088ee4c0bfe))

## [5.8.0](https://github.com/tammersaleh/slack-cli/compare/v5.7.0...v5.8.0) (2026-11-04)


### Features

* **list:** add list create, update, and column commands ([aad992e](https://github.com/tammersaleh/slack-cli/commit/aad992e134908b9121d6f5576fc8a7d97b4cc8d7))

## [5.7.0](https://github.com/tammersaleh/slack-cli/compare/v5.6.0...v5.7.0) (2026-11-04)


### Features

* **list:** add list info, schema, records, find ([57d85e2](https://github.com/tammersaleh/slack-cli/commit/57d85e2688ae02e85c27dccbba5ccdbeda2647aa))

## [5.6.0](https://github.com/tammersaleh/slack-cli/compare/v5.5.0...v5.6.0) (2026-11-04)


### Features

* **slackurl:** accept /lists/ URLs as file links ([fcc6589](https://github.com/tammersaleh/slack-cli/commit/fcc6589fa40ac834a2c7b28e96dd0975cc538a20))

## [5.5.0](https://github.com/tammersaleh/slack-cli/compare/v5.4.0...v5.5.0) (2026-11-03)


### Features

* **draft:** accept lists and code inside --markdown quotes ([19811c0](https://github.com/tammersaleh/slack-cli/commit/19811c0c81062081036bf98ca76a11b184b37b6c))

## [5.4.0](https://github.com/tammersaleh/slack-cli/compare/v5.3.0...v5.4.0) (2026-10-19)


### Features

* add channel invite and kick ([a3e8a32](https://github.com/tammersaleh/slack-cli/commit/a3e8a32ee52e19637e4ff160afd76c9ce0b2068b))

## [5.3.0](https://github.com/tammersaleh/slack-cli/compare/v5.2.0...v5.3.0) (2026-10-19)


### Features

* add section emoji and --emoji on section create and move ([b5d0e8b](https://github.com/tammersaleh/slack-cli/commit/b5d0e8b1fcdd10ce46a9c39054ab8c927c707391))

## [5.2.0](https://github.com/tammersaleh/slack-cli/compare/v5.1.0...v5.2.0) (2026-10-17)


### Features

* add section rename ([7e5e376](https://github.com/tammersaleh/slack-cli/commit/7e5e3769fb24004cc0084b7be12311a6c7699ea5))


### Bug Fixes

* send emoji on section create ([33ae84d](https://github.com/tammersaleh/slack-cli/commit/33ae84d85587073909374334702b14d81fcddea8))

## [5.1.0](https://github.com/tammersaleh/slack-cli/compare/v5.0.5...v5.1.0) (2026-10-16)


### Features

* add --markdown input to draft create and update ([1adea9a](https://github.com/tammersaleh/slack-cli/commit/1adea9a1ab5949447d2737faf3f86a7b6cf49c9b))

## [5.0.5](https://github.com/tammersaleh/slack-cli/compare/v5.0.4...v5.0.5) (2026-09-29)


### Bug Fixes

* document per-emoji reaction search modifiers ([2ff276f](https://github.com/tammersaleh/slack-cli/commit/2ff276fb424f7dc55839fdd6daa9c328eb928114))

## [5.0.4](https://github.com/tammersaleh/slack-cli/compare/v5.0.3...v5.0.4) (2026-09-21)


### Bug Fixes

* guard the @" slice in search modifier quote handling ([d835227](https://github.com/tammersaleh/slack-cli/commit/d83522737f3617aa1fe08be31e60097f4c3c1d0a))
* resolve [@name](https://github.com/name) in search from:/to:/in: modifiers ([b0e1d86](https://github.com/tammersaleh/slack-cli/commit/b0e1d867de379d096d7c2068299afb5e75a160c5))
* resolve quoted from:/to: names without a leading @ ([29abed1](https://github.com/tammersaleh/slack-cli/commit/29abed11bbd23aa26ef1150c8342dc12f8504295))

## [5.0.3](https://github.com/tammersaleh/slack-cli/compare/v5.0.2...v5.0.3) (2026-09-04)


### Bug Fixes

* stop the --workspace help from promising name matching ([7bdeb88](https://github.com/tammersaleh/slack-cli/commit/7bdeb88a9e9e9de4c7195c88b7b6609618309cbc))

## [5.0.2](https://github.com/tammersaleh/slack-cli/compare/v5.0.1...v5.0.2) (2026-09-04)


### Bug Fixes

* make the auth login workspace hint reflect the current env ([87589ab](https://github.com/tammersaleh/slack-cli/commit/87589ab004968147bb03c3cc8d50a6cb78c9930e))

## [5.0.1](https://github.com/tammersaleh/slack-cli/compare/v5.0.0...v5.0.1) (2026-09-01)


### Bug Fixes

* resolve draft create channel names on the workspace client ([79419cb](https://github.com/tammersaleh/slack-cli/commit/79419cbbfc1bff69341a1459eb249372c5c79795))

## [5.0.0](https://github.com/tammersaleh/slack-cli/compare/v4.0.2...v5.0.0) (2026-08-03)


### ⚠ BREAKING CHANGES

* a --enrich run whose items cannot all be looked up now exits 1 after writing every row, where it used to exit 0 with the fields missing. Rows can carry a new enrich_error field.

### Bug Fixes

* request the history scopes in the OAuth flow ([c9efc16](https://github.com/tammersaleh/slack-cli/commit/c9efc16933493e8db2580bfaad2f7a5c6c5d7361))
* resolve thread replies in saved list --enrich ([67440f6](https://github.com/tammersaleh/slack-cli/commit/67440f6c4bdc4ae0f9c0c5662406f8701e2b7fdb))

## [4.0.2](https://github.com/tammersaleh/slack-cli/compare/v4.0.1...v4.0.2) (2026-07-28)


### Bug Fixes

* give _meta.error a stable code instead of Go error text ([da9454a](https://github.com/tammersaleh/slack-cli/commit/da9454a4f2f3ad6ac48e217a42cba232cddf9d20))
* keep --after/--before on every page of a message walk ([19bfda3](https://github.com/tammersaleh/slack-cli/commit/19bfda34a9074af156dc654346866a842f94315b))
* make --has-unread actually find unread conversations ([4cd56be](https://github.com/tammersaleh/slack-cli/commit/4cd56be5dd1bdc6393deee73c25a03bae1eaa258))
* make --query search every page it can, and say when it can't ([75edd8e](https://github.com/tammersaleh/slack-cli/commit/75edd8e6f0b33911d8defdd443ee54fd06e0d017))
* resolve a channel name from your own conversations first ([cc0ac1d](https://github.com/tammersaleh/slack-cli/commit/cc0ac1d9fc382f1bd008d56984462073bdcb3cce))
* stop offering a rejected cursor as the page to resume from ([dc65038](https://github.com/tammersaleh/slack-cli/commit/dc650383385d0b922bb5d999babdabae63dba25f))

## [4.0.1](https://github.com/tammersaleh/slack-cli/compare/v4.0.0...v4.0.1) (2026-07-27)


### Bug Fixes

* stop shipping a real channel name in command help ([6981463](https://github.com/tammersaleh/slack-cli/commit/69814632bb98e376f8f0ee8a056a7826a5953e12))

## [4.0.0](https://github.com/tammersaleh/slack-cli/compare/v3.5.3...v4.0.0) (2026-07-27)


### ⚠ BREAKING CHANGES

* the default `channel list` output changes in four ways.

### Features

* list your own channels via users.conversations ([2dd9967](https://github.com/tammersaleh/slack-cli/commit/2dd9967581a30c0fe3ea7d030ad8b2c4631ebc60))

## [3.5.3](https://github.com/tammersaleh/slack-cli/compare/v3.5.2...v3.5.3) (2026-07-27)


### Bug Fixes

* mark truncated list output in the _meta trailer ([ba92e4f](https://github.com/tammersaleh/slack-cli/commit/ba92e4f1aed18437a571c59d0ef3ca6f5fcd3657))

## [3.5.2](https://github.com/tammersaleh/slack-cli/compare/v3.5.1...v3.5.2) (2026-07-09)


### Bug Fixes

* make section move actually move channels ([cd7f9ca](https://github.com/tammersaleh/slack-cli/commit/cd7f9ca097703fb5ef0a653dde31a459c0d15ccf))

## [3.5.1](https://github.com/tammersaleh/slack-cli/compare/v3.5.0...v3.5.1) (2026-07-09)


### Bug Fixes

* resolve thread replies in message get ([dd3dc95](https://github.com/tammersaleh/slack-cli/commit/dd3dc95af1af75f00c8cf6b78100622cb5dc9f56))

## [3.5.0](https://github.com/tammersaleh/slack-cli/compare/v3.4.1...v3.5.0) (2026-06-25)


### Features

* draft create to new DMs/MPDMs via user recipients ([0f15008](https://github.com/tammersaleh/slack-cli/commit/0f15008c4750d720d6743990d9a90a2e24db02a9))

## [3.4.1](https://github.com/tammersaleh/slack-cli/compare/v3.4.0...v3.4.1) (2026-06-23)


### Bug Fixes

* resolve channel names via conversations.info during enrichment ([83ce710](https://github.com/tammersaleh/slack-cli/commit/83ce71012e45d036b14eb2462cf9d65f391196da))
* resolve channel names via conversations.info during enrichment ([8fca518](https://github.com/tammersaleh/slack-cli/commit/8fca518c3fc3445631ff972d6d2d0ec5837d7679))

## [3.4.0](https://github.com/tammersaleh/slack-cli/compare/v3.3.0...v3.4.0) (2026-06-10)


### Features

* add channel managers command ([0ef8307](https://github.com/tammersaleh/slack-cli/commit/0ef83077bb763fbffccf09c91e41cbb7fa747b6d))

## [3.3.0](https://github.com/tammersaleh/slack-cli/compare/v3.2.1...v3.3.0) (2026-06-02)


### Features

* add user info --full and user manager-chain ([1b3c701](https://github.com/tammersaleh/slack-cli/commit/1b3c70104b95c7694ee22a137f5207b20c5feada))

## [3.2.1](https://github.com/tammersaleh/slack-cli/compare/v3.2.0...v3.2.1) (2026-05-30)


### Bug Fixes

* reject data_table in drafts (not draftable) ([b912ee5](https://github.com/tammersaleh/slack-cli/commit/b912ee54fb169b99e80aa5db10e8cf5a15e75a11))

## [3.2.0](https://github.com/tammersaleh/slack-cli/compare/v3.1.1...v3.2.0) (2026-05-29)


### Features

* draft tables via attachments (blocks object + --table) ([db26a73](https://github.com/tammersaleh/slack-cli/commit/db26a73b78003802b974c582572fc2ea9ef26e35))

## [3.1.1](https://github.com/tammersaleh/slack-cli/compare/v3.1.0...v3.1.1) (2026-05-29)


### Bug Fixes

* guide table-block drafts to the preformatted fallback ([bc1a8b2](https://github.com/tammersaleh/slack-cli/commit/bc1a8b21f9a285ff0c63ec590e326e3045842232))

## [3.1.0](https://github.com/tammersaleh/slack-cli/compare/v3.0.0...v3.1.0) (2026-05-28)


### Features

* accept file URLs in file info and download ([7c94c64](https://github.com/tammersaleh/slack-cli/commit/7c94c64fb92ccf8c9f4cd488bb8d7cbed087cace))
* accept message URLs in message get ([385eafd](https://github.com/tammersaleh/slack-cli/commit/385eafd8173e79f832bc404cce4ca17711e2c157))
* accept message URLs in reaction list ([4b508d6](https://github.com/tammersaleh/slack-cli/commit/4b508d61fff8c191b23c7ae774acdfacf126113b))
* accept Slack URLs for channel and user args ([ddefdac](https://github.com/tammersaleh/slack-cli/commit/ddefdacb1aabdc9a54bafbd7128598f7ff634c3d))
* accept thread URLs in thread list ([018aa59](https://github.com/tammersaleh/slack-cli/commit/018aa5921e619050290a1608ce6931a42d05b4ba))
* parse Slack URLs into typed channel/user/file/message refs ([6bd1d5d](https://github.com/tammersaleh/slack-cli/commit/6bd1d5dcf2982f38025b063d8c3d552dc0dc862f))

## [3.0.0](https://github.com/tammersaleh/slack-cli/compare/v2.0.1...v3.0.0) (2026-05-26)


### ⚠ BREAKING CHANGES

* the `slack skill` subcommand is gone.

### Features

* drop slack skill, ship skills/slack-cli/SKILL.md ([481462e](https://github.com/tammersaleh/slack-cli/commit/481462ec8770ef42c662f6009aed534a0ddddb76))


### Bug Fixes

* **draft:** point invalid_blocks rejections at the skill install ([310757f](https://github.com/tammersaleh/slack-cli/commit/310757f22b8bb460146c4fb162cc14d0df197814))

## [2.0.1](https://github.com/tammersaleh/slack-cli/compare/v2.0.0...v2.0.1) (2026-05-15)


### Bug Fixes

* **draft:** allow section before list/code/quote with \n terminator ([5e189c1](https://github.com/tammersaleh/slack-cli/commit/5e189c187986fa2368e0776891b9b1671fd41d18))

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
