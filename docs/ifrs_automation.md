# IFRS automation CLI

The IFRS command reads company and account settings from a JSON file. Pass it
with `--config`, or set `AGY_IFRS_CONFIG_PATH`:

```sh
agy ifrs run --config ./agy-ifrs.json
agy ifrs daemon --interval 1h --config ./agy-ifrs.json
```

`--config` takes precedence over the environment variable. `DATABASE_URL` is
required only when a non-empty configuration is available. If the path is
omitted, the file is missing or empty, or the JSON contains no companies, the
command emits a structured warning and exits without connecting to the
database. Invalid non-empty JSON and job configuration errors are returned.

The root JSON object has a `companies` array. The example below illustrates
the complete configuration shape; replace all sample UUIDs and lease terms
with real values.

```json
{
  "companies": [
    {
      "company_id": "11111111-1111-4111-8111-111111111111",
      "journal_id": "22222222-2222-4222-8222-222222222222",
      "created_by": "agy-ifrs",
      "ifrs9": {
        "impairment_account_id": "33333333-3333-4333-8333-333333333333",
        "allowance_account_id": "44444444-4444-4444-8444-444444444444",
        "currency": "USD",
        "provision_matrix": {
          "current": "0.01",
          "1_30": "0.03",
          "31_60": "0.08",
          "61_90": "0.20",
          "over_90": "0.50"
        },
        "sicr_receivable_ids": [],
        "defaulted_receivable_ids": []
      },
      "ifrs15": {
        "revenue_account_id": "55555555-5555-4555-8555-555555555555",
        "contract_liability_id": "66666666-6666-4666-8666-666666666666",
        "contract_asset_id": "77777777-7777-4777-8777-777777777777"
      },
      "ifrs16": {
        "leases": [
          {
            "id": "88888888-8888-4888-8888-888888888888",
            "contract": {
              "commencement_date": "2025-01-01T00:00:00Z",
              "annual_discount_rate": "0.12",
              "payments": [
                {
                  "due_date": "2025-01-31T00:00:00Z",
                  "amount": {"amount": "1000.00", "currency": "USD"}
                }
              ],
              "initial_direct_costs": {"amount": "0.00", "currency": "USD"},
              "prepayments": {"amount": "0.00", "currency": "USD"},
              "lease_incentives": {"amount": "0.00", "currency": "USD"},
              "useful_life_periods": 1
            },
            "accounts": {
              "rou_asset": "99999999-9999-4999-8999-999999999999",
              "lease_liability": "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
              "cash": "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb",
              "interest_expense": "cccccccc-cccc-4ccc-8ccc-cccccccccccc",
              "depreciation_expense": "dddddddd-dddd-4ddd-8ddd-dddddddddddd",
              "accumulated_depreciation": "eeeeeeee-eeee-4eee-8eee-eeeeeeeeeeee"
            }
          }
        ]
      }
    }
  ]
}
```

The standard job builder registers IFRS 9 ECL, IFRS 15 revenue recognition,
and IFRS 16 lease-payment/depreciation jobs per configured company. IFRS 9
uses the company's receivable open lines and reads its allowance balance as
of the period end; the optional receivable ID lists identify SICR and
defaulted exposures. IFRS 15 reads and updates contracts in the database.
IFRS 16 lease terms are supplied in configuration because lease persistence is
not currently provided by the database schema. Lease payment dates must be
strictly increasing and include any guaranteed residual amounts that form
part of the liability cash flows.

`run` processes the previous UTC calendar month once. `daemon` repeats the
same prior-month check at the configured interval (one hour by default).
Journal-entry posting remains idempotent. Structured logs report each job's
success or failure, and a failed job does not prevent independent jobs from
running for that period.
