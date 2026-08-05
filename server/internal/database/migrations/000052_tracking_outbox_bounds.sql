WITH ranked_pending AS (
    SELECT pending.id,
           row_number() OVER (
               PARTITION BY pending.profile_id, pending.provider, pending.title_id, pending.event_type
               ORDER BY EXISTS (
                   SELECT 1
                   FROM profile_tracking_event_heads head
                   WHERE head.profile_id = pending.profile_id
                     AND head.provider = pending.provider
                     AND head.title_id = pending.title_id
                     AND head.event_type = pending.event_type
                     AND head.idempotency_key = pending.idempotency_key
               ) DESC,
               pending.enqueue_sequence DESC
           ) AS position
    FROM profile_tracking_outbox pending
    WHERE pending.leased_until IS NULL
)
DELETE FROM profile_tracking_outbox pending
USING ranked_pending ranked
WHERE pending.id = ranked.id
  AND ranked.position > 1;

ALTER TABLE profile_tracking_outbox
    DROP CONSTRAINT IF EXISTS profile_tracking_outbox_profile_id_provider_idempotency_key_key;

CREATE UNIQUE INDEX IF NOT EXISTS profile_tracking_outbox_pending_event_idx
    ON profile_tracking_outbox (profile_id, provider, title_id, event_type)
    WHERE leased_until IS NULL;
