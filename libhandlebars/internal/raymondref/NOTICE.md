# raymondref

A frozen copy of `github.com/luthersystems/raymond` at commit `e77462c`
(2020-07-10), the Luther fork of `github.com/aymerick/raymond` (MIT, see
LICENSE). Only the import paths are changed.

It is the reference engine for the differential test harness. **Do not edit
it.** Production code must not import it.

Luther changes against upstream `b565731`:

1. `#each` over a map iterates string keys in sorted order.
2. Printing a map or struct gives `UNPRINTABLE`.
3. The `log` and `lookup` helpers are not registered.
4. Helper argument count is strict; `*Options` is accepted only as the last parameter.
