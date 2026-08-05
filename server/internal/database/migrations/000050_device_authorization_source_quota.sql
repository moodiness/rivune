DO $migration$
BEGIN
    ALTER TABLE device_authorizations
        ADD COLUMN source_hash bytea;

    -- Legacy rows have no trustworthy network owner. Group them with the same
    -- fail-closed sentinel used when a new request has no canonical source.
    UPDATE device_authorizations
    SET source_hash = decode('6a4470ab4d6fdca7987a962ea21b8c561c96d57133f89eb150e1e709476c1165', 'hex')
    WHERE source_hash IS NULL;

    ALTER TABLE device_authorizations
        ALTER COLUMN source_hash SET NOT NULL,
        ADD CONSTRAINT device_authorizations_source_hash_length
            CHECK (octet_length(source_hash) = 32);

    CREATE INDEX device_authorizations_active_source_expiry_idx
        ON device_authorizations (source_hash, expires_at)
        WHERE consumed_at IS NULL;
END
$migration$;
