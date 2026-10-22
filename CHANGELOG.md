# Changelog

## [Unreleased]

### Features

* pin proxy v0.5.0 and expose spec targets and upstream timeouts

## [0.4.4](https://github.com/barzoj-co/llmentary-go/compare/v0.4.3...v0.4.4) (2026-10-22)


### Bug Fixes

* fetch isolation vectors from the public GitHub archive ([#38](https://github.com/barzoj-co/llmentary-go/issues/38)) ([6dfa7be](https://github.com/barzoj-co/llmentary-go/commit/6dfa7be82d07721d059eaaa8f53a619a86a2beb1))
* list still-running calls from wait 504 ([#37](https://github.com/barzoj-co/llmentary-go/issues/37)) ([f962a1d](https://github.com/barzoj-co/llmentary-go/commit/f962a1d24f743c579e126958c49b0566b8aa5c28))

## [0.4.3](https://github.com/barzoj-co/llmentary-go/compare/v0.4.2...v0.4.3) (2026-10-22)


### Bug Fixes

* wait IsolatedFrom on the other session ([#33](https://github.com/barzoj-co/llmentary-go/issues/33)) ([1a2c3a2](https://github.com/barzoj-co/llmentary-go/commit/1a2c3a20f8524a83fd8201c13478d5f9addee776))

## [0.4.2](https://github.com/barzoj-co/llmentary-go/compare/v0.4.1...v0.4.2) (2026-10-22)


### Bug Fixes

* **deps:** bundle proxy v0.4.1 ([#31](https://github.com/barzoj-co/llmentary-go/issues/31)) ([5c015bf](https://github.com/barzoj-co/llmentary-go/commit/5c015bfcf06bdd730d3eb637d346000f9ca2c667))

## [0.4.1](https://github.com/barzoj-co/llmentary-go/compare/v0.4.0...v0.4.1) (2026-10-21)


### Bug Fixes

* align IsolatedFrom with empty-other and later-call spec ([#29](https://github.com/barzoj-co/llmentary-go/issues/29)) ([7d40792](https://github.com/barzoj-co/llmentary-go/commit/7d4079281b7fa78b338040344d07137e3f1f74f2))

## [0.4.0](https://github.com/barzoj-co/llmentary-go/compare/v0.3.0...v0.4.0) (2026-10-21)


### Features

* add ExpectNoCalls and empty IsolatedFrom ([#24](https://github.com/barzoj-co/llmentary-go/issues/24)) ([e71b166](https://github.com/barzoj-co/llmentary-go/commit/e71b1662b1a81d78957b50f2b6c294c8fa4f3f8c))
* expose typed ClientAborted and AbortPhase ([#22](https://github.com/barzoj-co/llmentary-go/issues/22)) ([676d54b](https://github.com/barzoj-co/llmentary-go/commit/676d54bec60d7faadc83edf8e5668e3ac4f55813))


### Bug Fixes

* label request-only spec checks in Summary ([#23](https://github.com/barzoj-co/llmentary-go/issues/23)) ([0ab0529](https://github.com/barzoj-co/llmentary-go/commit/0ab05291e462d4baa1508a8f2a9b32b325251671))
* match IsolatedFrom on foreign user turns only ([#21](https://github.com/barzoj-co/llmentary-go/issues/21)) ([22adbba](https://github.com/barzoj-co/llmentary-go/commit/22adbba670874f97a87527230e5788c43ad90f4c))
* pin proxy v0.4.0 ([#26](https://github.com/barzoj-co/llmentary-go/issues/26)) ([81789ee](https://github.com/barzoj-co/llmentary-go/commit/81789ee43c11a10460f6dd5ff0d710a6a848a3d8))

## [0.3.0](https://github.com/barzoj-co/llmentary-go/compare/v0.2.0...v0.3.0) (2026-10-21)


### Features

* add IsolatedFrom, wait 404 mapping, and seq in summaries ([#18](https://github.com/barzoj-co/llmentary-go/issues/18)) ([c2ab8a0](https://github.com/barzoj-co/llmentary-go/commit/c2ab8a02de11d4529e302685640089546030c992))


### Bug Fixes

* fail vacuous empty filtered assertions ([#15](https://github.com/barzoj-co/llmentary-go/issues/15)) ([4d10519](https://github.com/barzoj-co/llmentary-go/commit/4d10519e402bf0178332f23cc033e0c91045f3d3))
* pin proxy v0.2.2 ([#19](https://github.com/barzoj-co/llmentary-go/issues/19)) ([3651223](https://github.com/barzoj-co/llmentary-go/commit/3651223d18edd731c834058eae5ba85b1f9a96c3))
* pin proxy v0.3.0 ([#20](https://github.com/barzoj-co/llmentary-go/issues/20)) ([a9e83c0](https://github.com/barzoj-co/llmentary-go/commit/a9e83c0480bc33d2d6dd8a90b2cf33321671a7d7))

## [0.2.0](https://github.com/barzoj-co/llmentary-go/compare/v0.1.0...v0.2.0) (2026-10-21)


### Features

* add PathBaseURL, tool filters, mark/since, and mention helpers ([#12](https://github.com/barzoj-co/llmentary-go/issues/12)) ([7d05522](https://github.com/barzoj-co/llmentary-go/commit/7d05522402f65b05605b756bb2268569c6b24d79))
* add session list, min_calls, default upstream, and spec accessors ([#11](https://github.com/barzoj-co/llmentary-go/issues/11)) ([8beb416](https://github.com/barzoj-co/llmentary-go/commit/8beb4163957fb062496cfd61d4d205cf883f3508))
* rename Lab identifiers and slim README ([#8](https://github.com/barzoj-co/llmentary-go/issues/8)) ([674864b](https://github.com/barzoj-co/llmentary-go/commit/674864b90f7d07e0e1b09f02321b37c827a6fff5))


### Bug Fixes

* build with Go 1.26.10 ([#14](https://github.com/barzoj-co/llmentary-go/issues/14)) ([42cb1dd](https://github.com/barzoj-co/llmentary-go/commit/42cb1dde7b6b18c759bb400b1fc82dc35c3959d8))
* pin proxy v0.2.0 ([#13](https://github.com/barzoj-co/llmentary-go/issues/13)) ([24be620](https://github.com/barzoj-co/llmentary-go/commit/24be6205d358521deed99fbe74163c6367a8a150))
* pin proxy v0.2.1 ([#16](https://github.com/barzoj-co/llmentary-go/issues/16)) ([b92301c](https://github.com/barzoj-co/llmentary-go/commit/b92301cb4afa24a2c982e7b1a0138979aadfa71f))
* unify wait defaults and LLMENTARY_WAIT_* env names ([#10](https://github.com/barzoj-co/llmentary-go/issues/10)) ([e0ee4ee](https://github.com/barzoj-co/llmentary-go/commit/e0ee4ee85962a0143fc5a5a97dae382582b246b1))

## 0.1.0 (2026-10-19)


### ⚠ BREAKING CHANGES

* rename Go client from llm-test-lab to llmentary ([#4](https://github.com/barzoj-co/llmentary-go/issues/4))

### Features

* pin first release to 0.1.0 and proxy v0.1.0 ([#7](https://github.com/barzoj-co/llmentary-go/issues/7)) ([d920df1](https://github.com/barzoj-co/llmentary-go/commit/d920df1996d651cb2e0ceeaefba42eacdde6abd7))


### Code Refactoring

* rename Go client from llm-test-lab to llmentary ([#4](https://github.com/barzoj-co/llmentary-go/issues/4)) ([54ea308](https://github.com/barzoj-co/llmentary-go/commit/54ea308d86d71de6525e5e4640445d5fadaa805d))
