-- +goose Up
-- +goose StatementBegin
-- One wallet per customer, in Toman. Money held in USDT (a USDT top-up, a USDT gift code, the license wallet of before) is converted to Toman at the
-- live rate by core (two ledger entries of this kind, one out of the USDT wallet and one into the Toman wallet), and what is priced in USDT (a
-- license) is charged in Toman at the live rate. A license order keeps the USDT list price and the rate it was charged at, for the records.
ALTER TABLE ledger_entries DROP CONSTRAINT ledger_entries_kind_check;
ALTER TABLE ledger_entries ADD CONSTRAINT ledger_entries_kind_check
    CHECK (kind IN ('topup','purchase','refund','adjust','trial_grant','referral','commission','settlement','gift_code',
                    'license_purchase','license_refund','supplier_purchase','supplier_refund','wallet_convert'));
ALTER TABLE vendor_orders ADD COLUMN list_amount bigint CHECK (list_amount IS NULL OR list_amount > 0);
ALTER TABLE vendor_orders ADD COLUMN list_currency text CHECK (list_currency IS NULL OR list_currency ~ '^[A-Z]{3,5}$');
ALTER TABLE vendor_orders ADD COLUMN rate bigint CHECK (rate IS NULL OR rate > 0);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE vendor_orders DROP COLUMN IF EXISTS rate;
ALTER TABLE vendor_orders DROP COLUMN IF EXISTS list_currency;
ALTER TABLE vendor_orders DROP COLUMN IF EXISTS list_amount;
-- The ledger is append-only: conversion entries cannot be deleted, so the old rule is put back for new rows only (NOT VALID leaves the rows that are there).
ALTER TABLE ledger_entries DROP CONSTRAINT ledger_entries_kind_check;
ALTER TABLE ledger_entries ADD CONSTRAINT ledger_entries_kind_check
    CHECK (kind IN ('topup','purchase','refund','adjust','trial_grant','referral','commission','settlement','gift_code',
                    'license_purchase','license_refund','supplier_purchase','supplier_refund')) NOT VALID;
-- +goose StatementEnd
