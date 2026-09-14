# Usage & cost

**Admin → Usage & cost** answers one question: what is this storage costing me,
roughly, and where is it going.

filex does not meter your provider's bill. It reads the report the provider
already writes, normalises it, and prices it with a table you can edit. Today
that means **Backblaze B2**; the reader is an interface, so a second provider is
a file, not a redesign.

> ⚠ Everything on that page is an **estimate**, even though the numbers come
> from the provider's own report. Free allowances are applied over the range you
> asked for rather than over the provider's billing month, taxes and minimums
> are not modelled, and providers round differently. It is for "where is my
> money going", not for reconciling an invoice.

## How it works

Backblaze writes a usage report once a day into a bucket it owns, named
`b2-reports-<accountId>`, under one folder per day. filex reads those files over
the same S3 API it already speaks — no new dependency, no new credential type —
and caches what it parsed, so opening the page does not fetch a month of CSVs
every time.

```
b2-reports-<accountId>/
  2026-09-11/
    usage.account-<accountId>.csv     ← a standalone account
```

The account-level line and the per-bucket lines are rows of the **same** file;
the account line is the one with an empty `bucket_id`. An account inside a
Backblaze organization or group gets `usage.<resource>.<location>.csv` or
`usage.group-<groupId>.<location>.csv` instead, and filex reads every
`usage.*.csv` in the day's folder — skipping `usage.audit-*` and the
`*.reportingLocations.csv` lookup table, which are not usage.

## Setting it up

**1. Make a read-only application key.** In the Backblaze console, create an
application key with read access. ⚠ **Not the master key** — B2's S3 endpoint
rejects it with `InvalidAccessKeyId: Malformed Access Key Id`, which reads like
a wrong secret and is not one. See [STORAGE.md](STORAGE.md#s3--s3-compatible).

**2. Attach the report bucket as a storage.** *Storages → Add*, driver `s3`,
endpoint `https://s3.<region>.backblazeb2.com`, bucket `b2-reports-<accountId>`,
**read-only**. It holds no files of yours; it is a data source.

**3. Point the page at it.** On *Usage & cost*:

| Field | What it is |
|---|---|
| **Provider** | `Backblaze B2` |
| **Report storage** | the storage you just made |
| **Account ID** | your B2 account id — the same one in the bucket's name. Optional: the day's folder is listed either way, and the id is only used to try `usage.account-<id>.csv` by name when it cannot be |
| **Path prefix** | only if the reports are not at the root of that bucket |

The same values are rows in the settings table — `usage.provider`,
`usage.report_storage`, `usage.account_id`, `usage.prefix` and `usage.pricing`
— so they can also be written through `PATCH /api/admin/settings`. ⚠ There is
**no environment variable** for any of them: unlike the antivirus family they
are not seeded at first boot, so a compose file cannot configure this page.
Set them here, or through the settings API.

> **The report appears the day after the account does.** A brand-new B2 account
> has no `b2-reports-…` bucket at all until Backblaze writes its first daily
> report. Until then the page has nothing to read, and it says so rather than
> drawing an empty chart.

## Pricing is configuration, not code

The `usage.pricing` setting holds the table the estimate is computed with, and
the free allowances are their own fields rather than constants buried in a
formula — so a price change is an edit, not a release.

The defaults are Backblaze's list prices as read on 2026-09-11:

| Line | Default | Free allowance |
|---|---|---|
| Storage | $0.00695 / GB / month | first 10 GB |
| Download | $0.01 / GB | 3× what you store |
| Class A/B/C transactions | free | — |
| Class D transactions | $0.004 / 10,000 | 2,500 / day |

The page shows the allowance line separately from the billable line, because
"your downloads were free this month" is usually the thing worth knowing.

## Two rows that must never be added together

The provider reports an **account-level** row and a **per-bucket** row, and the
same transactions appear in both. Summing them overstates by exactly the amount
nobody notices, so filex keeps them apart: buckets are summed among themselves,
and the account row is rendered as its own line with a note saying what it is.

## The API

```
GET /api/admin/usage?days=30
GET /api/admin/usage?from=2026-08-01&to=2026-08-31
```

Supertenant-only. It answers `502` with a `hint` when the configuration points
at something it cannot read — a missing storage, a bucket with no reports in it,
a prefix that matches nothing — rather than an empty report, because an empty
report reads as "you used nothing".

## See also

- [STORAGE.md](STORAGE.md) — attaching an S3 storage, and the B2 master-key trap
- [CONFIGURATION.md](CONFIGURATION.md) — the environment variables filex does read
