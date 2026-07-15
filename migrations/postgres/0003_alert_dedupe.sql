-- The throttle check queries alert_history by (rule_id, payload->>'dedupe_key',
-- fired_at). The existing alert_history_rule_idx covers (rule_id, fired_at DESC),
-- which already serves it well; this adds an expression index on the dedupe key
-- so the NOT EXISTS probe is an index lookup rather than a filter over every
-- past fire of the rule.
CREATE INDEX IF NOT EXISTS alert_history_dedupe_idx
    ON alert_history (rule_id, (payload ->> 'dedupe_key'), fired_at DESC);
