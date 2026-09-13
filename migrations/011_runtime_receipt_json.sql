-- Retain the exact bounded JSON representation hashed by RuntimeReceiptDigest.
-- jsonb reorders nested provider log/trace objects and normalizes JSON numbers,
-- invalidating an otherwise immutable receipt. json preserves future input text.
-- Existing rows retain their already-normalized representation and original
-- digest. Reads reject mismatches; this migration cannot recover lost source text.
ALTER TABLE runtime_receipts ALTER COLUMN receipt TYPE json USING receipt::json;
