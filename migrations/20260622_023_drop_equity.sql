-- 20260622_023_drop_equity
-- The "Equity (advisory)" feature was removed from the product — gitstate
-- surfaces contribution DATA but does not itself recommend equity/ownership
-- splits. Drop the now-unused table. Forward-only.
DROP TABLE IF EXISTS equity_allocations;
