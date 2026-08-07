-- Development databases may already have applied migration 64 before it removed
-- the device rows owned by the legacy compatibility mappings. A cutover-revoked
-- native session is the durable evidence for those rows. Preserve any device
-- that has since gained a non-cutover session or a current profile mapping.
DELETE FROM devices legacy_device
WHERE EXISTS (
    SELECT 1
    FROM auth_sessions cutover_session
    WHERE cutover_session.device_id = legacy_device.id
      AND cutover_session.revoked_reason = 'jellyfin_profile_credential_cutover'
)
AND NOT EXISTS (
    SELECT 1
    FROM auth_sessions retained_session
    WHERE retained_session.device_id = legacy_device.id
      AND retained_session.revoked_reason IS DISTINCT FROM 'jellyfin_profile_credential_cutover'
)
AND NOT EXISTS (
    SELECT 1
    FROM jellyfin_compat_devices current_mapping
    WHERE current_mapping.device_id = legacy_device.id
);
