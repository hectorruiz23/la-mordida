# La Mordida

Who wins the most public technology contracts in Spain, and which of them are
handed over with the deepest discount.

Two questions over the same data:
`/adjudicatarios` ranks the winners of an organism and a period, and `/rebajas`
ranks the contracts by the rebate between their initial budget and what was
finally awarded. Every row opens the awards and the original documents that
sustain it, and the page says how much of the period it actually covers.

One Go binary with SQLite: it imports publications from the public procurement
platform, keeps the originals on disk with their hashes, and serves the
rankings over what it stored.

## Build

```
make verify    # gofmt, build and tests, what CI runs
./bin/contratos --help
```

The project is in progress; see `AGENTS.md` for the rules a change has to
follow.
