# Rules for a change in this repository

- Run `make verify` before handing the work back: it is the check a pull
  request has to pass.
- One change per pull request. Do not merge it and do not rewrite a branch
  with a force push: a review already read does not get reconstructed.
- Money is exact: decimal, its currency and its tax base. Never a `float64`.
- A date of award is civil, not an instant: do not fold it into a timestamp.
- Strings the reader sees are in Spanish. Code, comments, commit subjects and
  pull request bodies are in English.
- The originals are kept, not summarised: a document that backs a number is
  archived with its hash and its URL.
- A gap in the coverage is stated, not filled by guessing. Missing data is
  unknown, never zero.
