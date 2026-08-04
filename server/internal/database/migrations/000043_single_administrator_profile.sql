BEGIN;

WITH ranked_administrator_profiles AS (
    SELECT access.user_id,
           access.profile_id,
           row_number() OVER (
               PARTITION BY access.user_id
               ORDER BY profile.created_at, profile.id
           ) AS profile_ordinal
    FROM user_profile_access access
    JOIN users account ON account.id = access.user_id
    JOIN profiles profile ON profile.id = access.profile_id
    WHERE account.role = 'admin'
)
UPDATE user_profile_access access
SET can_manage = ranked.profile_ordinal = 1
FROM ranked_administrator_profiles ranked
WHERE access.user_id = ranked.user_id
  AND access.profile_id = ranked.profile_id
  AND access.can_manage IS DISTINCT FROM (ranked.profile_ordinal = 1);

COMMIT;
