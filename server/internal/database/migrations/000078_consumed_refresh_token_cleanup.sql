WITH paired_native_sessions AS (
    UPDATE auth_sessions session
    SET refresh_expires_at = '9999-12-31 23:59:59+00'::timestamptz
    FROM devices device
    WHERE session.device_id = device.id
      AND session.authorization_scope = 'category'
      AND session.revoked_at IS NULL
      AND session.refresh_expires_at > now()
      AND device.approved_at IS NOT NULL
      AND device.platform IN ('android', 'android_tv', 'ios', 'tvos', 'visionos', 'macos', 'apple', 'windows')
    RETURNING session.id
)
UPDATE auth_refresh_tokens token
SET expires_at = '9999-12-31 23:59:59+00'::timestamptz
FROM paired_native_sessions session
WHERE token.session_id = session.id
  AND token.consumed_at IS NULL;

CREATE INDEX auth_refresh_tokens_consumed_cleanup_idx
    ON auth_refresh_tokens (consumed_at, token_hash)
    WHERE consumed_at IS NOT NULL;
