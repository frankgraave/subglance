-- Capture decisions are historical facts, not today's monitor configuration.
-- NULL deliberately leaves old rows and failures without evidence unknown.
-- 0010 is reserved for resolved incident history; migrations are name-ledgered.
ALTER TABLE heartbeats ADD COLUMN response_capture_reason TEXT
    CHECK (response_capture_reason IN ('disabled', 'flapping', 'budget'));
